package swarm

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/skill"
)

// Supervisor owns one space's lifecycle and run engine: it launches a
// recover-guarded run loop per member, turns the three wake sources (message,
// task, timer — §5.5) into Controller.Run calls, and exposes membership
// (AddMember/Freeze/Unfreeze) and run control (Suspend/Resume/HaltAll). It holds
// each in-flight run's cancel so a Suspend is a deterministic ctx-cancel.
//
// Mechanically there are two wake channels — the bus mailbox (message, which
// also carries task-assignment messages from 1-7's task_assign) and a per-agent
// timer/resume poke. "Idle burns no tokens": with no wake there is no Run.
type Supervisor struct {
	sp    *SwarmSpace
	bus   *bus.Bus
	store *store.Store
	log   *slog.Logger

	tickInterval   time.Duration
	rescanInterval time.Duration

	// mu guards members + each member's schedule/nextDue (only the tick touches
	// those). A member's volatile run state (suspended/cancelRun) is guarded by
	// the member's own mutex; see memberRun.
	mu      sync.Mutex
	members map[string]*memberRun
	rootCtx context.Context
	started bool

	// wg tracks every goroutine launched under the supervisor's context — the
	// per-member run loops plus the timer/rescan ticks. Wait blocks on it so a
	// teardown can drain the run engine BEFORE the store closes; without it a
	// serve goroutine mid-ClaimUnread races the Store.Close and hits a closed DB.
	wg sync.WaitGroup
}

// defaultTickInterval is the scheduler's timer resolution. One second comfortably
// catches minute-resolution cron and short interval schedules; tests shrink it.
const defaultTickInterval = time.Second

// defaultRescanInterval is how often the safety re-scan (rescanTick) pokes idle
// members that the store says still have unread mail — the backstop for a wake
// hint that was dropped entirely (so a member was never woken at all, which the
// level-triggered drain can't catch on its own). Coarse on purpose: it only ever
// converts a permanent stall into a ≤interval delay; tests shrink it.
const defaultRescanInterval = 8 * time.Second

// NewSupervisor builds a supervisor over an assembled space. Call Start to bring
// the run loops up.
func NewSupervisor(sp *SwarmSpace) *Supervisor {
	s := &Supervisor{
		sp:    sp,
		bus:   sp.Bus,
		store: sp.Store,
		// Default to the process logger (the daemon routes it to the service
		// log) rather than io.Discard, so a swarm runs observable out of the
		// box; SetLogger overrides it. The old discard default is why the
		// run loop was invisible during debugging.
		log:            slog.Default(),
		tickInterval:   defaultTickInterval,
		rescanInterval: defaultRescanInterval,
		members:        make(map[string]*memberRun),
	}
	// Back-reference so the leader's schedule tools (which hold only the space)
	// can reach this run engine via sp.SetMemberSchedule. One supervisor per
	// space; set before Start, before any tool can fire.
	sp.mu.Lock()
	sp.super = s
	sp.mu.Unlock()
	return s
}

// SetLogger swaps the supervisor's logger. The service wires its own logger in
// so every member's wake/run lifecycle lands in the same log stream.
func (s *Supervisor) SetLogger(l *slog.Logger) {
	if l != nil {
		s.log = l
	}
}

// Start launches a run loop per current member plus the one timer tick. It is
// idempotent. Everything lives until ctx is cancelled — pass the space's context
// so SwarmSpace.Shutdown also stops the supervisor.
func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.rootCtx = ctx
	s.mu.Unlock()

	for _, name := range s.sp.Roster.Names() {
		s.startMemberLoop(ctx, name)
	}
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.timerTick(ctx) }()
	go func() { defer s.wg.Done(); s.rescanTick(ctx) }()
}

// Wait blocks until every run loop and tick goroutine started under the
// supervisor's context has returned. Call it AFTER cancelling that context
// (otherwise the loops never exit and Wait hangs) and BEFORE closing the space
// store, so no run loop can touch a closed DB. teardownSpace orders it exactly
// so; tests do the same in their cleanup.
func (s *Supervisor) Wait() { s.wg.Wait() }

// AddMember hot-loads agents/sub/<name>/ into the live space — roster entry,
// mailbox, and run loop — with no restart (design §5.4, user-triggered). Start
// must have run first.
func (s *Supervisor) AddMember(name string) error {
	s.mu.Lock()
	started, ctx := s.started, s.rootCtx
	s.mu.Unlock()
	if !started {
		return fmt.Errorf("swarm: AddMember %q before Start", name)
	}

	dir := filepath.Join(s.sp.Workdir, "agents", "sub", name)
	ld, err := s.sp.loader.Build(dir, agentdef.RoleWorker)
	if err != nil {
		return fmt.Errorf("swarm: add member %q: %w", name, err)
	}
	s.sp.registerDef(ld)
	if err := s.sp.constructMember(ld); err != nil {
		return fmt.Errorf("swarm: add member %q: %w", name, err)
	}
	s.startMemberLoop(ctx, name)
	s.sp.persistRuntime()
	return nil
}

