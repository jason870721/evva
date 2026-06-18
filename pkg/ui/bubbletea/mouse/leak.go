package mouse

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

// sgrLeakBodyRe matches the tail of an SGR mouse sequence (\x1b[<b;x;yM)
// that bubbletea v1.3.10's input parser mis-emits as a KeyRunes event.
//
// Under rapid wheel scrolling the terminal floods stdin with back-to-back
// \x1b[<b;x;yM sequences; bubbletea reads them into a fixed 256-byte buffer
// (readAnsiInputs in key.go). When that boundary splits a sequence before
// its terminating M/m, detectOneMsg has no "truncated mouse → wait for more"
// path: the leading "\x1b[" is consumed as Alt+[ (the single-rune-after-ESC
// rule) and the leftover "<b;x;y..M" resurfaces on the next read as plain
// runes. Both then fall through to the textarea and get typed into the
// prompt. The "\x1b[" always splits off as the Alt+[ head, so the body
// keeps its leading "<"; requiring it (and anchoring) makes false positives
// on real typing effectively impossible.
var sgrLeakBodyRe = regexp.MustCompile(`^\[?<\d{1,5};\d{1,5};\d{1,5}[Mm]$`)

// IsLeakedMouseSequence reports whether m is a fragment of an SGR mouse
// sequence that leaked into keyboard input (see sgrLeakBodyRe). recentWheel
// must be true when a wheel event fired within the dedup window; the
// ambiguous "alt+[" head is only treated as a leak in that window so a
// deliberate Alt+[ outside scrolling still reaches the application.
func IsLeakedMouseSequence(m tea.KeyMsg, recentWheel bool) bool {
	if m.Type != tea.KeyRunes {
		return false
	}
	s := string(m.Runes)
	if !m.Alt && sgrLeakBodyRe.MatchString(s) {
		return true // the "<b;x;yM" tail — unmistakable, drop unconditionally
	}
	if recentWheel && m.Alt && s == "[" {
		return true // the "\x1b[" head, mis-read as Alt+[
	}
	return false
}
