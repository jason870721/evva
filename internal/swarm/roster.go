package swarm

import (
	"fmt"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/llm"
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

// RunPhase is the fine-grained, event-derived run sub-phase (RP-3). It refines
// the coarse RunStatus the supervisor sets (idle/busy/suspended) into the same
// vocabulary evva's TUI shows — running / thinking / executing / … — plus the
// swarm-critical WAITING_APPROVAL / WAITING_INPUT, so an operator can tell a long
// tool call from a hang from a blocked approval instead of seeing a flat "busy".
// It is derived purely from the member's event stream (see phaseDeriver); the
// coarse RunStatus stays authoritative for lifecycle (suspended) and for
// event-less callers. Composition for display: a suspended member shows
// "suspended" (coarse wins); otherwise the phase.
type RunPhase string

const (
	PhaseReady           RunPhase = "ready"            // idle — burns no tokens
	PhaseRunning         RunPhase = "running"          // loop alive between sub-phases
	PhaseThinking        RunPhase = "thinking"         // model generating reasoning
	PhaseTexting         RunPhase = "texting"          // model generating response text
	PhaseExecuting       RunPhase = "executing"        // a tool call is in flight (Tool names it)
	PhaseWaitingApproval RunPhase = "waiting-approval" // blocked in the permission broker (Tool names it)
	PhaseWaitingInput    RunPhase = "waiting-input"    // blocked in the question broker
	PhaseDraining        RunPhase = "draining"         // folding async results / inbox
	PhaseCompacting      RunPhase = "compacting"       // session compaction
	PhasePaused          RunPhase = "paused"           // iteration limit
	PhaseError           RunPhase = "error"            // last run failed
)

// rosterEntry is the internal record. The Controller handle is unexported —
// only the supervisor/webapi reach it via Controller(); read surfaces get a
// MemberView (no handle) from Snapshot.
type rosterEntry struct {
	name        string
	role        agentdef.Role
	membership  Membership
	run         RunStatus // coarse lifecycle, supervisor-set (idle/busy/suspended)
	phase       RunPhase  // fine sub-phase, event-derived (refines busy)
	tool        string    // tool name for executing / waiting-approval phases
	phaseSince  int64     // unix millis the current phase was entered (for "stuck for N" timing)
	currentTask int64
	whenToUse   string
	ctl         ui.Controller

	// Token metering (RP-13), pushed by the supervisor at run boundaries (the
	// member's own loop goroutine reads the controller; the roster only stores
	// the snapshot — no concurrent session reads from display goroutines).
	usage         llm.Usage // cumulative session usage as of the last run boundary
	lastTurnInput int       // input tokens of the most recent turn (context pressure)
	dailyTokens   int       // input+output tokens spent today (meter day)
}

// MemberView is a read-only snapshot of one member, the shape served to the
// list_members tool and the web API.
type MemberView struct {
	Name        string
	Role        agentdef.Role
	Membership  Membership
	Run         RunStatus
	Phase       RunPhase
	Tool        string
	PhaseSince  int64 // unix millis the current phase was entered
	CurrentTask int64
	WhenToUse   string

	Usage         llm.Usage // cumulative session tokens as of the last run boundary (RP-13)
	LastTurnInput int       // most recent turn's input tokens (context pressure)
	DailyTokens   int       // tokens spent today (the budget breaker's counter)
}

// DisplayPhase composes the coarse run status and the fine event-derived phase
// into the single label shown to operators and teammates. A suspended member
// reads "suspended" (the coarse status wins, since the deriver may have moved to
// "ready" right after the cancel); otherwise the fine phase, with the tool name
// appended for the executing / waiting-approval phases ("executing:bash"). An
// empty phase falls back to the coarse status. The web composes the same rule.
func (v MemberView) DisplayPhase() string {
	if v.Run == RunSuspended {
		return string(RunSuspended)
	}
	if v.Phase == "" {
		return string(v.Run)
	}
	if v.Tool != "" {
		return string(v.Phase) + ":" + v.Tool
	}
	return string(v.Phase)
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
	e := &rosterEntry{
		name:       name,
		role:       role,
		membership: MembershipActive,
		run:        RunIdle,
		phase:      PhaseReady,
		phaseSince: time.Now().UnixMilli(),
		whenToUse:  whenToUse,
		ctl:        ctl,
	}
	r.entries[name] = e
	r.order = append(r.order, name)
	return nil
}

// remove drops a member from the roster entirely (RP-8 web remove). Unlike
// freeze (which keeps the seat), this forgets the member — both the entry and
// its slot in the insertion order. Unknown names are a no-op.
func (r *Roster) remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; !ok {
		return
	}
	delete(r.entries, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// roleOf returns a member's role and whether it exists. RemoveMember reads it to
// enforce leader-uniqueness (the leader can never be removed — RP-8 §3.E).
func (r *Roster) roleOf(name string) (agentdef.Role, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		return e.role, true
	}
	return "", false
}

// Controller returns a member's handle by member name, for the
// supervisor/webapi to drive.
func (r *Roster) Controller(name string) (ui.Controller, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil, false
	}
	return e.ctl, true
}

