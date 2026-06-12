package agent

import (
	"log/slog"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools/todo"
)

// newClearTestAgent builds the minimal Agent ClearSession touches: a live
// session, a toolState (for the todo store), and a logger. Bypasses agent.New
// for the same reason compact_test does — no LLM/toolset wiring needed.
func newClearTestAgent() *Agent {
	return &Agent{
		ID:               "boot-id",
		logger:           slog.Default(),
		session:          session.New(),
		toolState:        toolset.NewToolState(),
		sessionCreatedAt: time.Now(),
	}
}

// TestClearSession_FreshState locks the contract: empty history, zeroed
// usage, cleared todos, a NEW session id, zeroed sessionCreatedAt, and the
// SessionStart latch re-armed with source "clear".
func TestClearSession_FreshState(t *testing.T) {
	a := newClearTestAgent()
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	a.session.RecordTurn(llm.Usage{InputTokens: 100, OutputTokens: 50})
	a.toolState.TodoStore().Replace([]todo.Todo{{Content: "task", Status: todo.StatusPending}})
	a.sessionStarted.Store(true) // pretend the first Run already fired SessionStart

	if err := a.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}

	if n := len(a.session.GetMessages()); n != 0 {
		t.Errorf("messages after clear: got %d, want 0", n)
	}
	if u := a.session.Usage; u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Errorf("usage after clear: got %+v, want zero", u)
	}
	if n := a.session.LastTurnInputTokens(); n != 0 {
		t.Errorf("lastTurnInputTokens after clear: got %d, want 0", n)
	}
	if got := a.toolState.TodoStore().List(); len(got) != 0 {
		t.Errorf("todos after clear: got %d entries, want 0", len(got))
	}
	if a.ID == "boot-id" || a.ID == "" {
		t.Errorf("session id after clear: got %q, want a fresh non-empty id", a.ID)
	}
	if !a.sessionCreatedAt.IsZero() {
		t.Errorf("sessionCreatedAt after clear: got %v, want zero", a.sessionCreatedAt)
	}
	if a.sessionStarted.Load() {
		t.Error("sessionStarted latch still set — SessionStart would never re-fire")
	}
	if a.sessionStartSource != "clear" {
		t.Errorf("sessionStartSource: got %q, want %q", a.sessionStartSource, "clear")
	}
}

// TestClearSession_Guards: a clear during a Run is refused with
// ErrRunInProgress, and subagents cannot clear at all.
func TestClearSession_Guards(t *testing.T) {
	a := newClearTestAgent()
	a.running.Store(true)
	if err := a.ClearSession(); err != ErrRunInProgress {
		t.Errorf("ClearSession while running: got %v, want ErrRunInProgress", err)
	}
	a.running.Store(false)

	sub := newClearTestAgent()
	sub.Parent = a
	if err := sub.ClearSession(); err == nil {
		t.Error("ClearSession on a subagent: got nil, want error")
	}
}
