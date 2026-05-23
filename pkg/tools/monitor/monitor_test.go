package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// fakeDaemonHost wires a DaemonHost backed by an in-memory DaemonState.
// The notify closure increments a counter and pushes onto done so tests
// can wait for events to flow.
type fakeDaemonHost struct {
	state *daemon.DaemonState
	ctx   context.Context
	mu    sync.Mutex
	lines []string
	done  chan struct{}
}

func newFakeHost(ctx context.Context) *fakeDaemonHost {
	h := &fakeDaemonHost{
		ctx:  ctx,
		done: make(chan struct{}, 16),
	}
	h.state = daemon.NewState(func() {
		select {
		case h.done <- struct{}{}:
		default:
		}
	})
	return h
}

func (h *fakeDaemonHost) DaemonState() *daemon.DaemonState { return h.state }
func (h *fakeDaemonHost) RootCtx() context.Context         { return h.ctx }
func (h *fakeDaemonHost) AgentID() string                  { return "test-agent" }

// pollEvents drains the daemon's signal queue and accumulates event lines
// until the deadline elapses or a closing event arrives. Returns the
// captured lines (in arrival order) and the final daemon status.
func pollEvents(t *testing.T, h *fakeDaemonHost, id string, timeout time.Duration) (lines []string, finalStatus daemon.DaemonStatus) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	closed := false
	var lastStatus daemon.DaemonStatus
	for {
		select {
		case <-h.done:
		case <-deadline.C:
			t.Fatalf("monitor %s did not close within %s (lines=%d, lastStatus=%q)", id, timeout, len(lines), lastStatus)
		}
		for _, sig := range h.state.DrainSignals() {
			if sig.IsEvent() {
				if sig.Event.Closing {
					closed = true
					continue
				}
				lines = append(lines, sig.Event.Line)
			}
			if sig.IsLifecycle() {
				lastStatus = sig.Lifecycle.Status
			}
		}
		if closed && daemon.IsTerminal(lastStatus) {
			return lines, lastStatus
		}
	}
}

func TestMonitor_StreamsStdoutLines(t *testing.T) {
	host := newFakeHost(context.Background())
	tool := NewMonitor(host)

	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{
		"command":"printf 'a\nb\nc\n'",
		"description":"test",
		"persistent":false,
		"timeout_ms":10000
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %q", res.Content)
	}
	id := extractMonitorID(t, res.Content)

	lines, status := pollEvents(t, host, id, 3*time.Second)

	want := []string{"a", "b", "c"}
	if len(lines) != len(want) {
		t.Fatalf("lines: got %v want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line[%d]: got %q want %q", i, lines[i], want[i])
		}
	}
	// Clean exit → Completed in daemon vocabulary.
	if status != daemon.StatusCompleted {
		t.Errorf("status: got %q want %q", status, daemon.StatusCompleted)
	}
}

func TestMonitor_RejectsEmptyCommand(t *testing.T) {
	host := newFakeHost(context.Background())
	tool := NewMonitor(host)
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{
		"command":"",
		"description":"x",
		"persistent":false,
		"timeout_ms":1000
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError for empty command")
	}
	if !strings.Contains(res.Content, "required") {
		t.Errorf("unexpected error message: %q", res.Content)
	}
}

func TestMonitorDaemon_NonZeroExitTransitionsToFailed(t *testing.T) {
	// A wrong command yields a non-zero shell exit. The monitor must
	// classify that as Failed (red chip in the TUI) — not Completed —
	// so the user sees the crash and the model's reminder reads "failed".
	host := newFakeHost(context.Background())
	tool := NewMonitor(host)

	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{
		"command":"echo bad-input; exit 127",
		"description":"wrong command",
		"persistent":false,
		"timeout_ms":5000
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %q", res.Content)
	}
	id := extractMonitorID(t, res.Content)

	_, status := pollEvents(t, host, id, 3*time.Second)
	if status != daemon.StatusFailed {
		t.Errorf("status: got %q want %q", status, daemon.StatusFailed)
	}
}

func TestMonitorDaemon_KillTransitionsToKilled(t *testing.T) {
	host := newFakeHost(context.Background())
	tool := NewMonitor(host)

	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{
		"command":"sleep 30",
		"description":"long sleep",
		"persistent":true,
		"timeout_ms":0
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	id := extractMonitorID(t, res.Content)

	// Let the goroutine actually spawn.
	time.Sleep(100 * time.Millisecond)

	if _, err := host.state.Stop(context.Background(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, status := pollEvents(t, host, id, 5*time.Second)
	if status != daemon.StatusKilled {
		t.Errorf("status: got %q want %q", status, daemon.StatusKilled)
	}
}

// extractMonitorID pulls the "m…" id out of the monitor ack message.
// Format: "Monitor m… started. ...".
func extractMonitorID(t *testing.T, ack string) string {
	t.Helper()
	parts := strings.Fields(ack)
	if len(parts) < 2 {
		t.Fatalf("ack too short: %q", ack)
	}
	id := parts[1]
	if !strings.HasPrefix(id, "m") {
		t.Fatalf("expected monitor id prefix 'm', got %q", id)
	}
	return id
}

func TestGenerateID_MonitorPrefix(t *testing.T) {
	id := daemon.GenerateID(daemon.KindMonitor)
	if len(id) != 9 || id[0] != 'm' {
		t.Errorf("monitor ID shape wrong: %q", id)
	}
}