// CreateMember authors a brand-new worker from an operator spec (RP-8): it writes
// the agent dir, then hot-loads it through the exact AddMember path
// (register→construct→startLoop→persist). A roster pre-check rejects a duplicate
// before touching disk; if the hot-load fails after the dir is written, the dir
// is rolled back so a failed create leaves no half state. The manifest rewrite
// (so the member survives restart) is the service's job, layered on top.
func (s *Supervisor) CreateMember(spec agentdef.MemberSpec) error {
	name := spec.Name
	if _, ok := s.sp.Roster.membership(name); ok {
		return fmt.Errorf("swarm: member %q already exists", name)
	}
	// Two callers converge here: the web form authors a NEW definition (full spec,
	// no dir yet → write it); `evva swarm add-member <name>` MOUNTS an existing
	// on-disk dir (name only, no system prompt → skip the write). A fresh spec
	// whose name collides with a leftover dir is an ambiguous overwrite — reject
	// it rather than silently mount or clobber.
	dirExists := agentdef.MemberDirExists(s.sp.Workdir, name)
	wroteDir := false
	switch {
	case dirExists && strings.TrimSpace(spec.SystemPrompt) != "":
		return fmt.Errorf("swarm: a definition for %q already exists on disk — remove it first or pick another name", name)
	case !dirExists:
		if err := agentdef.WriteMemberDir(s.sp.Workdir, spec); err != nil {
			return err
		}
		wroteDir = true
	}
	if err := s.AddMember(name); err != nil {
		if wroteDir {
			_ = agentdef.RemoveMemberDir(s.sp.Workdir, name) // roll back the just-written dir
		}
		return err
	}
	return nil
}

// RemoveMember retires a worker from the live space (RP-8): it stops the member's
// run loop and any in-flight run, drops it from the roster, stops mail delivery,
// and tears down its agent — then persists the new membership. The LEADER can
// never be removed (it is unique). On-disk concerns — dropping it from the
// manifest and optionally deleting its dir — are the service's job (ordered so a
// restart never references a missing dir). The .vero ledger is untouched (v1
// never deletes history).
func (s *Supervisor) RemoveMember(name string) error {
	role, ok := s.sp.Roster.roleOf(name)
	if !ok {
		return fmt.Errorf("swarm: remove: unknown member %q", name)
	}
	if role == agentdef.RoleLeader {
		return fmt.Errorf("swarm: the leader cannot be removed")
	}
	s.mu.Lock()
	m := s.members[name]
	delete(s.members, name)
	s.mu.Unlock()
	if m != nil {
		m.mu.Lock()
		if m.loopCancel != nil {
			m.loopCancel()
		}
		if m.cancelRun != nil {
			m.cancelRun()
		}
		m.mu.Unlock()
	}
	s.sp.Roster.remove(name)
	s.sp.Bus.Deregister(name)
	s.sp.removeAgent(name)
	s.sp.persistRuntime()
	return nil
}

// Freeze cold-stores a member: it keeps its mailbox and history but is never
// scheduled again until Unfreeze. An in-flight run is left to finish (freeze
// stops future dispatch, it is not a kill).
func (s *Supervisor) Freeze(name string) error {
	if _, ok := s.sp.Roster.membership(name); !ok {
		return fmt.Errorf("swarm: freeze: unknown member %q", name)
	}
	s.sp.Roster.setMembership(name, MembershipFrozen)
	s.sp.persistRuntime()
	return nil
}

// Unfreeze returns a member to service and pokes it to drain any mail that
// queued while it was frozen.
func (s *Supervisor) Unfreeze(name string) error {
	if _, ok := s.sp.Roster.membership(name); !ok {
		return fmt.Errorf("swarm: unfreeze: unknown member %q", name)
	}
	s.sp.Roster.setMembership(name, MembershipActive)
	s.sp.persistRuntime()
	if m := s.memberOf(name); m != nil {
		s.poke(m, wakeMessage)
	}
	return nil
}

// SetSchedule puts a member on (or replaces) a recurring timer schedule and
// applies it to the running loop immediately (fixing the old "seeded once at
// startMemberLoop" gap, RP-7 §3.4). It updates two representations in separate
// critical sections to preserve the package lock order s.mu → sp.mu (never
// nested): the live memberRun (read by the tick under s.mu) and sp.schedules
// (the declared/persist source under sp.mu), then persists so a leader-set cron
// survives a service restart. A bad cron or empty spec is rejected up front.
func (s *Supervisor) SetSchedule(name string, sch agentdef.Schedule) error {
	if err := sch.Validate(); err != nil {
		return err
	}
	due, err := sch.Next(time.Now())
	if err != nil {
		return err
	}
	s.mu.Lock()
	if m, ok := s.members[name]; ok {
		cp := sch
		m.schedule = &cp
		m.nextDue = due
	}
	s.mu.Unlock()

	s.sp.mu.Lock()
	s.sp.schedules[name] = sch
	s.sp.mu.Unlock()

	s.sp.persistRuntime()
	return nil
}