// ControllerRef resolves a controller by either member name OR controller
// AgentID. Internal callers (supervisor, lifecycle commands) use the name; the
// web approval/question reply path carries the event's AgentID instead, so it
// must resolve too — without this, every web-driven RespondPermission misses
// (name-keyed lookup, AgentID ref) and the blocked tool hangs forever.
//
// The name lookup is the O(1) common path; an AgentID falls through to a scan
// (AgentID() just reads a string field, the member count is small, and gate
// replies are human-paced — so the scan is free in practice and we avoid a
// second index to keep in sync). The two namespaces don't overlap (UUID hex vs
// human names), so name-first is unambiguous.
func (r *Roster) ControllerRef(ref string) (ui.Controller, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[ref]; ok {
		return e.ctl, true
	}
	for _, e := range r.entries {
		if e.ctl != nil && e.ctl.AgentID() == ref {
			return e.ctl, true
		}
	}
	return nil, false
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
			Name:          e.name,
			Role:          e.role,
			Membership:    e.membership,
			Run:           e.run,
			Phase:         e.phase,
			Tool:          e.tool,
			PhaseSince:    e.phaseSince,
			CurrentTask:   e.currentTask,
			WhenToUse:     e.whenToUse,
			Usage:         e.usage,
			LastTurnInput: e.lastTurnInput,
			DailyTokens:   e.dailyTokens,
		})
	}
	return out
}

// ActiveMembers returns the names of active (in-service) members in insertion
// order. It is the bus.Membership view used to expand a "to: all" broadcast;
// frozen members are excluded so a broadcast never reaches them (SPRD-1-5).
func (r *Roster) ActiveMembers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if r.entries[name].membership == MembershipActive {
			out = append(out, name)
		}
	}
	return out
}

// ResolveRecipient maps a send target to a concrete member name. An exact member
// name and the "all" broadcast pass through unchanged; otherwise the target is
// treated as a ROLE and resolved to the unique active member of that role — so a
// worker can address "leader" without knowing the leader's member name (which in
// practice often differs, e.g. "lead"/"pm"). An ambiguous role (more than one
// active member, e.g. "worker") or an unknown target is returned unchanged for
// the caller to reject with a helpful error.
func (r *Roster) ResolveRecipient(to string) string {
	if r == nil || to == "" {
		return to // no roster to resolve against (e.g. a store-only test space)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.entries[to]; ok {
		return to // already an exact member name
	}
	var match string
	n := 0
	for _, name := range r.order {
		e := r.entries[name]
		if string(e.role) == to && e.membership == MembershipActive {
			match, n = name, n+1
		}
	}
	if n == 1 {
		return match
	}
	return to // ambiguous or no role match — leave for the caller to reject
}

// membership returns a member's membership and whether the member exists. The
// supervisor's scheduler reads it to gate wakes (frozen members never run).
func (r *Roster) membership(name string) (Membership, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		return e.membership, true
	}
	return "", false
}

// runOf returns a member's coarse run status and whether the member exists. The
// scheduler reads it to skip a timer tick for a member that is already running
// (RP-7 §3.6): a scheduled wake is a recurring patrol, not a queued job, so a
// busy member's tick is dropped rather than buffered to catch up later.
func (r *Roster) runOf(name string) (RunStatus, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		return e.run, true
	}
	return "", false
}

// setRun updates a member's coarse run status (supervisor lifecycle). It also
// keeps the fine phase coherent for event-less callers and at run boundaries:
// going busy seeds PhaseRunning (the event deriver then refines it), going idle
// resets to PhaseReady (so no stale sub-phase lingers once the run is over). A
// suspended member keeps its phase — display composition lets the coarse
// "suspended" win. Unknown names are ignored.
func (r *Roster) setRun(name string, s RunStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return
	}
	e.run = s
	switch s {
	case RunBusy:
		e.setPhase(PhaseRunning, "")
	case RunIdle:
		e.setPhase(PhaseReady, "")
	}
}

// setPhase records the event-derived fine sub-phase (and the tool name for
// executing / waiting-approval). Called by a member's sink as events flow; it
// never touches the coarse run status, so the supervisor's lifecycle and the
// deriver's sub-phase compose cleanly. Unknown names are ignored.
func (r *Roster) setPhase(name string, p RunPhase, tool string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.setPhase(p, tool)
	}
}

// setPhase assigns the entry's phase + tool and stamps phaseSince when the phase
// actually changes, so "how long in this phase" stays meaningful (a repeated
// same-phase write — e.g. tool name unchanged — doesn't reset the clock).
func (e *rosterEntry) setPhase(p RunPhase, tool string) {
	if e.phase != p {
		e.phaseSince = time.Now().UnixMilli()
	}
	e.phase, e.tool = p, tool
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

// LeaderName returns the unique leader's member name, or "" when the roster
// has none (e.g. a store-only test space). Exported for the proposal tools
// (RP-23), which address the leader without knowing its actual name; the
// budget breaker and watchdog use it via the unexported alias below.
func (r *Roster) LeaderName() string {
	return r.leaderName()
}

// leaderName returns the unique leader's member name, or "" when the roster has
// none (e.g. a store-only test space). Used by the budget breaker to address
// its notification mail without the caller knowing the leader's actual name.
func (r *Roster) leaderName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range r.order {
		if r.entries[name].role == agentdef.RoleLeader {
			return name
		}
	}
	return ""
}

// setUsage stores a member's token snapshot (RP-13). Called by the supervisor
// at run boundaries — on the member's own loop goroutine, where reading the
// controller's session is race-free — so every display surface (list_members,
// web) reads this stored copy instead of touching the live session.
func (r *Roster) setUsage(name string, u llm.Usage, lastTurnInput, dailyTokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.usage = u
		e.lastTurnInput = lastTurnInput
		e.dailyTokens = dailyTokens
	}
}
