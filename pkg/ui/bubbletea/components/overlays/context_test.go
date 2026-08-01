package overlays

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// contextCtrl is a ui.Controller stub answering only what the overlay
// reads. Pins are tracked so the toggle path can be exercised end to end.
type contextCtrl struct {
	ui.Controller
	report  ui.ContextReport
	pins    map[string]bool
	toggles int
}

func (c *contextCtrl) ContextReport(topN int) ui.ContextReport {
	out := c.report
	out.Blocks = make([]ui.ContextBlock, len(c.report.Blocks))
	copy(out.Blocks, c.report.Blocks)
	for i := range out.Blocks {
		out.Blocks[i].Pinned = c.pins[out.Blocks[i].ToolID]
	}
	if topN > 0 && len(out.Blocks) > topN {
		out.Blocks = out.Blocks[:topN]
	}
	return out
}

func (c *contextCtrl) TogglePinnedBlock(id string) bool {
	c.toggles++
	c.pins[id] = !c.pins[id]
	return c.pins[id]
}

func sampleReport() ui.ContextReport {
	return ui.ContextReport{
		Blocks: []ui.ContextBlock{
			{ToolID: "t1", Category: "file", ToolName: "read", Label: "loop.go", Bytes: 41_000, Turn: 4},
			{ToolID: "t2", Category: "tool", ToolName: "bash", Label: "go", Bytes: 8_600, Turn: 6, IsError: true},
			{ToolID: "t3", Category: "tool", ToolName: "grep", Label: "func New", Bytes: 3_100, Turn: 7, Pruned: true},
			{Category: "system", Label: "system prompt", Bytes: 12_000},
		},
		Categories:  map[string]int{"file": 41_000, "tool": 11_700, "system": 12_000},
		TotalBytes:  64_700,
		Turns:       7,
		UsedTokens:  120_000,
		LimitTokens: 200_000,
	}
}

func newTestContext(t *testing.T) (*Context, *contextCtrl) {
	t.Helper()
	c := &contextCtrl{report: sampleReport(), pins: map[string]bool{}}
	o := NewContext(c)
	if o == nil {
		t.Fatal("NewContext returned nil for a non-nil controller")
	}
	return o, c
}

func TestContextNilControllerReturnsNil(t *testing.T) {
	if NewContext(nil) != nil {
		t.Error("NewContext(nil) should return nil so the App can hint instead of opening an empty panel")
	}
}

func TestContextViewShowsGaugeAndCategories(t *testing.T) {
	o, _ := newTestContext(t)
	view := o.View(100, theme.Default())

	for _, want := range []string{"/CONTEXT", "120,000", "200,000", "60.0%", "file", "system", "loop.go"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

// An unknown model has no window size. Rendering a percentage against a
// zero denominator would be a lie (and a divide); the panel must say so.
func TestContextViewHandlesUnknownWindow(t *testing.T) {
	c := &contextCtrl{report: sampleReport(), pins: map[string]bool{}}
	c.report.LimitTokens = 0
	o := NewContext(c)

	view := o.View(100, theme.Default())
	if !strings.Contains(view, "window size unknown") {
		t.Error("an unknown context window should be stated, not rendered as a percentage")
	}
	if strings.Contains(view, "%)") {
		t.Error("a percentage was rendered against an unknown window size")
	}
}

func TestContextSpaceTogglesPin(t *testing.T) {
	o, ctrl := newTestContext(t)

	if close, _ := o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}); close {
		t.Fatal("Space should not close the panel")
	}
	if ctrl.toggles != 1 {
		t.Fatalf("expected 1 toggle, got %d", ctrl.toggles)
	}
	if !ctrl.pins["t1"] {
		t.Error("the selected block was not pinned")
	}
	// The overlay re-snapshots after a mutation, so the pin marker must
	// be visible without an explicit refresh.
	if !strings.Contains(o.View(100, theme.Default()), "📌") {
		t.Error("pin marker did not appear after toggling")
	}

	o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if ctrl.pins["t1"] {
		t.Error("second Space should unpin")
	}
}

// Only tool results are prunable, so only they are pinnable — offering
// the control on a conversation turn would be a button that does nothing.
func TestContextRefusesToPinNonToolBlocks(t *testing.T) {
	o, ctrl := newTestContext(t)
	// Block index 3 is the system prompt (no tool id).
	for i := 0; i < 3; i++ {
		o.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	if ctrl.toggles != 0 {
		t.Errorf("pinning a non-tool block reached the controller (%d toggles)", ctrl.toggles)
	}
	if !strings.Contains(o.View(100, theme.Default()), "only tool results can be pinned") {
		t.Error("the refusal was silent; the user needs to know why nothing happened")
	}
}

func TestContextSelectionClamps(t *testing.T) {
	o, _ := newTestContext(t)
	for i := 0; i < 20; i++ {
		o.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if o.sel != len(o.report.Blocks)-1 {
		t.Errorf("selection ran past the end: %d", o.sel)
	}
	for i := 0; i < 20; i++ {
		o.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if o.sel != 0 {
		t.Errorf("selection ran past the start: %d", o.sel)
	}
}

func TestContextEscCloses(t *testing.T) {
	o, _ := newTestContext(t)
	if close, _ := o.Update(tea.KeyMsg{Type: tea.KeyEsc}); !close {
		t.Error("Esc should close the panel")
	}
}

func TestContextEmptyLedgerRenders(t *testing.T) {
	c := &contextCtrl{report: ui.ContextReport{Categories: map[string]int{}}, pins: map[string]bool{}}
	o := NewContext(c)
	view := o.View(80, theme.Default())
	if !strings.Contains(view, "no blocks yet") {
		t.Error("an empty ledger should say so rather than render an empty table")
	}
	// Space on an empty list must not panic or reach the controller.
	o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if c.toggles != 0 {
		t.Error("Space on an empty ledger reached the controller")
	}
}

func TestContextSizeFormatting(t *testing.T) {
	cases := map[int]string{0: "0B", 512: "512B", 2048: "2.0KB", 1 << 21: "2.0MB"}
	for in, want := range cases {
		if got := contextSize(in); got != want {
			t.Errorf("contextSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncCell(t *testing.T) {
	if got := truncCell("short", 10); got != "short" {
		t.Errorf("under-length string was modified: %q", got)
	}
	if got := truncCell("averylongfilename.go", 8); len([]rune(got)) != 8 {
		t.Errorf("truncCell did not clip to 8 runes: %q (%d)", got, len([]rune(got)))
	}
	// Rune-based, so a multi-byte subject clips by display cells rather
	// than by bytes.
	if got := truncCell("日本語のファイル名", 5); len([]rune(got)) != 5 {
		t.Errorf("multi-byte clip wrong: %q (%d runes)", got, len([]rune(got)))
	}
}
