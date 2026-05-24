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
//   - Clipboard write: any tea.Cmd can produce a fresh clipboard
//     payload by returning the Copy cmd. Used by yank mode to
//     copy a Block.PlainText() to the system clipboard.
//
// Copy tries two backends in order:
//
//  1. The OS-native clipboard via atotto/clipboard (pbcopy on
//     macOS, xclip/xsel on Linux, the Windows clipboard API
//     otherwise). Most reliable for local sessions — survives
//     terminal configs that block OSC52.
//
//  2. The OSC52 terminal escape sequence written to stderr. Works
//     over SSH where pbcopy isn't available, and in terminals that
//     expose clipboard access via escapes (iTerm2, kitty, WezTerm,
//     Alacritty, Ghostty, tmux with `set-clipboard on`).
//
// The two paths together cover the realistic terminal landscape;
// the result message carries the Method that actually succeeded so
// the user can tell which backend their session is using.
package mouse

import (
	"encoding/base64"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/atotto/clipboard"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
)

// osc52MaxPayload is the conservative upper bound on a single OSC52
// payload before terminals start truncating. 100 KB is well below
// the documented kitty/iTerm2 limits but already much bigger than
// any code block a user is likely to copy from a TUI transcript.
const osc52MaxPayload = 100 * 1024

// Copy returns a tea.Cmd that writes s to the system clipboard.
// Order of attempts:
//   1. atotto/clipboard.WriteAll — invokes pbcopy / xclip / etc.
//      Synchronous; returns an error when the helper isn't found
//      (typical over SSH).
//   2. OSC52 escape on stderr — base64-encoded, wrapped in the
//      standard `\x1b]52;c;<b64>\x07` sequence.
//
// On success the message reports the size in bytes and Method =
// "native" or "osc52". On failure (both backends rejected the
// payload, or it was empty / oversized) the message has OK=false
// and a descriptive Err.
//
// We write OSC52 to stderr because stdout is multiplexed with the
// bubbletea alt-screen renderer — emitting our own escape there
// can corrupt the redraw buffer. Stderr always reaches the terminal.
//
// The work happens inside the Cmd (not inline) so it executes after
// Update returns. Inline writes from Update can race with the
// renderer; the Cmd path serialises everything through the main
// loop.
func Copy(s string) tea.Cmd {
	return func() tea.Msg {
		if s == "" {
			return events.ClipboardMsg{OK: false, Err: errEmptyPayload}
		}
		// Native backend first. Works locally; preserves the
		// clipboard even after evva exits.
		if !clipboard.Unsupported {
			if err := clipboard.WriteAll(s); err == nil {
				return events.ClipboardMsg{OK: true, Size: len(s), Method: "native"}
			}
		}
		// OSC52 fallback. Skip on oversized payloads — most
		// terminals silently truncate above ~100 KB and that's
		// worse than a clean failure.
		if len(s) > osc52MaxPayload {
			return events.ClipboardMsg{OK: false, Size: len(s), Err: errPayloadTooLarge}
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(s))
		if _, err := fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", b64); err != nil {
			return events.ClipboardMsg{OK: false, Err: err}
		}
		return events.ClipboardMsg{OK: true, Size: len(s), Method: "osc52"}
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
