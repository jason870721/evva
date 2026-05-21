package agent

import (
	"context"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/toolset"
)

// TestDrainUserPrompts_LandsAsRoleUser locks in the contract: a prompt
// the UI enqueues while a Run is in flight gets folded into the
// session as a fresh RoleUser message at the top of the next
// iteration. This is the "user can type mid-run" feature.
func TestDrainUserPrompts_LandsAsRoleUser(t *testing.T) {
	stub := &stubLLM{
		complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
			// Terminal response: no tool_calls -> loop exits after one iter.
			return llm.Response{}, nil
		},
	}
	a := newTestAgent(stub)
	a.toolState = toolset.NewToolState()
	a.maxIters.Store(3)

	// Pretend the UI enqueued two prompts while the agent was idle.
	// drainUserPrompts is supposed to fold both into the session in
	// order, as separate RoleUser turns.
	a.toolState.UserPromptQueue().Enqueue("first interruption")
	a.toolState.UserPromptQueue().Enqueue("second interruption")

	if _, err := a.Run(context.Background(), "initial"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var userMsgs []string
	for _, m := range a.session.GetMessages() {
		if m.Role == llm.RoleUser {
			userMsgs = append(userMsgs, m.Content)
		}
	}
	want := []string{"initial", "first interruption", "second interruption"}
	if len(userMsgs) != len(want) {
		t.Fatalf("got %d user messages, want %d: %v", len(userMsgs), len(want), userMsgs)
	}
	for i, w := range want {
		if userMsgs[i] != w {
			t.Errorf("user msg %d: got %q, want %q", i, userMsgs[i], w)
		}
	}

	// Queue should be empty after the drain.
	if got := a.toolState.UserPromptQueue().Len(); got != 0 {
		t.Errorf("queue should be empty after drain; got Len=%d", got)
	}
}

// TestDrainUserPrompts_SkipsSubagents asserts that a subagent never
// drains its own queue — only the root agent does. This protects the
// "user can only talk to main" invariant.
func TestDrainUserPrompts_SkipsSubagents(t *testing.T) {
	parent := newTestAgent(nil)
	parent.toolState = toolset.NewToolState()

	child := newTestAgent(nil)
	child.toolState = toolset.NewToolState()
	child.Parent = parent // marks it as a subagent (IsSubagent checks Parent)

	// Seed the subagent's own queue. If drainUserPrompts mistakenly
	// processes it, this entry would land in the subagent's session.
	child.toolState.UserPromptQueue().Enqueue("should never land")

	child.drainUserPrompts()

	if len(child.session.GetMessages()) != 0 {
		t.Fatalf("subagent drained the queue; got %d messages, want 0",
			len(child.session.GetMessages()))
	}
	// And the queue should still hold the entry (untouched).
	if got := child.toolState.UserPromptQueue().Len(); got != 1 {
		t.Errorf("subagent should not have drained; queue Len=%d, want 1", got)
	}
}
