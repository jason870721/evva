package session

import (
	"reflect"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

func TestSnapshotRoundTripPreservesProviderFields(t *testing.T) {
	// Anthropic ThinkingSignature + DeepSeek reasoning_content (stored in
	// the same Thinking field) must round-trip verbatim — the LLM rejects
	// thinking blocks that come back without their original signature.
	live := New()
	live.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	live.Append(llm.Message{
		Role:              llm.RoleAssistant,
		Content:           "ok",
		Thinking:          "step 1\nstep 2",
		ThinkingSignature: "anthropic-sig-abc123",
		ToolCalls: []*tools.Call{
			{ID: "tu_1", Name: "read", Input: []byte(`{"file_path":"/x"}`)},
		},
	})
	live.Append(llm.Message{
		Role: llm.RoleTool,
		ToolResults: []*llm.ToolResult{
			{ID: "tu_1", Content: "file body"},
		},
	})
	live.RecordTurn(llm.Usage{InputTokens: 42, OutputTokens: 7})

	state := live.ToSnapshot()
	rehydrated := FromSnapshot(state)

	if !reflect.DeepEqual(live.Messages, rehydrated.Messages) {
		t.Errorf("messages diverged on round trip\nlive: %+v\ngot:  %+v", live.Messages, rehydrated.Messages)
	}
	if live.Usage != rehydrated.Usage {
		t.Errorf("usage diverged: live=%+v got=%+v", live.Usage, rehydrated.Usage)
	}
	if live.LastTurnInputTokens() != rehydrated.LastTurnInputTokens() {
		t.Errorf("last-turn tokens diverged: live=%d got=%d",
			live.LastTurnInputTokens(), rehydrated.LastTurnInputTokens())
	}
}

func TestSnapshotMessagesSliceIsCopied(t *testing.T) {
	live := New()
	live.Append(llm.Message{Role: llm.RoleUser, Content: "first"})
	state := live.ToSnapshot()
	state.Messages[0].Content = "tampered"
	if live.Messages[0].Content != "first" {
		t.Errorf("snapshot mutation aliased into live session: %q", live.Messages[0].Content)
	}
}

func TestFirstUserPromptPreviewTrimsAndCaps(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "ignored system message"},
		{Role: llm.RoleUser, Content: "\n\n  Hello\n  world  "},
	}
	got := FirstUserPromptPreview(msgs)
	if got != "Hello world" {
		t.Errorf("preview: got %q want %q", got, "Hello world")
	}

	long := make([]byte, PreviewMaxBytes+50)
	for i := range long {
		long[i] = 'a'
	}
	got = FirstUserPromptPreview([]llm.Message{
		{Role: llm.RoleUser, Content: string(long)},
	})
	if len(got) != PreviewMaxBytes {
		t.Errorf("preview length: got %d want %d", len(got), PreviewMaxBytes)
	}
}

func TestFirstUserPromptPreviewSkipsNonUser(t *testing.T) {
	got := FirstUserPromptPreview([]llm.Message{
		{Role: llm.RoleAssistant, Content: "hello there"},
	})
	if got != "" {
		t.Errorf("expected empty preview for no-user transcript; got %q", got)
	}
}
