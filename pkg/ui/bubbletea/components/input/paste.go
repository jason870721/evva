package input

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// pastePlaceholderRe matches the compact stand-in inserted into the
// textarea when the user pastes a multi-line or large block. Submit
// expands these back to their stored content before the prompt
// reaches the agent.
var pastePlaceholderRe = regexp.MustCompile(`\[- paste total \d+ characters -\]`)

// pasteCompactThreshold is the size above which a single-line paste
// gets a placeholder instead of being inserted verbatim. Below this
// users usually see what they pasted, which is what they want.
const pasteCompactThreshold = 200

// shouldCompactPaste reports whether content should be shown as a
// compact placeholder in the input box. Multi-line content always
// compacts; short single-line pastes pass through as plain text.
func shouldCompactPaste(s string) bool {
	if strings.ContainsRune(s, '\n') {
		return true
	}
	return len(s) > pasteCompactThreshold
}

// formatPlaceholder returns the placeholder string for a paste of
// the given size. Exposed as a function (not a const) so tests can
// match the regex against the formatted output.
func formatPlaceholder(size int) string {
	return fmt.Sprintf("[- paste total %d characters -]", size)
}

// expandForAgent walks text in order and replaces each compact
// placeholder with the corresponding stored paste content. Extra
// placeholders past the buffer length stay literal (defensive);
// extra stored pastes past the placeholder count are dropped (the
// user deleted them from the input).
//
// This is the agent-facing expansion: raw content only, no boundary
// markers. The model should see exactly what the user pasted,
// byte-for-byte.
func expandForAgent(text string, pasted []string) string {
	if len(pasted) == 0 {
		return text
	}
	i := 0
	return pastePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		if i >= len(pasted) {
			return match
		}
		s := pasted[i]
		i++
		return s
	})
}

// expandForView is the transcript-facing expansion: paste content
// is sandwiched between visible head/tail markers so the user can
// scroll the scrollback and confirm the whole payload made it in.
// Without the markers a long paste blends into surrounding typed
// prose and the user has no anchor for "where does the paste end".
func expandForView(text string, pasted []string, th *theme.Theme) string {
	if len(pasted) == 0 {
		return text
	}
	i := 0
	return pastePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		if i >= len(pasted) {
			return match
		}
		s := pasted[i]
		i++
		head := th.PasteChip.Render(fmt.Sprintf("╔═ PASTE %d chars ═╗", len(s)))
		tail := th.PasteChip.Render("╚════════════════════╝")
		return "\n" + head + "\n" + s + "\n" + tail + "\n"
	})
}
