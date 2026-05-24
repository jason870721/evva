// Package events declares the tea.Msg types the v2 TUI passes through
// its Update loop. Keeping them in their own package prevents import
// cycles between app and components (both need to recognise the same
// message types but neither owns the other).
package events

import (
	"github.com/johnny1110/evva/pkg/event"
)

// AgentEventMsg wraps an event.Event for delivery into bubbletea's
// Update loop. The package-level UI's Emit method wraps each event and
// calls tea.Program.Send so the message lands on the bubbletea main
// goroutine — model state is only mutated there.
type AgentEventMsg struct {
	Event event.Event
}

// QuitMsg signals the user has decided to exit (Ctrl+C or context
// cancel). Separate from tea.Quit so Update can run cleanup first
// (cancel in-flight agent run, drain logs) before returning tea.Quit.
type QuitMsg struct{}

// SpinnerTickMsg drives the braille-dot spinner animation. Update
// increments the frame counter and schedules the next tick; the status
// bar reads the frame index when composing its state pill.
type SpinnerTickMsg struct{}

// RunDoneMsg is the bubbletea-side notification that a Controller.Run
// or Continue call has returned. It carries the error (if any) so
// Update can re-enable input, surface failures, and decide whether to
// prompt the user to Continue on iter-limit.
type RunDoneMsg struct {
	Err error
}

// ClipboardMsg is the result of a clipboard-copy attempt.
// Dispatched by mouse.Copy's returned tea.Cmd after both the
// native OS-clipboard write (pbcopy/xclip/etc.) and the OSC52
// terminal escape have been attempted. The App reads it to flash a
// transient status hint ("copied N chars" or "clipboard: …").
//
// Method records which path actually succeeded so a future-debugging
// user can tell whether their terminal is honoring OSC52 or the
// payload landed via a subprocess.
type ClipboardMsg struct {
	OK     bool
	Size   int    // bytes written when OK; informational when !OK
	Method string // "native" | "osc52" | "" on failure
	Err    error  // populated when OK=false
}
