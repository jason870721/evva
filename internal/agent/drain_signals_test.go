package agent

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/toolset"
	monitorpkg "github.com/johnny1110/evva/pkg/tools/monitor"
	shellpkg "github.com/johnny1110/evva/pkg/tools/shell"
)

// newDrainTestAgent builds an agent shell sufficient for drain tests.
// We bypass agent.New because we don't need a real LLM client, tool
// registry, or signal pump — just the session + toolState + logger.
func newDrainTestAgent() *Agent {
	a := &Agent{
		ID:        "drain-test-agent",
		logger:    slog.Default(),
		session:   session.New(),
		toolState: toolset.NewToolState(),
	}
	return a
}

func TestDrainBackgroundTaskResults_FoldsSnapshotIntoSession(t *testing.T) {
	a := newDrainTestAgent()
	store := a.toolState.BgTaskStore()

	store.Add(shellpkg.BgTaskSnapshot{ID: "b1", Command: "echo hi", Status: shellpkg.BgRunning, StartedAt: time.Now()}, func() {})
	store.Complete("b1", shellpkg.BgCompleted, 0, "hi\n")
	store.Add(shellpkg.BgTaskSnapshot{ID: "b2", Command: "exit 1", Status: shellpkg.BgRunning, StartedAt: time.Now()}, func() {})
	store.Complete("b2", shellpkg.BgFailed, 1, "boom")

	drained := a.drainBackgroundTaskResults()
	if !drained {
		t.Fatal("drainBackgroundTaskResults should return true when results present")
	}

	msgs := a.session.Messages
	if len(msgs) != 1 {
		t.Fatalf("session message count: got %d want 1", len(msgs))
	}
	body := msgs[0].Content
	if !strings.Contains(body, "task b1 completed") {
		t.Errorf("missing b1 reminder: %q", body)
	}
	if !strings.Contains(body, "task b2 failed") {
		t.Errorf("missing b2 reminder: %q", body)
	}
	if !strings.Contains(body, "exit code 0") {
		t.Errorf("missing exit code line for completed: %q", body)
	}
	if !strings.Contains(body, "exit code 1") {
		t.Errorf("missing exit code line for failed: %q", body)
	}
	// Store should be empty after drain.
	if a.toolState.BgTaskStore().HasPending() {
		t.Error("HasPending should be false after drain")
	}
}

func TestDrainBackgroundTaskResults_NoopWhenEmpty(t *testing.T) {
	a := newDrainTestAgent()
	if a.drainBackgroundTaskResults() {
		t.Error("drain on empty store should return false")
	}
	if len(a.session.Messages) != 0 {
		t.Error("empty drain must not append to session")
	}
}

func TestDrainMonitorEvents_FoldsLinesAndClosing(t *testing.T) {
	a := newDrainTestAgent()
	queue := a.toolState.MonitorEventQueue()
	queue.Enqueue(monitorpkg.MonitorEvent{MonitorID: "m1", Line: "log line one"})
	queue.Enqueue(monitorpkg.MonitorEvent{MonitorID: "m1", Line: "log line two"})
	queue.Enqueue(monitorpkg.MonitorEvent{MonitorID: "m1", Closing: true, At: time.Now()})

	if !a.drainMonitorEvents() {
		t.Fatal("drain should report true when events present")
	}
	msgs := a.session.Messages
	if len(msgs) != 1 {
		t.Fatalf("session messages: got %d want 1", len(msgs))
	}
	body := msgs[0].Content
	if !strings.Contains(body, "log line one") || !strings.Contains(body, "log line two") {
		t.Errorf("missing streamed lines: %q", body)
	}
	if !strings.Contains(body, "monitor m1 closed") {
		t.Errorf("missing closing reminder: %q", body)
	}
}

func TestHasPendingSignals_TriggersWhenAny(t *testing.T) {
	a := newDrainTestAgent()
	if a.hasPendingSignals() {
		t.Error("fresh agent should not report pending signals")
	}
	a.toolState.BgTaskStore().Add(shellpkg.BgTaskSnapshot{ID: "b1", Status: shellpkg.BgRunning, StartedAt: time.Now()}, func() {})
	if a.hasPendingSignals() {
		t.Error("running task should not register as pending")
	}
	a.toolState.BgTaskStore().Complete("b1", shellpkg.BgCompleted, 0, "")
	if !a.hasPendingSignals() {
		t.Error("completed task should make hasPendingSignals return true")
	}
}
