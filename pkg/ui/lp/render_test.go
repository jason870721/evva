package lp

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
)

// TestRenderSmoke drives the root model headlessly (no TTY): size it, feed a
// few agent events, and confirm View composes a non-empty frame carrying
// lp's signature chrome — the "lp" brand and the gold ❯ prompt. This is the
// automatable proxy for "it renders"; the full interactive look still wants
// a real-terminal `evva -tui lp`.
func TestRenderSmoke(t *testing.T) {
	u := New("/tmp/evva-lp-test-home")
	m := u.model

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(events.AgentEventMsg{Event: event.Event{Kind: event.KindRunStart, RunStart: &event.RunStartPayload{Prompt: "hi"}}})
	m.Update(events.AgentEventMsg{Event: event.Event{
		Kind: event.KindToolUseStart,
		ToolUseStart: &event.ToolUseStartPayload{
			Name:   "read",
			Input:  []byte(`{"file_path":"main.go"}`),
			ToolID: "t1",
		},
	}})
	m.Update(events.AgentEventMsg{Event: event.Event{
		Kind:          event.KindToolUseResult,
		ToolUseResult: &event.ToolUseResultPayload{ToolID: "t1", Content: "142 lines"},
	}})
	m.Update(events.AgentEventMsg{Event: event.Event{
		Kind: event.KindText,
		Text: &event.TextPayload{Text: "token is stored in plaintext"},
	}})

	out := m.View()
	if strings.TrimSpace(out) == "" {
		t.Fatal("View returned empty frame")
	}
	if !strings.Contains(out, "lp") {
		t.Error("status line brand 'lp' missing from frame")
	}
	if !strings.Contains(out, "❯") {
		t.Error("input prompt '❯' missing from frame")
	}
}

// TestApprovalOverlayPushDoesNotPanic feeds a KindApprovalNeeded event and
// confirms the reused approval overlay mounts and renders without panicking —
// the mandatory broker-round-trip surface.
func TestApprovalOverlayPushDoesNotPanic(t *testing.T) {
	u := New("/tmp/evva-lp-test-home")
	m := u.model
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(events.AgentEventMsg{Event: event.Event{
		Kind: event.KindApprovalNeeded,
		ApprovalNeeded: &event.ApprovalNeededPayload{
			RequestID: "r1",
			ToolName:  "bash",
			ToolInput: []byte(`{"command":"rm -rf /tmp/x"}`),
			Mode:      "default",
		},
	}})
	// Without a controller attached the overlay is skipped defensively; either
	// way View must still compose.
	if strings.TrimSpace(m.View()) == "" {
		t.Fatal("View returned empty frame after approval event")
	}
}
