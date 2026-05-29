package bubbletea

import (
	"testing"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
)

// Compile-time check that *UI satisfies ui.UI. Failure here means an
// interface method was renamed or removed and v2 hasn't kept up.
var _ ui.UI = (*UI)(nil)

// And event.Sink — implied by ui.UI but worth pinning explicitly so a
// future refactor that pulls Sink off UI breaks loudly.
var _ event.Sink = (*UI)(nil)

func TestNew(t *testing.T) {
	u := New("/tmp/evva-v2-test-home")
	if u == nil {
		t.Fatal("New returned nil")
	}
	if u.program == nil {
		t.Fatal("program not initialised")
	}
	if u.model == nil {
		t.Fatal("model not initialised")
	}
}

// TestRegisteredAsBubbletea pins the side effect of register.go: importing
// this package must register the "bubbletea" UI so `evva -tui bubbletea`
// resolves. The factory must build a non-nil ui.UI.
func TestRegisteredAsBubbletea(t *testing.T) {
	factory, ok := ui.Lookup("bubbletea")
	if !ok {
		t.Fatal(`ui.Lookup("bubbletea") = _, false; register.go init() did not run`)
	}
	if got := factory("/tmp/evva-v2-test-home"); got == nil {
		t.Fatal("bubbletea factory returned a nil ui.UI")
	}
}

// NOTE: there's no Emit-before-Run test. tea.Program.Send blocks on an
// unbuffered channel until Run() starts the read loop, so the
// "pathological" case of emitting before Run can't be exercised from a
// unit test without spinning a goroutine. In real usage Emit is only
// called from the agent goroutine after the host has wired
// New → Attach → Run, so the window doesn't exist.
