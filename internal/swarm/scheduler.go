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
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/ui"
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

	// RP-14 stall-watchdog bookkeeping, guarded by mu: when the in-flight run
	// claimed the slot (zero = no run in flight), and whether this run already
	// raised its stall alert / had its hard-timeout kill notice sent — one of
	// each per run, reset when the next run claims the slot.
	runStartedAt  time.Time
	stallNotified bool
	stallKilled   bool
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

	// Seed the roster's usage snapshot (RP-13) so a resumed member shows its
	// cumulative spend before its first run. Safe: the loop hasn't started, so
	// no run is in flight to race the session read.
	if ctl, ok := s.sp.Roster.Controller(name); ok {
		s.sp.Roster.setUsage(name, ctl.Usage(), ctl.LastTurnInputTokens(), s.sp.dailyFor(name))
	}

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
	s.sp.metrics.countWake(name, reason) // RP-17: one tally per served wake

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
	m.runStartedAt = time.Now()
	m.stallNotified, m.stallKilled = false, false
	s.sp.Roster.setRun(name, RunBusy)
	m.mu.Unlock()

	// Pre-run usage snapshot (RP-13): read on this goroutine, where the member's
	// session is quiescent, so the post-run delta is race-free.
	ctl, hasCtl := s.sp.Roster.Controller(name)
	var preUsage llm.Usage
	if hasCtl {
		preUsage = ctl.Usage()
	}

	s.log.Debug("swarm run start", "member", name)
	_, err := s.safeRun(runCtx, name, prompt)
	cancel()

	m.mu.Lock()
	m.cancelRun = nil
	started := m.runStartedAt
	m.runStartedAt = time.Time{}
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

	// Meter the run regardless of clean — tokens were burned either way (RP-13).
	if hasCtl {
		s.meterRun(name, preUsage, ctl)
	}
	s.sp.metrics.countRun(name, time.Since(started), clean) // RP-17
	return clean
}

// meterRun folds one finished run's token delta into the member's daily
// counter, refreshes the roster's usage snapshot, and trips the budget breaker
// when the member crossed its daily cap (RP-13). Runs on the member's loop
// goroutine right after the run — the only place the session is safely
// readable while the loop owns the member.
func (s *Supervisor) meterRun(name string, pre llm.Usage, ctl ui.Controller) {
	post := ctl.Usage()
	delta := (post.InputTokens + post.OutputTokens) - (pre.InputTokens + pre.OutputTokens)
	total := s.sp.addDailyUsage(name, delta, localDay(time.Now()))
	s.sp.Roster.setUsage(name, post, ctl.LastTurnInputTokens(), total)

	budget := s.sp.BudgetFor(name)
	if budget <= 0 || total < budget {
		return
	}
	// Fresh mark only: a member the operator unfroze while still over budget
	// re-trips exactly once after its next run (Unfreeze clears the mark).
	if !s.sp.markBudgetFrozen(name) {
		return
	}
	s.tripBudget(name, total, budget)
}

// tripBudget freezes an over-budget member and notifies the operator and the
// leader (durable mail — the leader wakes on it; the operator reads it in the
// web). Freeze also persists the runtime snapshot, meter included.
func (s *Supervisor) tripBudget(name string, total, budget int) {
	if err := s.Freeze(name); err != nil {
		s.log.Warn("swarm: budget trip could not freeze member", "member", name, "err", err)
		return
	}
	s.log.Warn("swarm: daily token budget tripped — member frozen",
		"member", name, "spent", total, "budget", budget)

	subject := fmt.Sprintf("⚠️ budget breaker: %s frozen", name)
	body := fmt.Sprintf(
		"Member %q spent %d tokens today, crossing its daily budget of %d, and has been FROZEN by the budget breaker. "+
			"Its mailbox keeps queuing; it runs nothing until unfrozen. It auto-unfreezes when the local day rolls over "+
			"(unless settings.budget_stay_frozen is set). An operator may unfreeze it earlier via the web — if it is still "+
			"over budget it will re-freeze after its next run, so raise settings.daily_budget_tokens (or the member's "+
			"budget_tokens) to give it real headroom.",
		name, total, budget)
	s.notifyOps(name, subject, body)
}

// notifyOps sends one durable notice to the operator ("user" — read in the web)
// and, when the subject member is not the leader itself, to the leader (waking
// it so the team can react). Shared by the budget breaker and the stall
// watchdog.
func (s *Supervisor) notifyOps(about, subject, body string) {
	if leader := s.sp.Roster.leaderName(); leader != "" && leader != about {
		_, _ = s.sp.Bus.Send(store.Message{Sender: "system", Recipient: leader, Subject: subject, Body: body})
	}
	_, _ = s.sp.Bus.Send(store.Message{Sender: "system", Recipient: "user", Subject: subject, Body: body})
}

