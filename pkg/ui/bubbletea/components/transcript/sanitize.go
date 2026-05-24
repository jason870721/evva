package transcript

import "strings"

// sanitizeForTranscript scrubs control bytes that would corrupt the
// terminal when written through to the renderer. The dangerous ones
// are `\r` (cursor → column 0, overwriting prior cells), `\f` (form
// feed, some terminals clear screen), `\b` (backspace), and `\x07`
// (BEL).
//
// Embedded `\r` was the root cause of v1's "TUI frozen after tasks
// created" report: a pasted user prompt contained `python f  \rile`,
// and every subsequent redraw replayed that `\r` and clobbered the
// visible row. The Update goroutine was fine — the screen just
// looked stuck.
//
// We preserve `\n`, `\t`, and ESC (0x1b — the leader for ANSI/CSI
// escapes that lipgloss emits for styling).
func sanitizeForTranscript(s string) string {
	if !strings.ContainsAny(s, "\r\b\f\x07") {
		return s
	}
	// Normalize CRLF → LF first so we don't lose a legitimate
	// newline when stripping the leading \r.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\r', '\b', '\f', '\x07':
			// Drop — these wreck terminal layout from inside a
			// scrollback buffer the renderer can't escape from.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
