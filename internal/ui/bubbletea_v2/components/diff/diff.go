// Package diff renders a *fs.FileDiff (the structured metadata
// write_file / edit_file attach to tools.Result) as a multi-line
// git-style string with a gutter / content split.
//
// Layout per row:
//
//	|<oldNum>|<newNum>|<sign>| <text>
//	└─ gutter (muted) ──────┘└─ content ─┘
//
// The gutter holds the two line-number columns and the +/-/space
// sign; the content column holds the actual file text with syntax
// highlighting applied via chroma (keyed by file extension).
// Splitting them matters for terminal copy-paste — selecting just the
// content column is the right ergonomic, line numbers shouldn't paste
// into the clipboard as if they were code.
//
// Width handling: gutter width adapts to the largest line number in
// the diff so a 9-line file doesn't reserve four columns of empty
// space. Content rows still take an optional `width` arg for the
// background-fill behavior on add/remove rows (M2 visual contract).
//
// Lives in components/ because it's reused by both the transcript
// tool-result block (M3) and the permission overlay's diff preview
// (M10). Theme-free callers can pass theme.Default().
package diff

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/tools/fs"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
)

// Render returns a multi-line styled diff. width is the column count
// each + / - row's CONTENT column is padded to so its background tint
// reads as a solid block across the transcript column. width <= 0
// disables the fill (used in tests). The gutter prefix is added on
// top of width — callers sizing the transcript column should pass the
// full available width and trust Render to subtract the gutter.
func Render(d *fs.FileDiff, th *theme.Theme, width int) string {
	if d == nil || th == nil {
		return ""
	}

	hl := getHighlighter(d.Path)

	maxLine := maxLineNumber(d)
	colWidth := digits(maxLine)
	if colWidth < 1 {
		colWidth = 1
	}
	// gutter = "<old>" + " " + "<new>" + " " + "<sign>" + " "
	gutterWidth := colWidth*2 + 4
	contentWidth := width - gutterWidth
	if contentWidth < 0 {
		contentWidth = 0
	}

	var b strings.Builder
	b.WriteString(th.DiffHeader.Render(fmt.Sprintf("diff %s a/%s b/%s", d.Op, d.Path, d.Path)))
	b.WriteByte('\n')

	for _, h := range d.Hunks {
		b.WriteString(th.DiffHeader.Render(
			fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount),
		))
		b.WriteByte('\n')
		for _, ln := range h.Lines {
			b.WriteString(renderRow(th, ln, colWidth, contentWidth, hl))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderRow assembles one diff line as gutter+content. The gutter is
// styled via th.DiffContext (muted, no background) so terminal
// selection of the content column doesn't drag along the line-number
// gutter. Content gets syntax highlighting via hl when available,
// with add / remove / context background fills from the theme.
func renderRow(th *theme.Theme, ln fs.DiffLine, colWidth, contentWidth int, hl *highlighter) string {
	oldCell := lineNumCell(ln.Old, colWidth)
	newCell := lineNumCell(ln.New, colWidth)
	gutterText := oldCell + " " + newCell + " " + signFor(ln.Kind) + " "
	gutter := th.DiffContext.Render(gutterText)

	var bg lipgloss.Style
	switch ln.Kind {
	case fs.LineAdd:
		bg = th.DiffAdd
	case fs.LineRemove:
		bg = th.DiffRemove
	default:
		bg = th.DiffContext
	}

	content := highlightLine(ln.Text, bg, hl, contentWidth)

	return gutter + content
}

func signFor(kind string) string {
	switch kind {
	case fs.LineAdd:
		return "+"
	case fs.LineRemove:
		return "-"
	default:
		return " "
	}
}

// lineNumCell right-aligns n into a column of the given width. Zero
// renders as blank — that's how the data model says "this line
// doesn't exist on this side."
func lineNumCell(n, width int) string {
	if n == 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, n)
}

// maxLineNumber walks every hunk to find the largest old/new line
// number — used to size the gutter columns just wide enough.
func maxLineNumber(d *fs.FileDiff) int {
	max := 0
	for _, h := range d.Hunks {
		// Hunk header end-of-range is a tight upper bound and avoids
		// scanning every line for the common case.
		if v := h.OldStart + h.OldCount; v > max {
			max = v
		}
		if v := h.NewStart + h.NewCount; v > max {
			max = v
		}
	}
	return max
}

func digits(n int) int {
	if n <= 0 {
		return 1
	}
	return len(strconv.Itoa(n))
}
