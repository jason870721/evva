package status

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// saturatedBar is the widest the HUD ever gets: every optional cell
// populated, a long model id, seven-figure token counts, and an active
// state whose label ("EXECUTING") is among the longest.
func saturatedBar() *StatusBar {
	st := NewState()
	st.Apply(event.Event{Kind: event.KindToolUseStart})
	bar := New(st)
	bar.SetModel(string(constant.OPUS_4_8))
	bar.SetAgentID("1a2b3c4d5e6f7890")
	bar.SetAgentName("VERONICA")
	bar.SetEffort("medium")
	bar.SetPermissionMode("accept_edits")
	bar.SetUsage(llm.Usage{InputTokens: 1_250_000, OutputTokens: 345_000})
	bar.SetContext(120_000, 200_000)
	return bar
}

// The bar must occupy exactly one row at every width. th.StatusBar sets
// Width() over a background with horizontal padding, and lipgloss
// hard-wraps over-wide content — a two-line bar renders as two stacked
// full-width bars and Bubble Tea's partial repaint leaves them on screen
// as ghosts.
func TestComposeIsAlwaysExactlyOneRow(t *testing.T) {
	th := theme.Default()
	idle := New(NewState())
	idle.SetModel(string(constant.OPUS_4_8))

	bars := map[string]*StatusBar{"saturated": saturatedBar(), "idle": idle}
	for name, bar := range bars {
		for width := 1; width <= 220; width++ {
			out := bar.Compose(width, th)
			if n := strings.Count(out, "\n"); n != 0 {
				t.Fatalf("%s bar at width %d rendered %d rows, want 1:\n%q", name, width, n+1, out)
			}
			if got := lipgloss.Width(out); got != width {
				t.Fatalf("%s bar at width %d measured %d columns, want %d", name, width, got, width)
			}
		}
	}
}

// A width that fits everything keeps every cell — narrowing must not
// cost anything until it has to.
func TestComposeKeepsEveryCellWhenItFits(t *testing.T) {
	out := saturatedBar().Compose(400, theme.Default())
	// Probes stay inside a single styled run — cells glue several
	// Render calls together, so "SID " and the id are not contiguous.
	for _, want := range []string{"EXECUTING", "VERONICA", string(constant.OPUS_4_8),
		"medium", "IN ", "OUT ", "CTX ", "accept_edits", "SID ", "1a2b3c4d"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide bar dropped %q:\n%s", want, out)
		}
	}
}

// Cells leave in rank order as the terminal narrows, and the run-state
// pill never leaves at all.
func TestComposeDropsCellsInRankOrder(t *testing.T) {
	th := theme.Default()
	bar := saturatedBar()

	// Rendered from lowest rank to highest: the order cells drop in.
	order := []struct {
		rank  string
		probe string
	}{
		{"sid", "SID "},
		{"agent name", "VERONICA"},
		{"spend", "OUT "},
		{"context", "CTX "},
		{"model", string(constant.OPUS_4_8)},
		{"permission mode", "accept_edits"},
	}

	// Walking inward one column at a time, a cell that has dropped must
	// never reappear, and no cell may drop before a lower-ranked one.
	dropped := 0
	for width := 400; width >= 20; width-- {
		out := bar.Compose(width, th)
		if !strings.Contains(out, "EXECUTING") {
			t.Fatalf("state pill dropped at width %d — it is pinned:\n%s", width, out)
		}
		for dropped < len(order) && !strings.Contains(out, order[dropped].probe) {
			dropped++
		}
		for i, c := range order {
			if got := strings.Contains(out, c.probe); got != (i >= dropped) {
				t.Fatalf("at width %d %s cell present=%v, want %v (dropped through %d):\n%s",
					width, c.rank, got, i >= dropped, dropped, out)
			}
		}
	}
	if dropped == 0 {
		t.Fatal("narrowing to 20 columns dropped nothing — the fit path never ran")
	}
}

// When only the pinned cell is left and it still overflows, the bar
// truncates rather than wrapping.
func TestFitCellsTruncatesPinnedOverflow(t *testing.T) {
	cells := []hudCell{{strings.Repeat("x", 40), rankPinned}}
	got := fitCells(cells, " ◆ ", 10)
	if lipgloss.Width(got) != 10 {
		t.Errorf("pinned overflow = %d columns, want 10: %q", lipgloss.Width(got), got)
	}
}

// Dropping a cell must not leave its separator behind as a dangling
// diamond.
func TestFitCellsNoDanglingSeparator(t *testing.T) {
	cells := []hudCell{{"AAAA", rankPinned}, {"BBBB", rankSID}}
	got := fitCells(cells, " ◆ ", 6)
	if got != "AAAA" {
		t.Errorf("fitCells = %q, want %q", got, "AAAA")
	}
}

// Empty cells collapse without contributing a separator — the same
// guarantee the pre-fit code gave for a "default" permission mode and
// an unset agent id.
func TestComposeCollapsesEmptyCells(t *testing.T) {
	bar := New(NewState())
	bar.SetModel("m")
	out := bar.Compose(200, theme.Default())
	if strings.Contains(out, "◆  ◆") {
		t.Errorf("empty cell left a dangling separator:\n%s", out)
	}
	if strings.Contains(out, "SID") || strings.Contains(out, "⛨") {
		t.Errorf("unset cells should collapse entirely:\n%s", out)
	}
}

// A hint carrying a wrapped error's newlines must still be one row —
// the layout budgets the footer at exactly two.
func TestResolveHintFlattensNewlines(t *testing.T) {
	st := NewState()
	st.SetHint("clear: cannot clear mid-run\ncaused by: session busy\r\nretry later")
	got := ResolveHint(st, nil)
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("hint still spans rows: %q", got)
	}
	for _, want := range []string{"cannot clear mid-run", "session busy", "retry later"} {
		if !strings.Contains(got, want) {
			t.Errorf("flattened hint lost %q: %q", want, got)
		}
	}
}

// Single-line hints pass through byte-for-byte.
func TestResolveHintLeavesSingleLineAlone(t *testing.T) {
	st := NewState()
	st.SetHint("queued — will land at next iteration")
	if got := ResolveHint(st, nil); got != "queued — will land at next iteration" {
		t.Errorf("ResolveHint mangled a single-line hint: %q", got)
	}
}
