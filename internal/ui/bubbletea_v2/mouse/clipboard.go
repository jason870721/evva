// Package mouse owns mouse-capture wiring + clipboard integration.
//
// M8 introduces two cross-cutting capabilities:
//
//   - Wheel scroll: the bubbletea program now opts into
//     tea.WithMouseCellMotion (see ui.go), so the terminal hands
//     every wheel / drag event to the program. The App's MouseMsg
//     handler routes wheel up/down to the transcript viewport;
//     non-wheel mouse events are dropped (modal overlays don't use
//     them, drag-select still works via Shift/Alt-bypass in modern
//     terminals).
//
//   - OSC52 clipboard write: any tea.Cmd can produce a fresh
//     clipboard payload by returning the WriteOSC52 cmd. Used by
//     yank mode to copy a Block.PlainText() to the system clipboard
//     without an external library. Works in iTerm2, kitty, WezTerm,
//     Alacritty, Ghostty, and most modern terminals; broken on
//     Apple's Terminal.app by default (user must enable
//     "Allow Mouse Reporting" / use a different terminal).
package mouse

import (
	"encoding/base64"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/events"
)

// osc52MaxPayload is the conservative upper bound on a single OSC52
// payload before terminals start truncating. 100 KB is well below
// the documented kitty/iTerm2 limits but already much bigger than
// any code block a user is likely to copy from a TUI transcript.
const osc52MaxPayload = 100 * 1024

// WriteOSC52 returns a tea.Cmd that writes s to the system
// clipboard using the OSC52 escape sequence (`\x1b]52;c;<base64>\x07`).
// On success the returned message is ClipboardMsg{OK: true, Size: len(s)};
// on failure (write error, payload too large) it's ClipboardMsg{
// OK: false, Err: ...}.
//
// We write to stderr because stdout is multiplexed with the
// alt-screen renderer — emitting our own escape there can corrupt
// the redraw buffer. Stderr always reaches the terminal.
//
// The escape is wrapped by the runtime in a tea.Cmd rather than
// called inline so it executes after Update returns. Inline calls
// from Update can race with bubbletea's renderer; the Cmd path
// serialises everything through the main loop.
func WriteOSC52(s string) tea.Cmd {
	return func() tea.Msg {
		if s == "" {
			return events.ClipboardMsg{OK: false, Err: errEmptyPayload}
		}
		if len(s) > osc52MaxPayload {
			return events.ClipboardMsg{OK: false, Size: len(s), Err: errPayloadTooLarge}
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(s))
		if _, err := fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", b64); err != nil {
			return events.ClipboardMsg{OK: false, Err: err}
		}
		return events.ClipboardMsg{OK: true, Size: len(s)}
	}
}

// IsWheelEvent reports whether m is a mouse-wheel scroll event. The
// transcript viewport responds to wheel events; click / motion
// events are dropped by the App in normal focus (yank mode may
// intercept them in a future iteration).
func IsWheelEvent(m tea.MouseMsg) bool {
	return m.Button == tea.MouseButtonWheelUp ||
		m.Button == tea.MouseButtonWheelDown ||
		m.Button == tea.MouseButtonWheelLeft ||
		m.Button == tea.MouseButtonWheelRight
}

// Local sentinel errors. Not exported — callers read
// ClipboardMsg.OK and ignore Err details unless they want to log
// them.
var (
	errEmptyPayload    = clipboardError("empty payload")
	errPayloadTooLarge = clipboardError("payload too large for OSC52")
)

type clipboardError string

func (e clipboardError) Error() string { return string(e) }
