package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/slash"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// enterKey is the KeyMsg the App sees when the user presses Enter.
var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

// TestSlashEnterCompletesAndDispatches verifies that pressing Enter
// while the slash panel is open completes the partial input to the
// highlighted command AND runs it — no separate Tab needed.
//
// Observable signal: /clear unconditionally resets the transcript
// (even with a nil controller), dropping the user prompt back to the
// banner-only baseline. The OLD behavior submitted the raw "/cl" text
// to the agent default-branch, which does NOT reset the transcript —
// so a shrunk transcript proves the completion+dispatch happened.
func TestSlashEnterCompletesAndDispatches(t *testing.T) {
	a := New(t.TempDir())
	base := len(a.transcript.Blocks()) // banner-only baseline
	a.transcript.AppendUserPrompt("hello")
	if len(a.transcript.Blocks()) != base+1 {
		t.Fatalf("precondition: append should add one block, got %d", len(a.transcript.Blocks()))
	}

	a.input.SetValue("/cl") // unambiguous prefix of /clear
	if !a.slashVisible() {
		t.Fatal("precondition: slash panel should be visible for /cl")
	}

	a.handleKey(enterKey)

	if got := len(a.transcript.Blocks()); got != base {
		t.Errorf("/cl + Enter should complete to /clear and reset the transcript to baseline %d, got %d", base, got)
	}
	if v := a.input.Value(); v != "" {
		t.Errorf("input should be cleared after dispatch, got %q", v)
	}
}

// TestSlashEnterDispatchesHighlighted verifies Enter runs the
// currently-highlighted suggestion, not just the first match. For
// "/c" the matches are /compact, /config, /cost, /clear; moving the
// selection down to /clear (index 3) and pressing Enter must reset
// the transcript — which only /clear does.
func TestSlashEnterDispatchesHighlighted(t *testing.T) {
	a := New(t.TempDir())
	base := len(a.transcript.Blocks())
	a.transcript.AppendUserPrompt("hello")

	a.input.SetValue("/c")
	cat := slash.Catalog(a.controller)
	for i := 0; i < 3; i++ {
		if !a.slash.MoveSel("/c", cat, +1) {
			t.Fatalf("MoveSel(+1) #%d should engage", i+1)
		}
	}
	if got := a.slash.Complete("/c", cat); got != "/clear" {
		t.Fatalf("precondition: selection should rest on /clear, got %q", got)
	}

	a.handleKey(enterKey)

	if got := len(a.transcript.Blocks()); got != base {
		t.Errorf("Enter should dispatch the highlighted /clear and reset to baseline %d, got %d", base, got)
	}
}

// TestSlashEnterDoesNotResetForCompact pins the inverse: for "/c" the
// default highlight is /compact, which does NOT reset the transcript.
// This confirms the dispatch follows the highlighted command rather
// than resetting by accident — and that Enter still consumed the key
// (input cleared) instead of submitting "/c" as a prompt.
func TestSlashEnterDoesNotResetForCompact(t *testing.T) {
	a := New(t.TempDir())
	a.transcript.AppendUserPrompt("hello")
	before := len(a.transcript.Blocks())

	a.input.SetValue("/c") // highlight defaults to /compact (index 0)
	if !a.slashVisible() {
		t.Fatal("precondition: slash panel should be visible for /c")
	}

	a.handleKey(enterKey)

	if got := len(a.transcript.Blocks()); got != before {
		t.Errorf("/compact must not reset the transcript; want %d blocks, got %d", before, got)
	}
	if v := a.input.Value(); v != "" {
		t.Errorf("input should be cleared after dispatching /compact, got %q", v)
	}
}
