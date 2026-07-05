// This file is the DWF ephemeral-member layer: member_spawn clones an
// existing worker's definition under a derived name — no agent dir written,
// no manifest touch (the deliberate divergence from the operator's
// CreateMember, which authors NEW definitions) — and auto-retires it when its
// assigned work completes. Clones re-enter the exact registerDef →
// constructMember chokepoints their base was built through, so team protocol,
// memory dir, member context, and permission stance all compose for the
// clone's own name.

package swarm

import (
	"fmt"
	"maps"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

// Retire policies for spawned members.
const (
	// RetireOnComplete (default): the supervisor retires the clone once it has
	// completed at least one task, holds no incomplete ones, is idle, and has
	// an empty mailbox — never mid-run (the sweep runs between wakes).
	RetireOnComplete = "on_complete"
	// RetireManual: the clone lives until the leader's member_retire or an
	// operator removal.
	RetireManual = "manual"
)

// SpawnedMeta records one ephemeral member's provenance (DWF).
type SpawnedMeta struct {
	From   string `json:"from"`   // base member the clone was made from
	Retire string `json:"retire"` // RetireOnComplete | RetireManual
	Seq    int    `json:"seq"`    // per-base clone number; never reused
}

// CloneMember builds a live duplicate of an existing member's definition
// under a new name: register (protocol re-derived for the clone's name) →
// construct (fresh config clone; same model/effort/permission resolution as
// the base) → mailbox + roster. Schedules deliberately do NOT clone — a
// duplicated cron duty is never what fan-out means; manifest budget
// overrides DO (a clone spends under its base's cap semantics).
// The caller records provenance and starts the run loop.
func (sp *SwarmSpace) CloneMember(base, clone string) error {
	sp.mu.Lock()
	raw, ok := sp.defs[base]
	budget, hasBudget := sp.budgets[base]
	sp.mu.Unlock()
	if !ok {
		return fmt.Errorf("swarm: clone: no member definition %q", base)
	}
	if raw.Role != agentdef.RoleWorker {
		return fmt.Errorf("swarm: clone: %q is the leader — only workers spawn", base)
	}
	if _, exists := sp.Roster.membership(clone); exists {
		return fmt.Errorf("swarm: clone: member %q already exists", clone)
	}

	ld := raw
	ld.Def.Name = clone
	ld.Schedule = nil
	if ld.FromPersona {
		ld.PersonaSource = base
	}
	if err := sp.registerDef(&ld); err != nil {
		return fmt.Errorf("swarm: clone %q from %q: %w", clone, base, err)
	}
	if err := sp.constructMember(ld); err != nil {
		return fmt.Errorf("swarm: clone %q from %q: %w", clone, base, err)
	}
	if hasBudget {
		sp.mu.Lock()
		if sp.budgets == nil {
			sp.budgets = map[string]int{}
		}
		sp.budgets[clone] = budget
		sp.mu.Unlock()
	}
	return nil
}

// nextCloneName derives the clone's name: "<base>-<seq>", seq monotonic per
// base (starting at 2 — the base is implicitly #1), bumped past any existing
// member or definition so a hand-authored "backend-a-2" can never collide.
// Sequences only grow, so a retired clone's name — and therefore its
// transcript slug — is never reused within the space's life.
func (sp *SwarmSpace) nextCloneName(base string) (string, int) {
	sp.mu.Lock()
	seq := 1
	for _, meta := range sp.spawned {
		if meta.From == base && meta.Seq > seq {
			seq = meta.Seq
		}
	}
	taken := func(name string) bool {
		_, ok := sp.defs[name]
		return ok
	}
	var name string
	for {
		seq++
		name = fmt.Sprintf("%s-%d", base, seq)
		if !taken(name) {
			break
		}
	}
	sp.mu.Unlock()
	for {
		if _, live := sp.Roster.membership(name); !live {
			return name, seq
		}
		seq++
		name = fmt.Sprintf("%s-%d", base, seq)
	}
}

// recordSpawned books a live clone's provenance (persisted by the caller's
// persistRuntime).
func (sp *SwarmSpace) recordSpawned(name string, meta SpawnedMeta) {
	sp.mu.Lock()
	if sp.spawned == nil {
		sp.spawned = map[string]SpawnedMeta{}
	}
	sp.spawned[name] = meta
	sp.mu.Unlock()
}

// dropSpawned forgets a clone's provenance — called from RemoveMember so
// operator removals and retires converge on one cleanup. Reports whether the
// member WAS spawned, so the caller can audit the retirement.
func (sp *SwarmSpace) dropSpawned(name string) bool {
	sp.mu.Lock()
	_, was := sp.spawned[name]
	delete(sp.spawned, name)
	sp.mu.Unlock()
	return was
}

// SpawnedInfo reports whether name is an ephemeral member, and its provenance.
func (sp *SwarmSpace) SpawnedInfo(name string) (SpawnedMeta, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	meta, ok := sp.spawned[name]
	return meta, ok
}

// spawnedSnapshot copies the live spawn map for lock-free iteration.
func (sp *SwarmSpace) spawnedSnapshot() map[string]SpawnedMeta {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	out := make(map[string]SpawnedMeta, len(sp.spawned))
	maps.Copy(out, sp.spawned)
	return out
}

// SpawnWorker and RetireWorker are the leader tools' seam to the run engine
// (the SetMemberSchedule precedent): the space delegates to its supervisor,
// which owns loops and lifecycle. A lite space (tests, no supervisor) refuses.

func (sp *SwarmSpace) SpawnWorker(base, retire string) (string, error) {
	if sp.super == nil {
		return "", fmt.Errorf("swarm: spawn: space has no supervisor")
	}
	return sp.super.SpawnMember(base, retire)
}

func (sp *SwarmSpace) RetireWorker(name string) error {
	if sp.super == nil {
		return fmt.Errorf("swarm: retire: space has no supervisor")
	}
	return sp.super.RetireSpawned(name)
}
