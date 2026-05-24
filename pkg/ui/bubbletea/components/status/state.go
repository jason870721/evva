// Package status owns the v2 TUI's bottom HUD: the run-state pill,
// model + token cells, context-utilization meter, and the
// contextual hint line that sits above it.
//
// The package exposes three pieces:
//
//   - *State — the run-state machine that maps incoming agent events
//     onto a coarse RunState enum. Sticky for terminal states
//     (Error / IterLimit); cleared on submit.
//
//   - *StatusBar — the rendering wrapper. Held by App; composes the
//     HUD using the live State, Usage, context tokens, and theme.
//
//   - HintProvider interface + default hint table. The App walks the
//     focus stack to find the topmost provider; absent that, falls
//     back to a RunState-keyed default. Lets overlays (M7+) hint
//     without coupling to the status bar.
package status

import (
	"github.com/johnny1110/evva/pkg/event"
)

// RunState is the agent's high-level lifecycle from the UI's
// perspective. Drives the status-bar pill (label + color + spinner)
// and the contextual hint shown above the input.
type RunState int

const (
	StateIdle       RunState = iota
	StateRunning             // generic "agent loop is alive between sub-phases"
	StateThinking            // model is generating reasoning tokens
	StateTexting             // model is generating response content tokens
	StateExecuting           // a tool call is in flight
	StateDraining            // pulling async subagent results back
	StateCompacting          // micro/full session compaction running
	StateIterLimit           // paused at the iter cap; Enter resumes
	StateError               // terminal error from the last Run
)

// String returns the uppercase label used in the status pill. The
// idle case reads "READY" rather than "IDLE" because the user hasn't
// done anything wrong — they're invited to type.
func (s RunState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateThinking:
		return "thinking"
	case StateTexting:
		return "texting"
	case StateExecuting:
		return "executing"
	case StateDraining:
		return "draining"
	case StateCompacting:
		return "compacting"
	case StateIterLimit:
		return "paused"
	case StateError:
		return "error"
	default:
		return "ready"
	}
}

// IsActive reports whether the state represents work-in-flight,
// i.e. the status pill should animate with the spinner rather than
// show a static glyph.
func (s RunState) IsActive() bool {
	switch s {
	case StateRunning, StateThinking, StateTexting, StateExecuting,
		StateDraining, StateCompacting:
		return true
	}
	return false
}

// IsTerminal reports whether the state is sticky — won't be
// overwritten by a stray mid-run event. Used by Apply to guard
// transitions out of Error / IterLimit.
func (s RunState) IsTerminal() bool {
	return s == StateError || s == StateIterLimit
}

// State holds the live RunState plus the spinner frame index and an
// optional one-line hint message that overrides the default hint
// when set (e.g. "interrupted", "queued — will land at next iter").
//
// Construct with NewState. Drive via Apply (for agent events),
// OnRunDone (when the Run goroutine returns), OnSubmit (when the
// user submits a prompt), and TickSpinner (on every spinnerTickMsg).
type State struct {
	current RunState
	hint    string
	frame   int
}

// NewState returns a fresh State at StateIdle.
func NewState() *State { return &State{current: StateIdle} }

// Current returns the live state.
func (s *State) Current() RunState { return s.current }

// Frame returns the current spinner frame index.
func (s *State) Frame() int { return s.frame }

// Hint returns the override hint message (or "" when no override is
// set). Overrides win over the default hint table.
func (s *State) Hint() string { return s.hint }

// SetHint installs (or clears) an override message. Cleared at the
// start of the next OnSubmit/OnRunDone so stale messages don't haunt
// future turns.
func (s *State) SetHint(msg string) { s.hint = msg }

// TickSpinner advances the spinner frame by 1. Called from the App
// on every spinnerTickMsg.
func (s *State) TickSpinner() { s.frame++ }

// OnSubmit transitions to StateRunning and clears any stale hint /
// terminal state. Called when the App kicks off controller.Run.
func (s *State) OnSubmit() {
	s.current = StateRunning
	s.hint = ""
}

// OnRunDone transitions out of the running cluster based on the
// error returned by controller.Run. Mirrors v1's handleRunDone:
//   - nil err → idle
//   - "iteration limit" sentinel → iter-limit pause
//   - other err → error state
//
// The caller passes an interrupted=true flag when the error came
// from a ctx.Cancel (Esc / Ctrl+C); we drop back to idle in that
// case and leave the hint for the App to display.
func (s *State) OnRunDone(err error, interrupted bool) {
	if err == nil {
		s.current = StateIdle
		s.hint = ""
		return
	}
	if interrupted {
		s.current = StateIdle
		s.hint = "interrupted"
		return
	}
	// Sentinel detection without importing the agent package — the
	// agent's iter-limit error always contains the literal phrase.
	if containsAny(err.Error(), "iteration limit") {
		s.current = StateIterLimit
		s.hint = "press Enter to continue, Ctrl+C to quit"
		return
	}
	s.current = StateError
	s.hint = "error: " + truncate(err.Error(), 120)
}

// Apply advances State in response to one agent event. Mirrors v1's
// updateStateFromEvent: terminal states are sticky (no overwrite
// from a stray turn-end), mid-run transitions map onto the coarse
// sub-phase enum.
func (s *State) Apply(e event.Event) {
	if s.current.IsTerminal() {
		return
	}
	switch e.Kind {
	case event.KindRunStart, event.KindRunResume, event.KindTurnStart, event.KindTurnEnd:
		s.current = StateRunning
	case event.KindRunEnd:
		// Run finished. Drop back to idle — the agent loop is done and the
		// user can type again. This covers both the user-prompt path (whose
		// RunDoneMsg does the same transition redundantly) and the
		// signal-wake path (background task / monitor event triggers runLoop
		// without going through startRun→RunDoneMsg).
		s.current = StateIdle
		s.hint = ""
	case event.KindThinking, event.KindThinkingChunk:
		s.current = StateThinking
	case event.KindText, event.KindTextChunk:
		s.current = StateTexting
	case event.KindToolUseStart:
		s.current = StateExecuting
	case event.KindToolUseResult:
		// Tool finished — back to generic running; the next
		// sub-phase event will move us forward again.
		s.current = StateRunning
	case event.KindDrainingInfo:
		s.current = StateDraining
	case event.KindCompacting:
		s.current = StateCompacting
	case event.KindCompactingEnd:
		s.current = StateRunning
	case event.KindIdle:
		// Agent signalled the loop is fully done (e.g. after a manual
		// /compact returned). Drop the status pill back to READY so the
		// user knows it's safe to type.
		s.current = StateIdle
		s.hint = ""
	case event.KindRunCancelled:
		s.current = StateIdle
	case event.KindIterLimit:
		s.current = StateIterLimit
		s.hint = "press Enter to continue, Ctrl+C to quit"
	case event.KindError:
		s.current = StateError
		if e.Error != nil && e.Error.Err != nil {
			s.hint = "error: " + truncate(e.Error.Err.Error(), 120)
		}
	}
}

// Dismiss clears a terminal state — used by the Esc handler when
// state is Error (matches the "Esc dismiss" hint). No-op for active
// states.
func (s *State) Dismiss() {
	if s.current == StateError {
		s.current = StateIdle
		s.hint = ""
	}
}

// containsAny reports whether s contains any of the given
// substrings. Local copy so we don't import strings just for one
// call site.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf — manual substring search to avoid importing strings here.
// Tiny implementation; performance doesn't matter (called once per
// run completion).
func indexOf(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// truncate clips s to max bytes, appending an ellipsis when cut.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
