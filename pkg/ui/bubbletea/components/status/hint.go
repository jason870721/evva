package status

import "strings"

// HintProvider is anything that can contribute a one-line
// contextual hint to the status row above the input. The App walks
// the focus stack to find the topmost provider; if none yields a
// non-empty string, the default hint table for the current
// RunState is used instead.
//
// Future overlays (yank mode, search, permission) satisfy this
// interface so the status hint always reflects what keys the user
// can press right now.
type HintProvider interface {
	Hint() string
}

// DefaultHint returns the fallback hint for a RunState. The state's
// own override Hint (when set via SetHint) wins over this; this is
// the floor.
//
// We intentionally don't promise features that aren't bound yet:
//   - Ctrl+Y yank → wired in M8
//   - Ctrl+F search → wired in M9
// The strings here will be extended as those keys come online.
func DefaultHint(s RunState) string {
	switch s {
	case StateRunning, StateThinking, StateTexting, StateExecuting,
		StateDraining, StateCompacting:
		return "Esc cancel · Ctrl+C quit"
	case StateIterLimit:
		return "Enter continue · Ctrl+C quit"
	case StateError:
		return "Esc dismiss"
	default:
		// Idle: discoverability hint for the keys that work right now.
		return "/ commands · ↑↓ history · Ctrl+O fold · Esc quit"
	}
}

// ResolveHint picks the active hint string from three layered sources,
// in priority order:
//
//  1. The state's override (set by App when a transient event needs to
//     speak: "interrupted", "queued — will land at next iteration",
//     "no controller attached"). Wins over everything.
//  2. The provided focus HintProvider, if non-nil and yields a
//     non-empty Hint().
//  3. The RunState's default hint.
//
// Either or both upstream sources may be nil — the function tolerates
// missing layers.
func ResolveHint(state *State, focus HintProvider) string {
	if state != nil && state.Hint() != "" {
		return oneLine(state.Hint())
	}
	if focus != nil {
		if h := focus.Hint(); h != "" {
			return oneLine(h)
		}
	}
	if state == nil {
		return DefaultHint(StateIdle)
	}
	return DefaultHint(state.Current())
}

// oneLine flattens a hint onto a single row. Hints are routinely built
// from arbitrary error text ("clear: " + err.Error()) and a wrapped
// error can carry newlines; the layout budgets the footer at exactly
// two rows, so a hint that spans two pushes the frame past the terminal
// height and the renderer starts dropping rows off the top of the frame.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	flat := strings.NewReplacer("\r\n", " · ", "\n", " · ", "\r", " · ").Replace(s)
	return strings.Join(strings.Fields(flat), " ")
}
