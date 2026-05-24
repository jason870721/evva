package theme

import (
	"time"

	"github.com/charmbracelet/lipgloss"
)

// SpinnerFrames is the braille-dot rotation used for any "ing" status
// (thinking, executing, draining, compacting). One frame per
// SpinnerInterval; consumers wrap via SpinnerFrame(i).
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerInterval is the wall-clock cadence at which the TUI advances
// the spinner. 100 ms is light enough for a TUI redraw budget and the
// rate the user requested in v1.
const SpinnerInterval = 100 * time.Millisecond

// SpinnerFrame returns one frame of the rotation. The argument can be
// any non-negative tick counter; the function handles wrapping.
func SpinnerFrame(i int) string {
	if i < 0 {
		i = -i
	}
	return SpinnerFrames[i%len(SpinnerFrames)]
}

// Glyph pairs a status string (task or subagent) with the symbol and
// foreground color the TUI uses to render it. One source of truth for
// panels, transcript snapshots, and any future widget that wants
// consistent lifecycle vocabulary.
type Glyph struct {
	Symbol string
	Color  lipgloss.Color
}

// Glyph returns the symbol + color for a status string. Unknown
// statuses get a neutral dim "·" — visible but not screaming for
// attention.
func (t *Theme) Glyph(status string) Glyph {
	if g, ok := t.glyphs[status]; ok {
		return g
	}
	return Glyph{Symbol: "·", Color: dim}
}

// SpinnerStyle returns the spinner style for an "ing" status. The
// second return is false when the status is terminal (idle / done /
// crushed) so the caller can fall back to the static Glyph table.
func (t *Theme) SpinnerStyle(status string) (lipgloss.Style, bool) {
	st, ok := t.activeSpinners[status]
	return st, ok
}
