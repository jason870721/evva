package transcript

import "github.com/muesli/reflow/wrap"

// wrapForWidth wraps s to w columns while preserving every printable
// rune — including leading whitespace on lines that resulted from a
// forced break. Critical for pasted code: dropping the first-column
// indent on wrapped continuations reads as "my prompt got cut",
// which was the bug that prompted v1's implementation.
//
// We deliberately do NOT pre-pass through wordwrap: wordwrap resets
// its space buffer on every internal newline, which silently strips
// leading indentation when a single line is wider than the column
// (long URL, minified JSON, etc.). `wrap.String` alone — with
// PreserveSpace enabled — keeps all bytes while honoring existing
// `\n` breaks. The cost is the occasional mid-word break on a long
// unbroken token; we accept it because losing content is worse than
// breaking a URL across two lines.
//
// w < 5 is treated as "don't wrap" — too narrow to be useful.
func wrapForWidth(s string, w int) string {
	if w < 5 {
		return s
	}
	ww := wrap.NewWriter(w)
	ww.PreserveSpace = true
	_, _ = ww.Write([]byte(s))
	return ww.String()
}