// sweepStalls is the RP-14 watchdog: a member whose in-flight run exceeded
// settings.StallThreshold raises ONE stall alert per run (durable mail via
// notifyOps), and — when settings.StallHardTimeout is set — a run past that
// line is cancelled. The cancel is safe by construction: a non-clean runOnce
// exit unclaims the run's mail, so the work retries on the member's next wake
// (and alerts again if it hangs again). Members blocked on a HUMAN — waiting
// approval or input, or paused at the iteration limit — are exempt: that wait
// is the operator's, not a hang. Driven by the timer tick beside
// sweepBudgetDay; zero new goroutines.
func (s *Supervisor) sweepStalls(now time.Time) {
	threshold := s.sp.settings.StallThreshold
	if threshold <= 0 {
		return
	}
	hard := s.sp.settings.StallHardTimeout

	phases := make(map[string]RunPhase)
	for _, mv := range s.sp.Roster.Snapshot() {
		phases[mv.Name] = mv.Phase
	}

	s.mu.Lock()
	type runRef struct {
		name string
		m    *memberRun
	}
	refs := make([]runRef, 0, len(s.members))
	for name, m := range s.members {
		refs = append(refs, runRef{name, m})
	}
	s.mu.Unlock()

	for _, r := range refs {
		switch phases[r.name] {
		case PhaseWaitingApproval, PhaseWaitingInput, PhasePaused:
			continue // waiting on a human — long is legitimate, not a hang
		}

		r.m.mu.Lock()
		started := r.m.runStartedAt
		inFlight := !started.IsZero()
		elapsed := now.Sub(started)
		alert := inFlight && elapsed >= threshold && !r.m.stallNotified
		if alert {
			r.m.stallNotified = true
		}
		kill := inFlight && hard > 0 && elapsed >= hard && !r.m.stallKilled
		var cancel context.CancelFunc
		if kill {
			r.m.stallKilled = true
			cancel = r.m.cancelRun
		}
		r.m.mu.Unlock()

		if alert {
			s.notifyStall(r.name, elapsed, string(phases[r.name]))
		}
		if kill && cancel != nil {
			s.log.Warn("swarm: stall hard-timeout — cancelling run", "member", r.name, "elapsed", elapsed.Round(time.Second))
			s.notifyStallKilled(r.name, elapsed)
			cancel()
		}
	}
}

// notifyStall raises the one-per-run stall alert.
func (s *Supervisor) notifyStall(name string, elapsed time.Duration, phase string) {
	s.log.Warn("swarm: member run stalled", "member", name, "elapsed", elapsed.Round(time.Second), "phase", phase)
	subject := fmt.Sprintf("⏳ stall: %s busy for %s", name, elapsed.Round(time.Second))
	body := fmt.Sprintf(
		"Member %q has been busy for %s (phase: %s) — past the stall threshold of %s. "+
			"This may be a hung LLM call or tool, or a legitimately long task. You can suspend it from the web "+
			"(its claimed mail is unclaimed and retries), raise settings.stall_threshold if long runs are expected "+
			"here, or set settings.stall_hard_timeout to auto-cancel runs like this one.",
		name, elapsed.Round(time.Second), phase, s.sp.settings.StallThreshold)
	s.notifyOps(name, subject, body)
}

// notifyStallKilled announces a hard-timeout cancel.
func (s *Supervisor) notifyStallKilled(name string, elapsed time.Duration) {
	subject := fmt.Sprintf("⏱️ stall: %s run cancelled after %s", name, elapsed.Round(time.Second))
	body := fmt.Sprintf(
		"Member %q exceeded settings.stall_hard_timeout (%s); its run was cancelled. The mail it was working is "+
			"unclaimed and retries on its next wake — if the same work hangs again you will be alerted again. "+
			"Raise the timeout if this was a legitimate long task.",
		name, s.sp.settings.StallHardTimeout)
	s.notifyOps(name, subject, body)
}

// sweepBudgetDay advances the meter day and unfreezes members whose breaker
// mark is from an earlier day (unless the space pins them with
// budget_stay_frozen). Driven by the timer tick — a frozen member never runs,
// so its release cannot depend on a run happening.
func (s *Supervisor) sweepBudgetDay(now time.Time) {
	for _, name := range s.sp.sweepMeter(localDay(now), s.sp.settings.BudgetStayFrozen) {
		if err := s.Unfreeze(name); err != nil {
			s.log.Warn("swarm: budget rollover unfreeze failed", "member", name, "err", err)
			continue
		}
		s.log.Info("swarm: budget day rolled over — member unfrozen", "member", name)
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
			s.sweepBudgetDay(now)
			s.sweepStalls(now)
			s.sweepRetention(now)
			s.sweepWorkflow(now)
			s.fireDue(now)
		}
	}
}

// sweepRetention runs the RP-16 ledger vacuum once per local day — and once
// right after startup, catching up a service that was down at midnight. The
// pass runs in its own goroutine: it holds the store's write lock and may
// VACUUM, which must not delay wakes on the tick goroutine. vacuumBusy
// collapses overlap if a slow pass outlives the day check; vacuumDay is only
// touched here (single tick goroutine), so it needs no lock.
func (s *Supervisor) sweepRetention(now time.Time) {
	days := s.sp.settings.RetentionDays // set once at construction, never mutated
	if days <= 0 {
		return // retention off — the pre-RP-16 "never deletes history" behavior
	}
	day := now.Local().Format("2006-01-02")
	if day == s.vacuumDay || !s.vacuumBusy.CompareAndSwap(false, true) {
		return
	}
	s.vacuumDay = day
	cutoff := now.AddDate(0, 0, -days)
	s.wg.Add(1) // tracked so teardown drains the pass BEFORE the store closes
	go func() {
		defer s.wg.Done()
		defer s.vacuumBusy.Store(false)
		stats, err := s.store.Vacuum(cutoff, false)
		if err != nil {
			s.log.Warn("swarm retention vacuum failed", "err", err)
			return
		}
		if stats.Messages+stats.Tasks > 0 {
			s.log.Info("swarm retention vacuum", "messages", stats.Messages, "tasks", stats.Tasks, "files", stats.Files)
		}
	}()
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
