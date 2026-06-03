package swarm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
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

	tickInterval time.Duration

	// mu guards members + each member's schedule/nextDue (only the tick touches
	// those). A member's volatile run state (suspended/cancelRun) is guarded by
	// the member's own mutex; see memberRun.
	mu      sync.Mutex
	members map[string]*memberRun
	rootCtx context.Context
	started bool
}

// defaultTickInterval is the scheduler's timer resolution. One second comfortably
// catches minute-resolution cron and short interval schedules; tests shrink it.
const defaultTickInterval = time.Second

// NewSupervisor builds a supervisor over an assembled space. Call Start to bring
// the run loops up.
func NewSupervisor(sp *SwarmSpace) *Supervisor {
	return &Supervisor{
		sp:           sp,
		bus:          sp.Bus,
		store:        sp.Store,
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickInterval: defaultTickInterval,
		members:      make(map[string]*memberRun),
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
	go s.timerTick(ctx)
}

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
	return nil
}

// Unfreeze returns a member to service and pokes it to drain any mail that
// queued while it was frozen.
func (s *Supervisor) Unfreeze(name string) error {
	if _, ok := s.sp.Roster.membership(name); !ok {
		return fmt.Errorf("swarm: unfreeze: unknown member %q", name)
	}
	s.sp.Roster.setMembership(name, MembershipActive)
	if m := s.memberOf(name); m != nil {
		s.poke(m, wakeMessage)
	}
	return nil
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
