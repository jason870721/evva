package swarm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/skill"
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
	// loopCancel stops just THIS member's run loop (a child of the supervisor
	// ctx), so RemoveMember (RP-8) can retire one member without tearing down the
	// space. Suspend cancels the current run; this cancels the loop itself.
	loopCancel context.CancelFunc

	schedule *agentdef.Schedule // nil when the member declared no schedule
	nextDue  time.Time

	// pendingSkills holds a rebuilt skill catalog requested mid-run (RP-10-4); the
	// run loop installs it at the next boundary (serve) so a busy member's prompt is
	// never swapped during an in-flight run. nil when no reload is pending.
	pendingSkills *skill.Registry
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
	if sch, ok := s.sp.ScheduleFor(name); ok {
		if due, err := sch.Next(time.Now()); err == nil {
			m.schedule = &sch
			m.nextDue = due
		} else {
			s.log.Warn("swarm: invalid schedule, timer disabled", "agent", name, "err", err)
		}
	}
	// Each loop gets its own cancel (child of the supervisor ctx) so RemoveMember
	// can stop one member without affecting the rest.
	loopCtx, loopCancel := context.WithCancel(ctx)
	m.loopCancel = loopCancel
	s.members[name] = m
	s.mu.Unlock()

	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.runLoop(loopCtx, name, m) }()
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

// serve handles one wake on a member (active members only). A timer wake runs
// the standing-duty prompt once; then, for both wake kinds, it drains the
// member's mailbox to EMPTY — claiming each unread batch from the store and
// running it, until the store reports nothing unread. Draining against the DB
// rather than trusting a chan hint to survive is what makes a lost or
// over-delivered hint harmless: the store is the single source of truth for
// liveness as well as content (§6.2). Roster bookkeeping + the message lifecycle
// (claim → settle/unclaim) live in runOnce.
func (s *Supervisor) serve(ctx context.Context, name string, m *memberRun, reason wakeReason) {
	// Install any skill reload requested while this member was busy (RP-10-4). Done
	// first — on the run-loop goroutine, before any runOnce in this serve, and even
	// for a frozen member — so the prompt swap lands at a clean boundary and a reload
	// is never stranded.
	m.mu.Lock()
	pending := m.pendingSkills
	m.pendingSkills = nil
	m.mu.Unlock()
	if pending != nil {
		s.applyMemberSkills(name, pending)
	}

	if !s.isActive(name) {
		return // frozen: never scheduled
	}

	if reason == wakeTimer {
		// A standing-duty tick. runOnce still settles/unclaims any mail drain B
		// folded during it, so even a timer run can't strand a mid-run message.
		// schedule/Prompt are s.mu-guarded; grab the custom prompt under the lock.
		s.mu.Lock()
		var dutyPrompt string
		if m.schedule != nil {
			dutyPrompt = m.schedule.Prompt
		}
		s.mu.Unlock()
		// now = wake-execution time (RP-7 §3.2 divergence: the duty runs ~sub-tick
		// after fireDue poked, so "the time the duty actually runs" is the right
		// value to hand a long-running agent whose static prompt holds no date — RP-5).
		s.log.Debug("swarm serve: timer duty", "member", name)
		if !s.runOnce(ctx, name, m, scheduledWakePrompt(time.Now(), dutyPrompt)) {
			return // suspended/errored — stop here
		}
	}

	// Level-triggered mail drain: claim the unread batch, run it, repeat until
	// the store has no unread for this member. Mail that lands during a run is
	// caught on the next pass here (or folded mid-run by drain B).
	for s.isActive(name) {
		batch, err := s.store.ClaimUnread(name)
		if err != nil {
			s.log.Warn("swarm serve: claim unread", "member", name, "err", err)
			return
		}
		if len(batch) == 0 {
			return // nothing unread — idle burns no tokens
		}
		s.log.Debug("swarm serve: member has mail", "member", name, "batch", len(batch))
		if !s.runOnce(ctx, name, m, composeMailPrompt(time.Now(), batch)) {
			return // suspended/errored — runOnce already unclaimed the batch for retry
		}
	}
}

