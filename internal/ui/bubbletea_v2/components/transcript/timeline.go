package transcript

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
)

// Timeline gutter helpers — port of v1's applyLineGutter /
// applyToolGutter / renderUserPrompt. Each helper takes the styled
// content of a block and returns it with the appropriate left-edge
// glyph prefix (and word-wrap to the available column).
//
// Width semantics: the helpers reserve columns for the gutter glyph
// (2 cols for `│ `, 3 for `├─ `) so the wrapped body never extends
// past the transcript column. wrap is via wrapForWidth — see wrap.go
// for the PreserveSpace=true rationale.
//
// Gutter glyphs are styled with theme.Timeline (muted grey) so they
// sit back as chrome, not as content.

// gutterStyle picks the muted-or-accent style depending on whether
// the block is the yank-mode focused one (M8). Centralised here so
// every gutter helper shares the same swap.
func gutterStyle(th *theme.Theme, accent bool) lipgloss.Style {
	if accent {
		return th.TimelineAccent
	}
	return th.Timeline
}

// applyLineGutter prepends `│ ` to every line of s. Empty input
// emits a single pipe line so a blank block still occupies one row
// of the timeline. accent=true switches the glyph to the cyan-bold
// variant used by yank-mode to highlight the focused block.
func applyLineGutter(s string, width int, th *theme.Theme, accent bool) string {
	g := gutterStyle(th, accent)
	if s == "" {
		return g.Render("│")
	}
	pipe := g.Render("│") + " "
	wrapped := wrapForWidth(s, width-2)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = pipe + line
	}
	return strings.Join(lines, "\n")
}

// applyToolGutter prefixes the first line with `├─ ` (branch-out
// connector) and subsequent lines with `│  ` so the body sits in
// line with the connector's arm. Content wraps to (width-3) — gutter
// is 3 cols here. accent=true uses the cyan-bold variant.
func applyToolGutter(s string, width int, th *theme.Theme, accent bool) string {
	g := gutterStyle(th, accent)
	if s == "" {
		return g.Render("├─")
	}
	branch := g.Render("├─") + " "
	pipe := g.Render("│") + "  "
	wrapped := wrapForWidth(s, width-3)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = branch + line
		} else {
			lines[i] = pipe + line
		}
	}
	return strings.Join(lines, "\n")
}

// renderUserPromptHeader draws a HUD scanline separator + diamond
// bullet so a user prompt reads as a hard break between turns. The
// body is appended below the separator and word-wrapped to the
// transcript column.
//
// Width < 20 is clamped — too narrow to draw a useful scanline.
func renderUserPromptHeader(body string, width int, th *theme.Theme) string {
	if width < 20 {
		width = 20
	}
	sep := strings.Repeat("═", width-2)
	return th.TimelineCut.Render("◆═"+sep) + "\n" + wrapForWidth(body, width)
}

// interBlockSpacer returns the row drawn BETWEEN two adjacent
// blocks. Most transitions emit a blank `│` to keep the timeline
// visually continuous; transitions where the next block draws its
// own separator (e.g. KindUserPrompt) emit "" so the renderer
// doesn't double up.
//
// Banner → next is also unprefixed — the banner sits outside the
// timeline column.
func interBlockSpacer(cur, next Kind, th *theme.Theme) string {
	if next == KindUserPrompt {
		return ""
	}
	if cur == KindBanner {
		return ""
	}
	return th.Timeline.Render("│")
}
