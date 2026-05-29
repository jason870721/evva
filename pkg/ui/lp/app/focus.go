package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Focusable is the contract every modal overlay implements. lp pushes one
// onto FocusStack when the user opens a panel and pops when Update reports
// close==true.
//
// The method set matches the bubbletea overlays exactly (same imported
// theme.Theme type), so lp reuses those overlays — they satisfy this
// interface structurally without any adapter.
type Focusable interface {
	Update(msg tea.Msg) (close bool, cmd tea.Cmd)
	View(width int, th *theme.Theme) string
	Key() string
	Modal() bool
	Hint() string
}

// FocusStack is the ordered modal stack. The topmost Focusable consumes
// key events; lower ones are visible but inert.
type FocusStack struct {
	stack []Focusable
}

// NewFocusStack returns an empty stack.
func NewFocusStack() *FocusStack { return &FocusStack{} }

// Push installs x as the new top. Nil is a no-op.
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

// Top returns the topmost Focusable without popping, or nil when empty.
func (f *FocusStack) Top() Focusable {
	if len(f.stack) == 0 {
		return nil
	}
	return f.stack[len(f.stack)-1]
}

// Len reports the stack depth.
func (f *FocusStack) Len() int { return len(f.stack) }

// Clear pops every overlay.
func (f *FocusStack) Clear() { f.stack = f.stack[:0] }