// runOnce claims the run slot (unless a Suspend beat it), runs one prompt under a
// cancellable context, and settles the message lifecycle exactly once: on a
// clean finish it stamps read_at on everything claimed during the run — the
// start batch plus any drain-B folds — via SettleClaimed; on any non-clean exit
// (suspend / cancel / error / panic) it resets those claims to unread via
// UnclaimFor so the mail retries (§6.2 — the DB is truth). Returns whether the
// run finished cleanly. This single settle point is why drain A and drain B can
// never disagree about when a message becomes read.
func (s *Supervisor) runOnce(ctx context.Context, name string, m *memberRun, prompt string) (clean bool) {
	// Claim the run unless a Suspend beat us. The roster status flips under m.mu
	// so a racing Suspend's RunSuspended can't be overwritten by our RunBusy.
	m.mu.Lock()
	if m.suspended {
		m.mu.Unlock()
		// A batch claimed just before this was suspended must not stay claimed.
		_ = s.store.UnclaimFor(name)
		return false
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancelRun = cancel
	s.sp.Roster.setRun(name, RunBusy)
	m.mu.Unlock()

	s.log.Debug("swarm run start", "member", name)
	_, err := s.safeRun(runCtx, name, prompt)
	cancel()

	m.mu.Lock()
	m.cancelRun = nil
	suspended := m.suspended
	if !suspended {
		s.sp.Roster.setRun(name, RunIdle)
	}
	m.mu.Unlock()

	clean = !suspended && err == nil
	if err != nil {
		// A silently aborted run is the hardest swarm failure to debug — surface it.
		s.log.Warn("swarm run aborted", "member", name, "suspended", suspended, "err", err)
	} else {
		s.log.Debug("swarm run end", "member", name, "suspended", suspended)
	}

	if clean {
		if err := s.store.SettleClaimed(name); err != nil {
			s.log.Warn("swarm serve: settle claimed", "member", name, "err", err)
		}
	} else {
		_ = s.store.UnclaimFor(name)
	}
	return clean
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

// composeMailPrompt renders a claimed batch of unread mail (store.ClaimUnread,
// already oldest-first) as the synthetic run-start prompt. The rows come from the
// store — the single source of truth — so this naturally absorbs dropped or
// over-delivered chan hints. Like the timer wake, it opens with a currenttime
// reminder (offset-stamped) so a mail-woken member knows what time it is, and
// each message carries its sent stamp so stale mail is visible as stale.
func composeMailPrompt(now time.Time, batch []store.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<system-reminder>currenttime: %s</system-reminder>\n", common.Stamp(now))
	b.WriteString("You have unread messages from your teammates. Read each one and take whatever action it asks for; use send_message to reply or report back.\n")
	for _, msg := range batch {
		b.WriteString("\n--- Message from ")
		b.WriteString(msg.Sender)
		if msg.Subject != "" {
			b.WriteString(" (re: ")
			b.WriteString(msg.Subject)
			b.WriteString(")")
		}
		if msg.CreatedAt > 0 {
			b.WriteString(" [sent ")
			b.WriteString(common.Stamp(time.UnixMilli(msg.CreatedAt)))
			b.WriteString("]")
		}
		b.WriteString(" ---\n")
		b.WriteString(msg.Body)
		b.WriteString("\n")
	}
	return b.String()
}

const scheduledDutyPrompt = "[Scheduled duty] Your recurring schedule fired. Carry out your standing responsibilities now: check the state you are responsible for and take any action it requires. If everything is in order, report that briefly and stand down — do not invent work."

// scheduledWakePrompt builds the run-start prompt for a timer wake. Wake
// prompts are where wall-clock time enters the conversation (the static system
// prompt deliberately carries no date — RP-5/RP-7): the agent learns "what time
// is it" from the wake itself, so the stamp carries an explicit UTC offset — a
// zone-less time reads as UTC to the model and is misread by the local offset.
// A member's custom schedule Prompt becomes the body; an empty one falls back
// to the generic standing-duty sentence. The whole thing is wrapped in a
// <system-reminder> so the model reads it as harness context, not a teammate's
// request.
func scheduledWakePrompt(now time.Time, prompt string) string {
	body := strings.TrimSpace(prompt)
	if body == "" {
		body = scheduledDutyPrompt
	}
	return fmt.Sprintf("<system-reminder>currenttime: %s, %s</system-reminder>", common.Stamp(now), body)
}

// poke signals a member's non-message wake (timer or resume). Non-blocking: if a
// poke is already pending, the loop is guaranteed to run, so dropping this one
// loses nothing.
func (s *Supervisor) poke(m *memberRun, r wakeReason) {
	select {
	case m.wake <- r:
	default:
	}
}

// rescanTick is the safety re-scan goroutine (one per space): it periodically
// pokes any idle, active member the store still shows unread mail for. The
// level-triggered drain in serve already covers mail that arrives during or
// after a run; this covers the one case it can't — a wake hint dropped entirely
// (full buffer, or a delivery that raced registration), so the member was never
// woken at all. It turns a permanent stall into a ≤rescanInterval delay.
func (s *Supervisor) rescanTick(ctx context.Context) {
	t := time.NewTicker(s.rescanInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.rescanUnread()
		}
	}
}

// rescanUnread pokes every idle, active member that has unread mail in the store.
// Only idle members are poked: a busy one is already draining (its unread are
// claimed mid-run), and a suspended/frozen one must not run. A spurious poke is
// harmless — serve just claims an empty batch and returns.
func (s *Supervisor) rescanUnread() {
	for _, mv := range s.sp.Roster.Snapshot() {
		if mv.Membership != MembershipActive || mv.Run != RunIdle {
			continue
		}
		ids, err := s.store.UnreadFor(mv.Name)
		if err != nil || len(ids) == 0 {
			continue
		}
		if m := s.memberOf(mv.Name); m != nil {
			s.log.Debug("swarm rescan: waking idle member with unread mail", "member", mv.Name, "unread", len(ids))
			s.poke(m, wakeMessage)
		}
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
		if !s.isActive(d.name) {
			continue // frozen — never scheduled
		}
		// Skip, don't queue: a scheduled wake is a recurring patrol, not a job
		// that must catch up. If the member is mid-run, drop this tick (nextDue
		// already advanced above) rather than buffering a poke that would fire a
		// late duty when the current run ends (RP-7 §3.6). rescanUnread uses the
		// same idle gate.
		if rs, ok := s.sp.Roster.runOf(d.name); ok && rs != RunIdle {
			s.log.Debug("swarm timer: member busy, skipping this tick", "member", d.name, "run", rs)
			continue
		}
		s.poke(d.m, wakeTimer)
	}
}
