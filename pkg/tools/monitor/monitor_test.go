package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

type fakeMonitorHost struct {
	store *MonitorTaskStore
	queue *MonitorEventQueue
	ctx   context.Context
	mu    sync.Mutex
	lines []string
	done  chan struct{}
}

func newFakeHost(ctx context.Context) *fakeMonitorHost {
	return &fakeMonitorHost{
		store: NewMonitorTaskStore(),
		queue: NewMonitorEventQueue(),
		ctx:   ctx,
		done:  make(chan struct{}, 1),
	}
}

func (h *fakeMonitorHost) MonitorTaskStore() *MonitorTaskStore { return h.store }
func (h *fakeMonitorHost) MonitorEventQueue() *MonitorEventQueue {
	return h.queue
}
func (h *fakeMonitorHost) RootCtx() context.Context { return h.ctx }
func (h *fakeMonitorHost) AgentID() string          { return "test-agent" }
func (h *fakeMonitorHost) NotifyMonitorEvent(ev MonitorEvent) {
	h.queue.Enqueue(ev)
	h.mu.Lock()
	if ev.Closing {
		select {
		case h.done <- struct{}{}:
		default:
		}
	} else {
		h.lines = append(h.lines, ev.Line)
	}
	h.mu.Unlock()
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

	select {
	case <-host.done:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not close within 3s")
	}

	host.mu.Lock()
	got := append([]string(nil), host.lines...)
	host.mu.Unlock()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("lines: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line[%d]: got %q want %q", i, got[i], want[i])
		}
	}

	// Status should be Stopped after clean exit.
	snaps := host.store.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("store snapshot count: got %d want 1", len(snaps))
	}
	if snaps[0].Status != Stopped {
		t.Errorf("status: got %q want stopped", snaps[0].Status)
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

func TestMonitorEventQueue_Drain(t *testing.T) {
	q := NewMonitorEventQueue()
	if q.HasPending() {
		t.Error("fresh queue should not be pending")
	}
	q.Enqueue(MonitorEvent{MonitorID: "m1", Line: "one"})
	q.Enqueue(MonitorEvent{MonitorID: "m1", Line: "two"})
	if !q.HasPending() {
		t.Error("queue should report pending after Enqueue")
	}
	drained := q.Drain()
	if len(drained) != 2 {
		t.Fatalf("Drain count: got %d want 2", len(drained))
	}
	if drained[0].Line != "one" || drained[1].Line != "two" {
		t.Errorf("drained order wrong: %+v", drained)
	}
	if q.HasPending() {
		t.Error("Drain should clear pending")
	}
}

func TestGenerateID_MonitorPrefix(t *testing.T) {
	id := GenerateID()
	if len(id) != 9 || id[0] != 'm' {
		t.Errorf("monitor ID shape wrong: %q", id)
	}
}
