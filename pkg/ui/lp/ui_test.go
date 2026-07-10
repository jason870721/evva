package lp

import (
	"errors"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
)

// Compile-time checks that *UI satisfies the public contracts. Failure here
// means the ui.UI / event.Sink surface drifted and lp hasn't kept up.
var (
	_ ui.UI      = (*UI)(nil)
	_ event.Sink = (*UI)(nil)
)

func TestNew(t *testing.T) {
	u := New("/tmp/evva-lp-test-home")
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

// TestRegisteredAsLp pins the side effect of register.go: importing this
// package must register the "lp" UI so `evva -tui lp` resolves.
func TestRegisteredAsLp(t *testing.T) {
	factory, ok := ui.Lookup("lp")
	if !ok {
		t.Fatal(`ui.Lookup("lp") = _, false; register.go init() did not run`)
	}
	if got := factory("/tmp/evva-lp-test-home"); got == nil {
		t.Fatal("lp factory returned a nil ui.UI")
	}
}

// Emit must buffer before Run and never block after — lp used to forward
// straight into tea.Program.Send, which carried both the pre-Run startup
// deadlock (#55 patched only the NEON TUI) and the dynamic-workflow
// dispatch deadlock (emitters hold locks the render path reads back; see
// ui.EmitQueue). Run() is never called here, so the pump's Send wedges
// exactly like a busy Update loop; every Emit must still return.
func TestEmitNeverBlocks(t *testing.T) {
	u := New(t.TempDir())
	u.Emit(event.Event{Kind: event.KindStoreUpdate})
	if got := u.emits.Len(); got != 1 {
		t.Fatalf("queued = %d events before Run, want 1 buffered", got)
	}
	u.emits.Start()
	done := make(chan struct{})
	go func() {
		for range 64 {
			u.Emit(event.Event{Kind: event.KindStoreUpdate})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked while the program loop was stalled — TUI deadlock regression")
	}
}

// isCleanExit must treat a normal interrupt/kill as a clean quit — otherwise
// cmd/evva takes its os.Exit path on quit and skips agent Shutdown, orphaning
// MCP stdio subprocesses (a leaked docker container per launch).
func TestIsCleanExit(t *testing.T) {
	if !isCleanExit(nil) {
		t.Fatal("nil should be a clean exit")
	}
	if !isCleanExit(tea.ErrInterrupted) {
		t.Fatal("ErrInterrupted (SIGINT) should be a clean exit")
	}
	if !isCleanExit(tea.ErrProgramKilled) {
		t.Fatal("ErrProgramKilled should be a clean exit")
	}
	if !isCleanExit(fmt.Errorf("wrapped: %w", tea.ErrProgramKilled)) {
		t.Fatal("a wrapped kill should still be a clean exit")
	}
	if isCleanExit(errors.New("boom")) {
		t.Fatal("a real error must NOT be treated as a clean exit")
	}
}
