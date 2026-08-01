package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// contextTopN is how many blocks the overlay asks for. Enough to cover
// the heavy tail of a long session without turning the panel into a
// transcript — the categories table above it already answers "where did
// the weight go" in aggregate.
const contextTopN = 40

// contextRows is the visible window; the list scrolls inside it.
const contextRows = 12

// Context is the /context panel: the block-ledger breakdown behind the
// status bar's gauge, plus the pin control.
//
// Unlike /cost this overlay is not purely read-only — Space toggles a pin
// on the selected block — so it re-snapshots after every mutation rather
// than caching at construction. The snapshot itself is cheap (one pass
// over the message slice) and taking a fresh one is what keeps the pin
// column honest.
type Context struct {
	ctrl   ui.Controller
	report ui.ContextReport
	sel    int
	top    int
	notice string
}

// NewContext opens the panel. Returns nil when ctrl is nil (pre-Attach),
// matching every other overlay's contract.
func NewContext(ctrl ui.Controller) *Context {
	if ctrl == nil {
		return nil
	}
	c := &Context{ctrl: ctrl}
	c.refresh()
	return c
}

func (c *Context) refresh() { c.report = c.ctrl.ContextReport(contextTopN) }

func (c *Context) Key() string { return "context" }
func (c *Context) Modal() bool { return true }
func (c *Context) Hint() string {
	return "[↑↓] select · [Space] pin/unpin · [r] refresh · [Esc] close"
}

func (c *Context) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c", "q":
		return true, nil
	case "up", "k":
		c.move(-1)
	case "down", "j":
		c.move(1)
	case "r":
		c.refresh()
		c.notice = ""
	case " ", "space":
		c.togglePin()
	}
	return false, nil
}

func (c *Context) move(d int) {
	n := len(c.report.Blocks)
	if n == 0 {
		return
	}
	c.sel = min(max(c.sel+d, 0), n-1)
	// Keep the selection inside the scroll window.
	if c.sel < c.top {
		c.top = c.sel
	}
	if c.sel >= c.top+contextRows {
		c.top = c.sel - contextRows + 1
	}
}

// togglePin flips the pin on the selected block and re-snapshots. Blocks
// with no tool id (system prompt, user and assistant turns) are not
// pinnable: the ladder never prunes them in the first place, so a pin
// there would be a control that does nothing.
func (c *Context) togglePin() {
	if c.sel >= len(c.report.Blocks) {
		return
	}
	b := c.report.Blocks[c.sel]
	if b.ToolID == "" {
		c.notice = "only tool results can be pinned — conversation turns are never pruned"
		return
	}
	pinned := c.ctrl.TogglePinnedBlock(b.ToolID)
	c.refresh()
	if pinned {
		c.notice = "pinned " + contextSubject(b) + " — it now survives pruning and compaction"
	} else {
		c.notice = "unpinned " + contextSubject(b)
	}
}

