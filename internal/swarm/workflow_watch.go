package swarm

import (
	"fmt"
	"time"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// workflow_watch.go is the RP-22 workflow watchdog — the ledger-level sibling
// of the RP-14 run watchdog. RP-14 catches "a run that IS going is stuck";
// this catches "work that NOBODY is moving": a task parked in running/
// verifying past settings.TaskStaleThreshold, and a member whose oldest
// unread mail exceeds settings.MailboxStaleThreshold (which the normal wake
// chain should make impossible — so when it fires it means a frozen or
// suspended member was forgotten, or the RP-1 delivery machinery regressed).
//
// Like the budget and retention sweeps, the watchdog rides the supervisor's
// timer tick, throttled to workflowSweepInterval (the checks are two small
// SQL queries — light, but not every-second light). ALL of its state — the
// last sweep instant and both anti-spam mark maps — is touched only on the
// tick goroutine, the vacuumDay pattern: no locks, and the marks reset on
// restart, so a still-stale task re-reminds once after a service bounce
// (the same stance RP-14 takes for its per-run marks).

// defaultWorkflowSweepInterval throttles the ledger checks. Coarse on
// purpose: thresholds are tens of minutes to days, so a ten-minute detection
// granularity adds nothing to latency that matters; tests shrink it.
const defaultWorkflowSweepInterval = 10 * time.Minute

// staleTaskKey identifies one stay in one state: a task re-entering running
// (updated_at changes) is a fresh stay and earns a fresh reminder.
type staleTaskKey struct {
	status    store.Status
	updatedAt int64
}

// sweepWorkflow runs both ledger checks when the throttle allows. Driven by
// timerTick; tests call it directly (single-goroutine, like the tick).
func (s *Supervisor) sweepWorkflow(now time.Time) {
	if s.sp.settings.TaskStaleThreshold <= 0 && s.sp.settings.MailboxStaleThreshold <= 0 {
		return
	}
	if !s.lastWorkflowSweep.IsZero() && now.Sub(s.lastWorkflowSweep) < s.workflowSweepInterval {
		return
	}
	s.lastWorkflowSweep = now
	s.sweepStaleTasks(now)
	s.sweepStaleMailboxes(now)
}

// sweepStaleTasks reminds the leader (and the operator) about tasks parked in
// running/verifying past the threshold — at most once per task per stay in
// that state. suspended is exempt: that state IS deliberate parking.
func (s *Supervisor) sweepStaleTasks(now time.Time) {
	threshold := s.sp.settings.TaskStaleThreshold
	if threshold <= 0 {
		return
	}
	tasks, err := s.store.ListTasks(store.TaskFilter{
		Statuses: []store.Status{store.StatusRunning, store.StatusVerifying},
	})
	if err != nil {
		s.log.Warn("swarm workflow watch: list tasks", "err", err)
		return
	}

	// Rebuild the mark map from the currently-stale set: marks for tasks that
	// moved on (or completed) drop out, a carried mark suppresses re-sending,
	// and a changed (status, updated_at) is a new stay → alert again.
	marks := make(map[int64]staleTaskKey)
	for _, t := range tasks {
		age := now.Sub(time.UnixMilli(t.UpdatedAt))
		if age < threshold {
			continue
		}
		key := staleTaskKey{status: t.Status, updatedAt: t.UpdatedAt}
		marks[t.ID] = key
		if s.staleTaskNotified[t.ID] == key {
			continue // already reminded for this stay
		}
		s.sp.metrics.countTaskStale()
		s.notifyStaleTask(t, age)
	}
	s.staleTaskNotified = marks
}

// notifyStaleTask raises the one-per-stay reminder, with a suggested action
// so the leader's first turn after waking can act (the Sunday
// suggested_action philosophy).
func (s *Supervisor) notifyStaleTask(t store.Task, age time.Duration) {
	s.log.Warn("swarm workflow watch: stale task",
		"task", t.ID, "status", t.Status, "assignee", t.Assignee, "age", humanAge(age))

	subject := fmt.Sprintf("⏳ task #%d stale: %s for %s", t.ID, t.Status, humanAge(age))
	action := fmt.Sprintf(
		"Suggested action: message the assignee (%s) for a progress report, reassign the work, "+
			"or move the task to suspended if it is intentionally parked (suspended tasks are not reminded).",
		t.Assignee)
	if t.Status == store.StatusVerifying {
		action = "Suggested action: review the reported result and task_verify {approve: true} to complete it, " +
			"or {approve: false} with a note to send it back for rework."
	}
	body := fmt.Sprintf(
		"Task #%d %q (assignee: %s) has sat in %q for %s — past the task_stale_threshold of %s. "+
			"The state machine only guarantees legal moves; moving it is on the team. %s",
		t.ID, t.Title, t.Assignee, t.Status, humanAge(age), s.sp.settings.TaskStaleThreshold, action)
	s.notifyOps(t.Assignee, subject, body)
}

// sweepStaleMailboxes alerts when a roster member's oldest unread (unclaimed)
// message exceeds the threshold — once per backlog episode: the mark clears
// when the member's backlog drains, so a NEW backlog alerts again. Frozen
// members are deliberately NOT exempt — "frozen with mail piling up" is
// exactly what the operator needs to hear; the notice names the state.
func (s *Supervisor) sweepStaleMailboxes(now time.Time) {
	threshold := s.sp.settings.MailboxStaleThreshold
	if threshold <= 0 {
		return
	}
	oldest, err := s.store.OldestUnread()
	if err != nil {
		s.log.Warn("swarm workflow watch: oldest unread", "err", err)
		return
	}

	alerted := make(map[string]bool)
	for _, mv := range s.sp.Roster.Snapshot() {
		at, ok := oldest[mv.Name]
		if !ok {
			continue // no unread at all — any prior episode mark dies here
		}
		age := now.Sub(time.UnixMilli(at))
		if age < threshold {
			continue
		}
		alerted[mv.Name] = true
		if s.mailboxAlerted[mv.Name] {
			continue // this episode already alerted
		}
		s.sp.metrics.countMailboxStale()
		s.notifyStaleMailbox(mv, age)
	}
	s.mailboxAlerted = alerted
}

// notifyStaleMailbox raises the one-per-episode backlog alert.
func (s *Supervisor) notifyStaleMailbox(mv MemberView, age time.Duration) {
	s.log.Warn("swarm workflow watch: mailbox backlog",
		"member", mv.Name, "oldest", humanAge(age), "membership", mv.Membership, "run", mv.Run)

	state := ""
	action := "Suggested action: this should not happen under the normal wake chain — check the service log " +
		"for delivery errors; suspending and resuming the member forces a re-drain."
	switch {
	case mv.Membership == MembershipFrozen:
		state = " The member is FROZEN, so it will not drain mail until unfrozen."
		action = "Suggested action: unfreeze the member so it drains its backlog, or reassign its pending work."
	case mv.Run == RunSuspended:
		state = " The member is SUSPENDED."
		action = "Suggested action: resume the member so it drains its backlog, or reassign its pending work."
	}

	subject := fmt.Sprintf("📬 mailbox backlog: %s (oldest unread %s)", mv.Name, humanAge(age))
	body := fmt.Sprintf(
		"Member %q has unread mail aging %s — past the mailbox_stale_threshold of %s.%s %s",
		mv.Name, humanAge(age), s.sp.settings.MailboxStaleThreshold, state, action)
	s.notifyOps(mv.Name, subject, body)
}

// humanAge renders a duration the way an operator reads a board: whole days
// past 48h, whole hours past one, whole minutes past one, else seconds.
func humanAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
