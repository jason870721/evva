package overlays

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// stubController is a minimal ui.Controller-shaped struct for tests
// of overlays that only need a non-nil controller reference. We
// don't need to satisfy the full interface — the overlay
// constructors take ui.Controller and we use stubs that satisfy
// only the methods the tested code paths reach.

// TestNewCompactNilCtrl — NewCompact returns nil for a nil ctrl so
// the App can hint "no controller attached" instead of opening an
// empty overlay.
func TestNewCompactNilCtrl(t *testing.T) {
	if o := NewCompact(nil); o != nil {
		t.Errorf("NewCompact(nil) should return nil, got %+v", o)
	}
}

func TestCompactKeyAndModal(t *testing.T) {
	c := &Compact{choices: compactChoices}
	if c.Key() != "compact" {
		t.Errorf("Key = %q, want compact", c.Key())
	}
	if !c.Modal() {
		t.Errorf("Modal should be true")
	}
	if c.Hint() == "" {
		t.Errorf("Hint should be non-empty")
	}
}

func TestCompactUpDown(t *testing.T) {
	c := &Compact{choices: compactChoices}
	last := len(compactChoices) - 1

	if close, _ := c.Update(tea.KeyMsg{Type: tea.KeyDown}); close {
		t.Fatal("Down should not close")
	}
	if c.sel != 1 {
		t.Errorf("Down should advance sel to 1, got %d", c.sel)
	}
	// Walk past the end; selection must clamp at the last rung rather
	// than wrap or run off.
	for i := 0; i < len(compactChoices)+2; i++ {
		c.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if c.sel != last {
		t.Errorf("Down past last should clamp at %d, got %d", last, c.sel)
	}
	for i := 0; i < len(compactChoices)+2; i++ {
		c.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if c.sel != 0 {
		t.Errorf("Up past first should clamp at 0, got %d", c.sel)
	}
}

// TestCompactChoicesAreTheLadder locks the chooser to the auto path's
// escalation order — a user picking manually should see the same rungs,
// cheapest first, that the agent would climb on its own.
func TestCompactChoicesAreTheLadder(t *testing.T) {
	want := []string{"prune", "span", "full"}
	if len(compactChoices) != len(want) {
		t.Fatalf("chooser has %d rows, want %d", len(compactChoices), len(want))
	}
	for i, kind := range want {
		if compactChoices[i].Kind != kind {
			t.Errorf("row %d: got kind %q, want %q", i, compactChoices[i].Kind, kind)
		}
	}
}

func TestCompactEscCloses(t *testing.T) {
	c := &Compact{choices: compactChoices}
	close, _ := c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !close {
		t.Errorf("Esc should close the overlay")
	}
}

func TestCompactEnterDispatches(t *testing.T) {
	c := &Compact{choices: compactChoices}
	close, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !close {
		t.Errorf("Enter should close the overlay")
	}
	if cmd == nil {
		t.Errorf("Enter should return a dispatch cmd")
	}
}

func TestCompactViewRenders(t *testing.T) {
	c := &Compact{choices: compactChoices, sel: 1}
	out := c.View(80, theme.Default())
	if out == "" {
		t.Fatal("View should return non-empty output")
	}
	if !strings.Contains(out, "/COMPACT") {
		t.Errorf("View should include /COMPACT header: %q", out)
	}
	if !strings.Contains(out, "Micro") || !strings.Contains(out, "Full") {
		t.Errorf("View should list both choices: %q", out)
	}
}

// TestCompactJKAsArrows — vim-style navigation is a nice quality-of-
// life affordance, mirror the Up/Down behaviour for j/k.
func TestCompactJKAsArrows(t *testing.T) {
	c := &Compact{choices: compactChoices}
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if c.sel != 1 {
		t.Errorf("j should advance to 1, got %d", c.sel)
	}
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if c.sel != 0 {
		t.Errorf("k should revert to 0, got %d", c.sel)
	}
}
