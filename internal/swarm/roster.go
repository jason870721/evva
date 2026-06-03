package swarm

import (
	"fmt"
	"sync"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/ui"
)

// Membership is the first of the two orthogonal status dimensions (design
// §5.1): whether a member is in service (active) or cold-stored (frozen). v1
// never deletes — freeze is the safe "offline".
type Membership string

const (
	MembershipActive Membership = "active"
	MembershipFrozen Membership = "frozen"
)

// RunStatus is the second dimension, meaningful only while active: idle (not in
// a run, burning no tokens), busy (Controller.Run in flight), or suspended (the
// run context was cancelled).
type RunStatus string

const (
	RunIdle      RunStatus = "idle"
	RunBusy      RunStatus = "busy"
	RunSuspended RunStatus = "suspended"
)

// rosterEntry is the internal record. The Controller handle is unexported —
// only the supervisor/webapi reach it via Controller(); read surfaces get a
// MemberView (no handle) from Snapshot.
type rosterEntry struct {
	name        string
	role        agentdef.Role
	membership  Membership
	run         RunStatus
	currentTask int64
	whenToUse   string
	ctl         ui.Controller
}

// MemberView is a read-only snapshot of one member, the shape served to the
// list_members tool and the web API.
type MemberView struct {
	Name        string
	Role        agentdef.Role
	Membership  Membership
	Run         RunStatus
	CurrentTask int64
	WhenToUse   string
}

// Roster is the per-space, thread-safe member directory — the single source of
// truth feeding both list_members and /api/swarm/:id.
type Roster struct {
	mu      sync.RWMutex
	order   []string // insertion order, for deterministic snapshots
	entries map[string]*rosterEntry
}

func newRoster() *Roster {
	return &Roster{entries: make(map[string]*rosterEntry)}
}

// add inserts a member as active+idle. Names are unique within the space
// (invariant #2 — per-space name scoping); a collision errors.
func (r *Roster) add(name string, role agentdef.Role, whenToUse string, ctl ui.Controller) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.entries[name]; dup {
		return fmt.Errorf("roster: duplicate member %q in space", name)
	}
	r.entries[name] = &rosterEntry{
		name:       name,
		role:       role,
		membership: MembershipActive,
		run:        RunIdle,
		whenToUse:  whenToUse,
		ctl:        ctl,
	}
	r.order = append(r.order, name)
	return nil
}

// Controller returns a member's handle, for the supervisor/webapi to drive.
func (r *Roster) Controller(name string) (ui.Controller, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil, false
	}
	return e.ctl, true
}

// Names returns the member names in insertion order.
func (r *Roster) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Snapshot returns a copy of every member in insertion order — for
// list_members and the web API. No Controller handles cross this boundary.
func (r *Roster) Snapshot() []MemberView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]MemberView, 0, len(r.order))
	for _, name := range r.order {
		e := r.entries[name]
		out = append(out, MemberView{
			Name:        e.name,
			Role:        e.role,
			Membership:  e.membership,
			Run:         e.run,
			CurrentTask: e.currentTask,
			WhenToUse:   e.whenToUse,
		})
	}
	return out
}

// setRun updates a member's run status (used by the scheduler/supervisor in
// SPRD-1-6). Unknown names are ignored.
func (r *Roster) setRun(name string, s RunStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.run = s
	}
}

// setMembership updates a member's membership (freeze/unfreeze; SPRD-1-6).
func (r *Roster) setMembership(name string, m Membership) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.membership = m
	}
}

// setCurrentTask records the task a member is working (SPRD-1-6/1-7).
func (r *Roster) setCurrentTask(name string, taskID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.currentTask = taskID
	}
}
