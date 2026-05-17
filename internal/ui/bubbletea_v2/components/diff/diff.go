// Package diff renders a *fs.FileDiff (the structured metadata
// write_file / edit_file attach to tools.Result) as a multi-line
// git-style string: two columns of line numbers + a sign character
// (+/-/space) + the line text.
//
// Add rows render white text on solid acid-green; remove rows render
// white text on solid glitch-red — the M2 "more pro feeling" diff the
// user asked for. Context rows stay un-filled so the eye lands on
// additions and removals first.
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
// each + / - row is padded to so its background tint reads as a solid
// block stretching across the transcript column. width <= 0 disables
// the fill — useful in tests that don't care about column alignment.
func Render(d *fs.FileDiff, th *theme.Theme, width int) string {
	if d == nil || th == nil {
		return ""
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
			oldCol := blankIfZero(ln.Old)
			newCol := blankIfZero(ln.New)
			text := fmt.Sprintf("%4s %4s %s %s", oldCol, newCol, signFor(ln.Kind), ln.Text)
			var row string
			switch ln.Kind {
			case fs.LineAdd:
				row = fill(th.DiffAdd, text, width)
			case fs.LineRemove:
				row = fill(th.DiffRemove, text, width)
			default:
				row = th.DiffContext.Render(text)
			}
			b.WriteString(row)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// fill renders s through style, padding to width so the Background
// extends across the row. width <= 0 falls back to natural rendering
// (no fill).
func fill(style lipgloss.Style, s string, width int) string {
	if width <= 0 {
		return style.Render(s)
	}
	return style.Width(width).Render(s)
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

func blankIfZero(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
