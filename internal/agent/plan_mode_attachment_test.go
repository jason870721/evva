package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/internal/permission"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/toolset"
)

// capturingSink is a minimal event.Sink that records every Emit for
// later inspection. Local to this file because the agent package has
// no shared test sink today.
type capturingSink struct {
	mu     sync.Mutex
	events []event.Event
}

func newCapturingSink() *capturingSink { return &capturingSink{} }

func (s *capturingSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *capturingSink) assertOne(t *testing.T, kind event.Kind) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Kind == kind {
			return
		}
	}
	t.Errorf("expected at least one %q event; got %d events", kind, len(s.events))
}

// Integration tests for the per-turn plan-mode attachment system:
// SetPermissionMode runs the transition hub, drainUserPrompts (and
// Run, for the very first prompt) prepends the resulting reminders to
// the session, and ExitPlanMode-style transitions queue a one-shot exit
// reminder on the next user turn.

func newPlanModeTestAgent() *Agent {
	a := newTestAgent(&stubLLM{
		complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
			return llm.Response{}, nil // terminal turn
		},
	})
	a.toolState = toolset.NewToolState()
	a.planModeState = permission.NewPlanModeState()
	a.maxIters.Store(2)
	return a
}

// Run() with the agent in plan mode must inject the FULL workflow
// reminder before the user's prompt — that's how the model learns it is
// currently in plan mode (the static system prompt only knows plan mode
// exists as a concept).
func TestRun_InjectsPlanModeReminderBeforeUserPrompt(t *testing.T) {
	a := newPlanModeTestAgent()
	a.SetPermissionMode(permission.ModePlan)

	if _, err := a.Run(context.Background(), "design a new caching layer"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := a.session.GetMessages()
	var users []string
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			users = append(users, m.Content)
		}
	}
	if len(users) < 2 {
		t.Fatalf("expected at least 2 user messages (reminder + prompt); got %d: %v", len(users), users)
	}
	if !strings.Contains(users[0], "<system-reminder>") {
		t.Errorf("first user message should be a <system-reminder>; got:\n%s", users[0])
	}
	if !strings.Contains(users[0], "Plan mode is active") {
		t.Errorf("reminder should announce plan mode; got:\n%s", users[0])
	}
	if users[len(users)-1] != "design a new caching layer" {
		t.Errorf("last user message should be the actual prompt; got %q", users[len(users)-1])
	}
}

// Run() with the agent in default mode must NOT inject a plan-mode
// reminder. Cheap insurance against bugs that would burn tokens on every
// turn regardless of mode.
func TestRun_NoReminderInDefaultMode(t *testing.T) {
	a := newPlanModeTestAgent()
	// stays in default mode

	if _, err := a.Run(context.Background(), "what does foo do?"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, m := range a.session.GetMessages() {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "<system-reminder>") {
			t.Errorf("default mode should not inject reminders; found:\n%s", m.Content)
		}
	}
}

// After exiting plan mode, the next user prompt must carry the one-shot
// exit reminder. Locks in the user-visible bug-fix promise of Phase 11:
// the model is told the constraints have lifted, instead of silently
// being out of plan mode.
func TestDrainUserPrompts_FiresExitReminderAfterTransition(t *testing.T) {
	a := newPlanModeTestAgent()
	a.SetPermissionMode(permission.ModePlan)
	a.SetPermissionMode(permission.ModeDefault)

	a.toolState.UserPromptQueue().Enqueue("now make the change")
	a.drainUserPrompts()

	msgs := a.session.GetMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected exit reminder + user prompt; got %d messages", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "exited plan mode") {
		t.Errorf("first message should be the exit reminder; got:\n%s", msgs[0].Content)
	}
	if msgs[1].Content != "now make the change" {
		t.Errorf("second message should be the user prompt; got %q", msgs[1].Content)
	}

	// Drain again with another prompt — exit reminder is a one-shot,
	// should NOT fire a second time.
	a.toolState.UserPromptQueue().Enqueue("and another")
	a.drainUserPrompts()
	if strings.Contains(a.session.GetMessages()[2].Content, "exited plan mode") {
		t.Errorf("exit reminder should fire only once")
	}
}

// SetPermissionMode must emit a KindModeChanged event so the TUI status
// bar updates without waiting for the next keystroke (the bug the user
// reported: status bar stays on "plan" after exit_plan_mode approval).
func TestSetPermissionMode_EmitsModeChangedEvent(t *testing.T) {
	a := newPlanModeTestAgent()
	captured := newCapturingSink()
	a.sink = captured

	a.SetPermissionMode(permission.ModeAcceptEdits)

	captured.assertOne(t, "mode_changed")
}

// Idempotent transitions (same mode) must not emit a duplicate event —
// otherwise a Shift+Tab cycle that resolves to the same mode would
// flicker the status bar.
func TestSetPermissionMode_NoEventOnSameModeNoOp(t *testing.T) {
	a := newPlanModeTestAgent()
	captured := newCapturingSink()
	a.sink = captured

	// Default is the startup mode, so a redundant Set is a no-op.
	a.SetPermissionMode(permission.ModeDefault)

	if got := len(captured.events); got != 0 {
		t.Errorf("no-op transition should emit no events; got %d", got)
	}
}
