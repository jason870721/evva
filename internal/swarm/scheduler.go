package swarm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

// wakeReason identifies why a member's run loop fired. The two mechanical wake
// channels — the bus mailbox and the timer/resume poke — realise the design's
// three sources: message and task both arrive as mailbox mail (task_assign
// sends a message, §7.1), timer arrives as a poke (§5.5).
type wakeReason int

const (
	wakeMessage wakeReason = iota // mailbox mail, or a resume re-check (drain A)
	wakeTimer                     // a declared profile schedule came due
)

// memberRun is the supervisor's per-agent run-loop control.
//
// Locking: suspended + cancelRun are guarded by mu. schedule + nextDue are
// guarded by the owning Supervisor.mu (only the timer tick reads/writes them).
// wake is a channel and needs no lock.
type memberRun struct {
	wake chan wakeReason // buffered(1): timer ticks + resume pokes

	mu        sync.Mutex
	suspended bool
	cancelRun context.CancelFunc

	schedule *agentdef.Schedule // nil when the member declared no schedule
	nextDue  time.Time
}

// startMemberLoop registers a member's run control (idempotent) and launches its
// recover-guarded run loop.
func (s *Supervisor) startMemberLoop(ctx context.Context, name string) {
	s.mu.Lock()
	if _, ok := s.members[name]; ok {
		s.mu.Unlock()
		return
	}
	m := &memberRun{wake: make(chan wakeReason, 1)}
	if sch, ok := s.sp.scheduleFor(name); ok {
		if due, err := sch.Next(time.Now()); err == nil {
			m.schedule = &sch
			m.nextDue = due
		} else {
			s.log.Warn("swarm: invalid schedule, timer disabled", "agent", name, "err", err)
		}
	}
	s.members[name] = m
	s.mu.Unlock()

	go s.runLoop(ctx, name, m)
}

// runLoop is one member's event loop: it blocks (idle, zero tokens) until a wake
// source fires, serves it, and repeats until ctx is cancelled. The mailbox carry
// only a "you have mail" hint — the actual messages come from the store.
func (s *Supervisor) runLoop(ctx context.Context, name string, m *memberRun) {
	inbox := s.bus.Inbox(name)
	for {
		select {
		case <-ctx.Done():
			return
		case <-inbox:
			s.serve(ctx, name, m, wakeMessage)
		case r := <-m.wake:
			s.serve(ctx, name, m, r)
		}
	}
}

// serve runs one wake on a member — but only if it is active, not suspended, and
// actually has work. It owns the idle↔busy↔suspended roster bookkeeping and the
// drain-A mark-read.
func (s *Supervisor) serve(ctx context.Context, name string, m *memberRun, reason wakeReason) {
	if !s.isActive(name) {
		return // frozen: never scheduled
	}

	prompt, msgIDs := s.composePrompt(name, reason)
	if prompt == "" {
		return // nothing to do — idle burns no tokens
	}

	// Claim the run unless a Suspend beat us to it. The roster status flips under
	// m.mu so a racing Suspend's RunSuspended can't be overwritten by our
	// RunBusy.
	m.mu.Lock()
	if m.suspended {
		m.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancelRun = cancel
	s.sp.Roster.setRun(name, RunBusy)
	m.mu.Unlock()

	_, err := s.safeRun(runCtx, name, prompt)
	cancel()

	m.mu.Lock()
	m.cancelRun = nil
	suspended := m.suspended
	if !suspended {
		s.sp.Roster.setRun(name, RunIdle)
	}
	m.mu.Unlock()

	// Drain A: a message is consumed (read_at stamped) only after a clean,
	// finished run. A suspended / panicked / errored run leaves it unread so it
	// retries on Resume or restart (§6.2 — the DB is truth).
	if !suspended && err == nil {
		for _, id := range msgIDs {
			_ = s.store.MarkRead(id)
		}
	}
}

// safeRun calls Controller.Run inside a recover guard so one agent's panic
// degrades only that run — never the loop goroutine or the process (invariant
// #3).
func (s *Supervisor) safeRun(ctx context.Context, name, prompt string) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("swarm: agent %q run panicked: %v", name, r)
			s.log.Error("swarm: recovered agent run panic", "agent", name, "panic", r)
		}
	}()
	ctl, ok := s.sp.Roster.Controller(name)
	if !ok {
		return "", fmt.Errorf("swarm: no controller for %q", name)
	}
	return ctl.Run(ctx, prompt)
}

// composePrompt builds the synthetic prompt for a wake. A message/resume wake
// gathers the member's unread mail from the store (the DB is truth — this
// naturally absorbs dropped chan hints and stragglers) and returns the ids to
// mark read on success. A timer wake is a standing-duty prompt with nothing to
// mark.
func (s *Supervisor) composePrompt(name string, reason wakeReason) (string, []string) {
	if reason == wakeTimer {
		return scheduledDutyPrompt, nil
	}
	ids, err := s.store.UnreadFor(name)
	if err != nil || len(ids) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("You have unread messages from your teammates. Read each one and take whatever action it asks for; use send_message to reply or report back.\n")
	for _, id := range ids {
		msg, err := s.store.GetMessage(id)
		if err != nil {
			continue
		}
		b.WriteString("\n--- Message from ")
		b.WriteString(msg.Sender)
		if msg.Subject != "" {
			b.WriteString(" (re: ")
			b.WriteString(msg.Subject)
			b.WriteString(")")
		}
		b.WriteString(" ---\n")
		b.WriteString(msg.Body)
		b.WriteString("\n")
	}
	return b.String(), ids
}

const scheduledDutyPrompt = "[Scheduled duty] Your recurring schedule fired. Carry out your standing responsibilities now: check the state you are responsible for and take any action it requires. If everything is in order, report that briefly and stand down — do not invent work."

// poke signals a member's non-message wake (timer or resume). Non-blocking: if a
// poke is already pending, the loop is guaranteed to run, so dropping this one
// loses nothing.
func (s *Supervisor) poke(m *memberRun, r wakeReason) {
	select {
	case m.wake <- r:
	default:
	}
}

// timerTick is the one tick goroutine per space; it fires due schedules.
func (s *Supervisor) timerTick(ctx context.Context) {
	t := time.NewTicker(s.tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.fireDue(now)
		}
	}
}

// fireDue pokes every scheduled, active member whose next activation has passed,
// advancing its nextDue. Pokes happen outside the lock.
func (s *Supervisor) fireDue(now time.Time) {
	type due struct {
		name string
		m    *memberRun
	}
	var fire []due

	s.mu.Lock()
	for name, m := range s.members {
		if m.schedule == nil || now.Before(m.nextDue) {
			continue
		}
		if nxt, err := m.schedule.Next(now); err == nil {
			m.nextDue = nxt
		}
		fire = append(fire, due{name, m})
	}
	s.mu.Unlock()

	for _, d := range fire {
		if s.isActive(d.name) {
			s.poke(d.m, wakeTimer)
		}
	}
}
