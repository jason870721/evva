package slash

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

func TestMatchEmpty(t *testing.T) {
	if got := Match("", Catalog(nil)); got != nil {
		t.Errorf("empty input should match nil, got %+v", got)
	}
}

func TestMatchNonSlashInput(t *testing.T) {
	if got := Match("hello", Catalog(nil)); got != nil {
		t.Errorf("non-/ input should match nil, got %+v", got)
	}
}

func TestMatchPrefixFiltering(t *testing.T) {
	got := Match("/co", Catalog(nil))
	// Should include /compact, /config; should NOT include /model, /clear, /exit
	names := commandNames(got)
	if !contains(names, "/compact") || !contains(names, "/config") {
		t.Errorf("/co should match /compact and /config, got %v", names)
	}
	if contains(names, "/model") {
		t.Errorf("/co should NOT match /model: %v", names)
	}
}

func TestMatchExact(t *testing.T) {
	got := Match("/clear", Catalog(nil))
	if len(got) != 1 || got[0].Name != "/clear" {
		t.Errorf("exact match should return only that command, got %+v", got)
	}
}

func TestMatchReturnsAll(t *testing.T) {
	// Match no longer caps the result — the cap moved to View's render
	// window so navigation (MoveSel/Complete) can reach every entry.
	// A catalog of 10 "/x*" entries must all come back.
	cat := []Command{}
	for i := 0; i < 10; i++ {
		cat = append(cat, Command{Name: "/x" + string(rune('a'+i)), Desc: "x"})
	}
	got := Match("/x", cat)
	if len(got) != 10 {
		t.Errorf("Match should return all 10 entries, got %d", len(got))
	}
}

func TestMoveSelReachesLastBeyondWindow(t *testing.T) {
	// Regression: a match list longer than the render window must stay
	// fully navigable. Walk +1 to the end of a 10-entry list and verify
	// the last entry is selectable and completable.
	p := New()
	cat := []Command{}
	for i := 0; i < 10; i++ {
		cat = append(cat, Command{Name: "/x" + string(rune('a'+i)), Desc: "x"})
	}
	moves := 0
	for p.MoveSel("/x", cat, +1) {
		moves++
		if moves > 100 {
			t.Fatal("MoveSel never stopped — runaway")
		}
	}
	if got := p.Selected(); got != 9 {
		t.Errorf("should reach last index 9, got %d", got)
	}
	if got := p.Complete("/x", cat); got != "/xj" {
		t.Errorf("Complete at last entry = %q, want /xj", got)
	}
}

func TestPanelDismissAndReset(t *testing.T) {
	p := New()
	catalog := Catalog(nil)
	// Visible before dismiss.
	if !p.Visible("/c", false, catalog) {
		t.Fatal("panel should be visible for /c")
	}
	p.Dismiss()
	if p.Visible("/c", false, catalog) {
		t.Errorf("panel should be hidden after Dismiss")
	}
	p.Reset()
	if !p.Visible("/c", false, catalog) {
		t.Errorf("panel should be visible after Reset")
	}
}

func TestPanelHiddenWhenOverlayOpen(t *testing.T) {
	p := New()
	if p.Visible("/c", true, Catalog(nil)) {
		t.Errorf("panel should be hidden when overlay is open")
	}
}

func TestPanelMoveSel(t *testing.T) {
	p := New()
	catalog := Catalog(nil)
	// "/c" matches /compact, /config, /clear → 3 entries.
	if !p.MoveSel("/c", catalog, +1) {
		t.Fatal("MoveSel(+1) should engage from idx 0")
	}
	if got := p.Selected(); got != 1 {
		t.Errorf("after MoveSel(+1), Selected = %d, want 1", got)
	}
	if !p.MoveSel("/c", catalog, +1) {
		t.Fatal("MoveSel(+1) again should engage")
	}
	if !p.MoveSel("/c", catalog, +1) {
		// Already at last; should report no movement.
		// But Visible logic might still return true... let me check:
		// Actually MoveSel returns whether selected CHANGED.
	}
	// Hitting +1 past the end should be a no-op.
	prev := p.Selected()
	if p.MoveSel("/c", catalog, +1) {
		t.Errorf("MoveSel past last entry should return false (prev=%d)", prev)
	}
}

func TestPanelComplete(t *testing.T) {
	p := New()
	catalog := Catalog(nil)
	// At index 0, /co matches: /compact, /config — first is /compact.
	got := p.Complete("/co", catalog)
	if got != "/compact" {
		t.Errorf("Complete at idx 0 = %q, want /compact", got)
	}
	// Move to the next match.
	p.MoveSel("/co", catalog, +1)
	got = p.Complete("/co", catalog)
	if got != "/config" {
		t.Errorf("Complete at idx 1 = %q, want /config", got)
	}
}

func TestPanelViewRendersMatches(t *testing.T) {
	p := New()
	out := p.View("/co", nil, 80, theme.Default())
	if out == "" {
		t.Fatal("View should render for /co")
	}
	if !strings.Contains(out, "compact") {
		t.Errorf("View should mention /compact: %q", out)
	}
	if !strings.Contains(out, "config") {
		t.Errorf("View should mention /config: %q", out)
	}
}

func TestPanelViewExactMatchMarker(t *testing.T) {
	p := New()
	out := p.View("/clear", nil, 80, theme.Default())
	if !strings.Contains(out, "✓") {
		t.Errorf("View should show ✓ marker for exact match: %q", out)
	}
}

func TestViewWindowsLongList(t *testing.T) {
	// "/" matches every builtin (>maxSuggestions). The initial window
	// shows the head of the list plus a "more" indicator; entries past
	// the window are not drawn.
	p := New()
	out := p.View("/", nil, 80, theme.Default())
	if !strings.Contains(out, "compact") {
		t.Errorf("initial window should show /compact: %q", out)
	}
	if strings.Contains(out, "exit") {
		t.Errorf("last command should be off the initial window: %q", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("a list longer than the window should show a 'more' indicator: %q", out)
	}
}

func TestViewWindowFollowsSelection(t *testing.T) {
	// Driving the selection to the last entry scrolls the window: the
	// last command becomes visible and the first scrolls off.
	p := New()
	cat := Catalog(nil)
	for p.MoveSel("/", cat, +1) {
	}
	out := p.View("/", nil, 80, theme.Default())
	if !strings.Contains(out, "exit") {
		t.Errorf("window should follow selection to show /exit: %q", out)
	}
	if strings.Contains(out, "compact") {
		t.Errorf("first command should have scrolled off-window: %q", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("a scrolled window should still show a 'more' indicator: %q", out)
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func commandNames(cs []Command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
