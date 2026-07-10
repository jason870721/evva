package bubbletea

import (
	"errors"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// Emit before Run must buffer, not forward: tea.Program.Send blocks until
// Run() starts the read loop, and the documented wiring constructs the
// agent (whose New can emit — the workflow board's SetSession notify does)
// before Run. Forwarding here deadlocked startup whenever
// enable_dynamic_workflow was on: evva hung before drawing the TUI with an
// empty agent log.
func TestEmitBeforeRunBuffers(t *testing.T) {
	u := New(t.TempDir())
	done := make(chan struct{})
	go func() {
		u.Emit(event.Event{Kind: event.KindStoreUpdate})
		u.Emit(event.Event{Kind: event.KindStoreUpdate})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked before Run — startup deadlock regression")
	}
	if got := u.emits.Len(); got != 2 {
		t.Fatalf("queued = %d events, want 2 buffered", got)
	}
}

// Emit must never block even with the pump live and the program loop dead
// — the shape of the dynamic-workflow dispatch deadlock: the sweep emitted
// daemon/board changes through an inline Program.Send while holding the
// engine mutex that the TUI's per-frame workflowDaemon.Snapshot pull
// needs, so the agent and the Update loop waited on each other forever
// (session edefa044: first wf_task_create with a worker froze the TUI).
// Run() is never called here, so the pump's Send wedges exactly like a
// busy Update loop; every Emit must still return.
func TestEmitNeverBlocksWhileLoopStalled(t *testing.T) {
	u := New(t.TempDir())
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
	// The pump goroutine stays wedged in Send until the process exits;
	// that IS the simulated condition, not a fixture leak.
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
	if !isCleanExit(fmt.Errorf("wrapped: %w", tea.ErrInterrupted)) {
		t.Fatal("a wrapped interrupt should still be a clean exit")
	}
	if isCleanExit(errors.New("boom")) {
		t.Fatal("a real error must NOT be treated as a clean exit")
	}
}