func (c *Context) View(width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /CONTEXT — where the prompt's weight sits"))
	b.WriteString("\n")

	// Gauge line. An unknown model has no window size, and saying so is
	// better than rendering a percentage against a made-up denominator.
	if c.report.LimitTokens > 0 {
		pct := float64(c.report.UsedTokens) * 100 / float64(c.report.LimitTokens)
		b.WriteString(th.DimText.Render("prompt · ") +
			th.StatusValue.Render(fmt.Sprintf("%s / %s tokens", costGroup(c.report.UsedTokens), costGroup(c.report.LimitTokens))) +
			th.DimText.Render(fmt.Sprintf("  (%.1f%% of the window)", pct)))
	} else {
		b.WriteString(th.DimText.Render("prompt · ") +
			th.StatusValue.Render(costGroup(c.report.UsedTokens)+" tokens") +
			th.DimText.Render("  (window size unknown for this model)"))
	}
	b.WriteString("\n")
	b.WriteString(th.DimText.Render(fmt.Sprintf("ledger · %s across %d turn(s)",
		contextSize(c.report.TotalBytes), c.report.Turns)))
	b.WriteString("\n\n")

	contextCategoryTable(&b, th, c.report)
	b.WriteString("\n")

	if len(c.report.Blocks) == 0 {
		b.WriteString(th.DimText.Render("  no blocks yet — the ledger fills in as the session runs"))
	} else {
		b.WriteString(contextListHeader(th))
		b.WriteString("\n")
		end := min(c.top+contextRows, len(c.report.Blocks))
		for i := c.top; i < end; i++ {
			b.WriteString(contextRow(th, c.report.Blocks[i], i == c.sel))
			b.WriteString("\n")
		}
		if end < len(c.report.Blocks) {
			b.WriteString(th.DimText.Render(fmt.Sprintf("  … %d more", len(c.report.Blocks)-end)))
			b.WriteString("\n")
		}
	}

	if c.notice != "" {
		b.WriteString("\n")
		b.WriteString(th.ToolOK.Render("  " + c.notice))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(th.DimText.Render("Pinned blocks are exempt from pruning and are re-injected verbatim after compaction."))
	b.WriteString("\n")
	b.WriteString(th.FooterHint.Render(c.Hint()))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// contextCategoryOrder fixes the row order so the table doesn't shuffle
// between renders as a map would.
var contextCategoryOrder = []string{"system", "user", "assistant", "file", "tool"}

var contextCategoryDesc = map[string]string{
	"system":    "persona prompt + tool schemas",
	"user":      "your turns and injected signals",
	"assistant": "model text, thinking, tool arguments",
	"file":      "file reads · cheapest to recover",
	"tool":      "other tool output",
}

func contextCategoryTable(b *strings.Builder, th *theme.Theme, r ui.ContextReport) {
	b.WriteString("  " + th.DimText.Render(fmt.Sprintf("%-11s%12s%8s  %s", "category", "bytes", "share", "")))
	b.WriteString("\n")
	for _, cat := range contextCategoryOrder {
		n, ok := r.Categories[cat]
		if !ok || n == 0 {
			continue
		}
		share := 0.0
		if r.TotalBytes > 0 {
			share = float64(n) * 100 / float64(r.TotalBytes)
		}
		b.WriteString("  " +
			th.PanelRow.Render(fmt.Sprintf("%-11s", cat)) +
			th.StatusValue.Render(fmt.Sprintf("%12s", contextSize(n))) +
			th.DimText.Render(fmt.Sprintf("%7.1f%%", share)) +
			th.DimText.Render("  "+contextCategoryDesc[cat]))
		b.WriteString("\n")
	}
}

func contextListHeader(th *theme.Theme) string {
	return "  " + th.DimText.Render(fmt.Sprintf("%-2s%-6s%-10s%-22s%10s", "", "turn", "tool", "subject", "bytes"))
}

// contextRow renders one block. Plain text is padded to width BEFORE
// styling so ANSI escapes never throw off the columns — the same
// discipline the /cost and /mcp panels use.
func contextRow(th *theme.Theme, b ui.ContextBlock, selected bool) string {
	marker := " "
	switch {
	case b.Pinned:
		marker = "📌"
	case b.IsError:
		marker = "✘"
	case b.Pruned:
		marker = "◌"
	}

	name := b.ToolName
	if name == "" {
		name = b.Category
	}

	style := th.PanelRow
	if selected {
		style = th.StatusValue
	}
	row := fmt.Sprintf("%-2s%-6s%-10s%-22s%10s",
		marker,
		contextTurn(b.Turn),
		truncCell(name, 9),
		truncCell(contextSubject(b), 21),
		contextSize(b.Bytes),
	)
	prefix := "  "
	if selected {
		prefix = "▸ "
	}
	if b.Pruned {
		// A tombstoned block is bookkeeping, not live weight — dim it so
		// the eye skips to what can still be reclaimed.
		return prefix + th.DimText.Render(row)
	}
	return prefix + style.Render(row)
}

func contextSubject(b ui.ContextBlock) string {
	if b.Label != "" {
		return b.Label
	}
	if b.ToolName != "" {
		return b.ToolName
	}
	return b.Category
}

func contextTurn(t int) string {
	if t <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d", t)
}

// contextSize formats bytes for a column: same vocabulary the tombstones
// use, so a number in the overlay matches the number the model was told.
func contextSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// truncCell clips s to n runes with an ellipsis. Rune-based so a path
// with wide characters doesn't overflow the column.
func truncCell(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