// ClearSchedule removes a member's recurring schedule from both the running loop
// and the declared/persist source, then persists (so the removal survives a
// restart — a manifest-declared schedule the leader cleared stays cleared). A
// member with no schedule is a no-op.
func (s *Supervisor) ClearSchedule(name string) error {
	s.mu.Lock()
	if m, ok := s.members[name]; ok {
		m.schedule = nil
		m.nextDue = time.Time{}
	}
	s.mu.Unlock()

	s.sp.mu.Lock()
	delete(s.sp.schedules, name)
	s.sp.mu.Unlock()

	s.sp.persistRuntime()
	return nil
}

// ReloadMemberSkills re-scans a member's on-disk skills directory and re-renders its
// system prompt to match (RP-10-4) — the seam the web add/remove-skill path drives
// after writing or deleting a SKILL.md. It rebuilds the registry from disk (sidestep-
// ping pkg/skill's lack of a Remove: a full re-scan is the source of truth, exactly
// how the member was first constructed), then applies it at the member's next RUN
// BOUNDARY: an idle member is poked and applies on the spot; a busy one stashes the
// new catalog and the run loop swaps it in once the current run ends (serve), so an
// in-flight conversation never sees its prompt change underfoot.
func (s *Supervisor) ReloadMemberSkills(name string) error {
	role, ok := s.sp.Roster.roleOf(name)
	if !ok {
		return fmt.Errorf("swarm: reload skills: unknown member %q", name)
	}
	m := s.memberOf(name)
	if m == nil {
		return fmt.Errorf("swarm: reload skills: unknown member %q", name)
	}
	// skill.LoadRegistry never errors (a missing dir is the empty registry) and reads
	// ONLY the member's own dir — no bundled/global overlay — matching construction.
	reg, _ := skill.LoadRegistry(agentdef.SkillsDir(s.sp.Workdir, role, name), "")
	m.mu.Lock()
	m.pendingSkills = reg
	m.mu.Unlock()
	s.poke(m, wakeMessage)
	return nil
}

// applyMemberSkills installs a rebuilt catalog on a member's live agent through the
// public reload seam. Called ONLY from serve (the run-loop goroutine, at a run
// boundary), so the prompt swap never races the member's own run.
func (s *Supervisor) applyMemberSkills(name string, reg *skill.Registry) {
	ag, ok := s.sp.agentOf(name)
	if !ok {
		return
	}
	r, ok := ag.(agent.SkillReloader)
	if !ok {
		s.log.Warn("swarm: agent does not support skill reload", "member", name)
		return
	}
	if err := r.ReloadSkills(reg); err != nil {
		s.log.Warn("swarm: reload member skills", "member", name, "err", err)
	}
}

// Suspend stops a member's current run (cancel its ctx) and parks it: further
// wakes do nothing until Resume. The unread messages it was working stay unread
// (the DB is truth), so Resume re-processes them.
func (s *Supervisor) Suspend(name string) error {
	m := s.memberOf(name)
	if m == nil {
		return fmt.Errorf("swarm: suspend: unknown member %q", name)
	}
	m.mu.Lock()
	m.suspended = true
	cancel := m.cancelRun
	s.sp.Roster.setRun(name, RunSuspended)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Resume un-parks a suspended member and pokes it to re-run its unread work.
func (s *Supervisor) Resume(name string) error {
	m := s.memberOf(name)
	if m == nil {
		return fmt.Errorf("swarm: resume: unknown member %q", name)
	}
	m.mu.Lock()
	m.suspended = false
	s.sp.Roster.setRun(name, RunIdle)
	m.mu.Unlock()
	s.poke(m, wakeMessage)
	return nil
}

// HaltAll suspends every member and cancels every in-flight run — the Phase-2
// kill switch. Members come back individually via Resume (or on restart).
func (s *Supervisor) HaltAll() error {
	for _, name := range s.sp.Roster.Names() {
		_ = s.Suspend(name)
	}
	return nil
}

// memberOf returns a member's run control, or nil if unknown.
func (s *Supervisor) memberOf(name string) *memberRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.members[name]
}

// isActive reports whether a member is currently schedulable (membership active).
func (s *Supervisor) isActive(name string) bool {
	m, ok := s.sp.Roster.membership(name)
	return ok && m == MembershipActive
}
