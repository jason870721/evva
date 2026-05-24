package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Focusable is the contract every modal overlay implements. The App
// pushes one onto FocusStack when the user opens a panel; pops when
// Update reports close==true.
//
// The interface is small on purpose: each overlay owns its state,
// renders its body, and tells the App when it's done. The App
// drives the stack but knows nothing about a given overlay's
// internals.
//
// Hint contributes to the status-bar contextual hint line — when an
// overlay is on top of the stack, its Hint() wins over the
// RunState-default hint (see components/status.ResolveHint).
type Focusable interface {
	// Update receives one tea.Msg while this overlay sits on top of
	// the stack. It returns:
	//   - close: true when the App should pop this overlay (Esc,
	//            Enter-on-confirm, programmatic dismissal)
	//   - cmd:   any side-effect command the App should run next
	//            (textinput blink, controller.Compact dispatch, etc.)
	Update(msg tea.Msg) (close bool, cmd tea.Cmd)

	// View renders the overlay's body. The App is responsible for
	// positioning it within the layout; the overlay just produces
	// the content.
	View(width int, th *theme.Theme) string

	// Key returns a short identifier ("config"|"model"|"compact"|
	// "yank"|"search"|"permit"). Used for debugging and for
	// stack-state diagnostics; the App doesn't dispatch on it.
	Key() string

	// Modal reports whether this overlay consumes all key events.
	// Always true for v1-style picker panels; future non-modal
	// overlays (a transient toast) can return false.
	Modal() bool

	// Hint is the contextual hint shown above the status bar while
	// this overlay is on top. Empty string falls through to the
	// state-default hint.
	Hint() string
}

// FocusStack is the ordered modal stack. The topmost Focusable
// consumes key events; lower ones are visible but inert. Push on
// open; Pop on close. Designed to be small and side-effect-free —
// the App owns the lifecycle.
type FocusStack struct {
	stack []Focusable
}

// NewFocusStack returns an empty stack.
func NewFocusStack() *FocusStack { return &FocusStack{} }

// Push installs x as the new top. Nil is a no-op so callers don't
// need to nil-check before opening.
func (f *FocusStack) Push(x Focusable) {
	if x == nil {
		return
	}
	f.stack = append(f.stack, x)
}

// Pop removes and returns the top, or nil if empty.
func (f *FocusStack) Pop() Focusable {
	if len(f.stack) == 0 {
		return nil
	}
	top := f.stack[len(f.stack)-1]
	f.stack = f.stack[:len(f.stack)-1]
	return top
}

// Top returns the topmost Focusable without popping, or nil when
// the stack is empty.
func (f *FocusStack) Top() Focusable {
	if len(f.stack) == 0 {
		return nil
	}
	return f.stack[len(f.stack)-1]
}

// Len reports the stack depth — used by the App to decide whether
// to render an overlay slot at all.
func (f *FocusStack) Len() int { return len(f.stack) }

// Clear pops every overlay. Used during /clear and /model swaps to
// guarantee no stale UI clings to the next turn.
func (f *FocusStack) Clear() {
	f.stack = f.stack[:0]
}
