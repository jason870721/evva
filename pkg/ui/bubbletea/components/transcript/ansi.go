package transcript

import "regexp"

// ansiRe matches CSI and OSC escape sequences emitted by lipgloss
// styling. Used by stripANSI to recover plain text for PlainText().
//
// Coverage:
//   - `\x1b[...m`     (SGR — colors, bold, italic)
//   - `\x1b[...K`     (erase in line)
//   - `\x1b[...H`     (cursor home; defensive — shouldn't appear in
//                      transcript content but stripped if it does)
//   - `\x1b]...\x07`  (OSC sequences ending in BEL — covers OSC52
//                      clipboard writes if they ever leak in)
//
// The pattern is intentionally permissive — over-stripping a stray
// ESC byte is preferable to leaving a half-cooked escape in copied
// text.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07`)

// stripANSI removes ANSI escape sequences from s. Pure function;
// safe to call on any string. Used by Block.PlainText to produce
// copy-safe / search-safe text.
func stripANSI(s string) string {
	if s == "" {
		return s
	}
	return ansiRe.ReplaceAllString(s, "")
}
