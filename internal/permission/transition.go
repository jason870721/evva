package permission

import "sync"

// PlanModeState collects the per-session state that plan mode needs in
// order to drive the per-turn <system-reminder> attachment system and
// restore the prior permission stance on exit.
//
// The struct exists so that every callsite that flips the permission
// mode — the TUI's Shift+Tab handler, the EnterPlanMode / ExitPlanMode
// tools, future SDK control messages — funnels through a single side-
// effect hub (Transition). Without it, side effects (stashing prePlanMode,
// queuing the exit reminder, resetting reminder counters) end up scattered
// at every callsite and drift over time. Ports the design of
// ref/src/utils/permissions/permissionSetup.ts:transitionPermissionMode.
//
// All fields are guarded by an internal mutex; safe for concurrent access
// from the agent loop and the TUI.
type PlanModeState struct {
	mu sync.Mutex

	// prePlanMode is the mode that was active immediately before plan mode
	// became active. ExitPlanMode reads this on user approval to restore
	// the prior stance (default → plan → default; accept_edits → plan →
	// accept_edits). Empty until the first plan-mode entry; ExitPlanMode
	// falls back to ModeDefault when empty.
	prePlanMode Mode

	// hasExited records whether plan mode was ever exited this session.
	// Drives the one-shot "you re-entered plan mode" reminder when the
	// model later flips back into plan mode and the plan file still
	// exists. Reset to false when the reminder fires.
	hasExited bool

	// pendingExitReminder is set by Transition() on every plan→non-plan
	// transition; the attachment computer reads and clears it on the next
	// user-prompt turn so the model sees exactly one "you have exited
	// plan mode" reminder per exit event.
	pendingExitReminder bool

	// attachmentsSinceExit counts plan-mode reminders that have been
	// emitted since the last plan_mode_exit attachment. Drives the
	// full-vs-sparse cycle (full every Nth reminder, sparse otherwise).
	// Resets to 0 on exit so re-entering plan mode starts with a fresh
	// "full" reminder.
	attachmentsSinceExit int

	// turnsSinceLastAttachment counts user-prompt turns since the last
	// plan-mode reminder injection. Used by the attachment computer to
	// throttle reminders (default: one reminder every 4 user turns once
	// the first turn has elapsed). Resets to 0 on every emission.
	turnsSinceLastAttachment int

	// planName is the user-provided plan name set by enter_plan_mode.
	// When empty, PlanFilePath falls back to "current.md". This field
	// is intentionally NOT cleared on plan→non-plan transitions so the
	// plan file path remains stable for the remainder of the session.
	planName string
}

// NewPlanModeState constructs an empty plan-mode state holder. Cheap; the
// agent constructor calls this exactly once per agent.
func NewPlanModeState() *PlanModeState {
	return &PlanModeState{}
}

// Transition runs the side effects when permission mode changes. Every
// entry path (Shift+Tab, EnterPlanMode tool, ExitPlanMode tool, future
// SDK control messages) MUST funnel through this function so reminder
// counters and pre-plan-mode stashing stay consistent across paths.
//
// Idempotent on no-op transitions (from == to).
//
//   - non-plan → plan: stashes the prior mode (so ExitPlanMode can restore
//     it), resets the attachment counters so the next user-prompt turn
//     fires a full reminder.
//   - plan → non-plan: clears prePlanMode, sets pendingExitReminder so the
//     next user-prompt turn carries a single "you have exited plan mode"
//     attachment, and marks hasExited so any later re-entry includes a
//     one-shot re-entry reminder.
//   - any other transition: no plan-mode side effect.
func (s *PlanModeState) Transition(from, to Mode) {
	if s == nil || from == to {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if to == ModePlan && from != ModePlan {
		s.prePlanMode = from
		// First turn in plan mode should see the full workflow reminder;
		// reset counters so the attachment computer takes the
		// "first-time" branch.
		s.attachmentsSinceExit = 0
		s.turnsSinceLastAttachment = 0
		return
	}
	if from == ModePlan && to != ModePlan {
		s.prePlanMode = ""
		s.hasExited = true
		s.pendingExitReminder = true
		s.attachmentsSinceExit = 0
		s.turnsSinceLastAttachment = 0
		return
	}
}

// PrePlanMode returns the mode that was active immediately before plan
// mode became active. Empty until the first plan-mode entry.
func (s *PlanModeState) PrePlanMode() Mode {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prePlanMode
}

// SetPrePlanMode lets callers force the pre-plan-mode value. Used by
// EnterPlanMode prior to the unified Transition refactor (kept on the
// API for backwards compatibility with the PlanModeController
// interface). New code should rely on Transition() to set this.
func (s *PlanModeState) SetPrePlanMode(m Mode) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prePlanMode = m
}

// HasExited reports whether the session has exited plan mode at least
// once. The attachment computer reads + clears this when emitting the
// one-shot re-entry reminder.
func (s *PlanModeState) HasExited() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasExited
}

// ConsumeReentry returns whether a re-entry reminder is owed and clears
// the flag. The attachment computer calls this exactly once on the first
// user-prompt turn after a re-entry into plan mode.
func (s *PlanModeState) ConsumeReentry() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasExited {
		return false
	}
	s.hasExited = false
	return true
}

// ConsumePendingExitReminder returns whether an "exited plan mode"
// reminder is owed and clears the flag. Called once per user-prompt
// turn by the attachment computer.
func (s *PlanModeState) ConsumePendingExitReminder() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pendingExitReminder {
		return false
	}
	s.pendingExitReminder = false
	return true
}

// AttachmentsSinceExit returns the count of plan-mode reminders that
// have been emitted since the last plan-mode exit. Used to choose
// full-vs-sparse on each emission.
func (s *PlanModeState) AttachmentsSinceExit() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attachmentsSinceExit
}

// TurnsSinceLastAttachment returns the count of user-prompt turns since
// the last plan-mode reminder injection. Used by the attachment computer
// to throttle reminders.
func (s *PlanModeState) TurnsSinceLastAttachment() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnsSinceLastAttachment
}

// RecordAttachmentEmitted is called by the attachment computer after it
// emits a plan-mode reminder this turn. Increments the cycle counter
// and resets the throttle counter.
func (s *PlanModeState) RecordAttachmentEmitted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachmentsSinceExit++
	s.turnsSinceLastAttachment = 0
}

// RecordTurnWithoutAttachment is called by the attachment computer on
// every user-prompt turn in plan mode where it chose NOT to emit a
// reminder. Bumps the throttle counter so the next eligible turn fires.
func (s *PlanModeState) RecordTurnWithoutAttachment() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnsSinceLastAttachment++
}

// PlanName returns the user-provided plan name set by enter_plan_mode.
// Empty string means "current" — PlanFilePath resolves the default.
func (s *PlanModeState) PlanName() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planName
}

// SetPlanName stores the user-provided plan name. Called by enter_plan_mode
// when the model supplies a plan_name in its input.
func (s *PlanModeState) SetPlanName(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planName = name
}
