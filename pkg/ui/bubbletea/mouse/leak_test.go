package mouse

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIsLeakedMouseSequence(t *testing.T) {
	runes := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	altRunes := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: true}
	}

	cases := []struct {
		name        string
		msg         tea.KeyMsg
		recentWheel bool
		want        bool
	}{
		// The SGR body tail leaks the same way whether or not the window
		// is still warm, so it is dropped unconditionally.
		{"sgr body, wheel warm", runes("<65;190;49M"), true, true},
		{"sgr body, wheel cold", runes("<65;190;49M"), false, true},
		{"sgr release (m)", runes("<64;12;3m"), true, true},
		{"sgr body with leading bracket", runes("[<65;190;49M"), false, true},

		// The Alt+[ head is ambiguous with a deliberate keypress, so it is
		// only treated as a leak right after a wheel event.
		{"alt+[ head, wheel warm", altRunes("["), true, true},
		{"alt+[ head, wheel cold", altRunes("["), false, false},

		// Real input must never be swallowed.
		{"plain letter", runes("a"), true, false},
		{"lone angle bracket", runes("<"), true, false},
		{"word", runes("hello"), true, false},
		{"semicolon triple without <", runes("1;2;3"), true, false},
		{"plain bracket no alt", runes("["), true, false},
		{"non-rune key", tea.KeyMsg{Type: tea.KeyEnter}, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLeakedMouseSequence(tc.msg, tc.recentWheel); got != tc.want {
				t.Errorf("IsLeakedMouseSequence(%q, alt=%v, recentWheel=%v) = %v, want %v",
					string(tc.msg.Runes), tc.msg.Alt, tc.recentWheel, got, tc.want)
			}
		})
	}
}
