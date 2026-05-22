package shell

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

// fakeBgHost is a minimal BgTaskHost for testing Bash run_in_background
// without spinning up a real ToolState. Captures the terminal snapshot
// the bg goroutine delivers so tests can assert on it.
type fakeBgHost struct {
	store     *BgTaskStore
	ctx       context.Context
	agentID   string
	mu        sync.Mutex
	delivered BgTaskSnapshot
	done      chan struct{}
}

func newFakeBgHost(ctx context.Context) *fakeBgHost {
	return &fakeBgHost{
		store:   NewBgTaskStore(),
		ctx:     ctx,
		agentID: "test-agent",
		done:    make(chan struct{}, 1),
	}
}

func (h *fakeBgHost) BgTaskStore() *BgTaskStore   { return h.store }
func (h *fakeBgHost) RootCtx() context.Context    { return h.ctx }
func (h *fakeBgHost) AgentID() string             { return h.agentID }
func (h *fakeBgHost) NotifyBgResult(s BgTaskSnapshot) {
	h.mu.Lock()
	h.delivered = s
	h.mu.Unlock()
	select {
	case h.done <- struct{}{}:
	default:
	}
}

func nopLogger() *slog.Logger { return tools.NopLogger() }

func TestBash_RunInBackground_HappyPath(t *testing.T) {
	host := newFakeBgHost(context.Background())
	tool := NewBashWithHost("", host)

	res, err := tool.Execute(context.Background(), nopLogger(), json.RawMessage(`{"command":"echo hi","run_in_background":true}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %q", res.Content)
	}
	if !strings.Contains(res.Content, "started in background") {
		t.Errorf("expected ack message; got %q", res.Content)
	}

	select {
	case <-host.done:
	case <-time.After(3 * time.Second):
		t.Fatal("bg goroutine did not deliver within 3s")
	}

	host.mu.Lock()
	got := host.delivered
	host.mu.Unlock()
	if got.Status != BgCompleted {
		t.Errorf("status: got %q want completed", got.Status)
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code: got %d want 0", got.ExitCode)
	}
	if !strings.Contains(got.Output, "hi") {
		t.Errorf("output: got %q want contains hi", got.Output)
	}
}

func TestBash_RunInBackground_FailureCapturesExit(t *testing.T) {
	host := newFakeBgHost(context.Background())
	tool := NewBashWithHost("", host)

	_, err := tool.Execute(context.Background(), nopLogger(), json.RawMessage(`{"command":"exit 7","run_in_background":true}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case <-host.done:
	case <-time.After(3 * time.Second):
		t.Fatal("bg goroutine did not deliver")
	}

	host.mu.Lock()
	got := host.delivered
	host.mu.Unlock()
	if got.Status != BgFailed {
		t.Errorf("status: got %q want failed", got.Status)
	}
	if got.ExitCode != 7 {
		t.Errorf("exit code: got %d want 7", got.ExitCode)
	}
}

func TestBash_RunInBackground_StopKills(t *testing.T) {
	host := newFakeBgHost(context.Background())
	tool := NewBashWithHost("", host)

	out, err := tool.Execute(context.Background(), nopLogger(), json.RawMessage(`{"command":"sleep 30","run_in_background":true}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Extract the task id from the ack message.
	parts := strings.Fields(out.Content)
	if len(parts) < 2 {
		t.Fatalf("ack message too short: %q", out.Content)
	}
	taskID := parts[1]

	// Let the goroutine actually spawn before we Stop it.
	time.Sleep(100 * time.Millisecond)

	if _, ok := host.store.Stop(taskID); !ok {
		t.Fatal("Stop should return ok=true for a running task")
	}

	select {
	case <-host.done:
	case <-time.After(5 * time.Second):
		t.Fatal("bg goroutine did not deliver after Stop")
	}

	host.mu.Lock()
	got := host.delivered
	host.mu.Unlock()
	if got.Status != BgKilled {
		t.Errorf("status: got %q want killed", got.Status)
	}
}

func TestBash_RunInBackground_RequiresHost(t *testing.T) {
	tool := NewBash("")
	res, err := tool.Execute(context.Background(), nopLogger(), json.RawMessage(`{"command":"echo","run_in_background":true}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no host")
	}
	if !strings.Contains(res.Content, "not available") {
		t.Errorf("expected clear error; got %q", res.Content)
	}
}
