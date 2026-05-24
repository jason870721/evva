package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// stubFocus is a minimal Focusable for stack-mechanics tests.
type stubFocus struct {
	key   string
	modal bool
	hint  string
}

func (s *stubFocus) Update(tea.Msg) (bool, tea.Cmd)             { return false, nil }
func (s *stubFocus) View(int, *theme.Theme) string              { return "" }
func (s *stubFocus) Key() string                                 { return s.key }
func (s *stubFocus) Modal() bool                                 { return s.modal }
func (s *stubFocus) Hint() string                                { return s.hint }

func TestFocusStackEmpty(t *testing.T) {
	f := NewFocusStack()
	if f.Len() != 0 {
		t.Errorf("Len = %d, want 0", f.Len())
	}
	if f.Top() != nil {
		t.Errorf("Top should be nil on empty stack")
	}
	if f.Pop() != nil {
		t.Errorf("Pop should be nil on empty stack")
	}
}

func TestFocusStackPushPop(t *testing.T) {
	f := NewFocusStack()
	a := &stubFocus{key: "a", modal: true}
	b := &stubFocus{key: "b", modal: true}
	f.Push(a)
	f.Push(b)
	if f.Len() != 2 {
		t.Errorf("Len after 2 pushes = %d, want 2", f.Len())
	}
	if f.Top().Key() != "b" {
		t.Errorf("Top after 2 pushes = %q, want b", f.Top().Key())
	}
	popped := f.Pop()
	if popped.Key() != "b" {
		t.Errorf("Pop returned %q, want b", popped.Key())
	}
	if f.Top().Key() != "a" {
		t.Errorf("Top after one pop = %q, want a", f.Top().Key())
	}
}

func TestFocusStackPushNilNoOp(t *testing.T) {
	f := NewFocusStack()
	f.Push(nil)
	if f.Len() != 0 {
		t.Errorf("Push(nil) should be a no-op, Len = %d", f.Len())
	}
}

func TestFocusStackClear(t *testing.T) {
	f := NewFocusStack()
	f.Push(&stubFocus{key: "a", modal: true})
	f.Push(&stubFocus{key: "b", modal: true})
	f.Clear()
	if f.Len() != 0 {
		t.Errorf("after Clear, Len = %d, want 0", f.Len())
	}
}
