package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

func TestToAPIMessages_ImageContentBlocks(t *testing.T) {
	msgs := []llm.Message{{
		Role: llm.RoleTool,
		ToolResults: []*llm.ToolResult{{
			ID:      "toolu_001",
			Content: "",
			ContentBlocks: []tools.ContentBlock{
				{Type: tools.ContentBlockText, Text: "Image analysis result:"},
				{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{
					MIMEType:     "image/png",
					Base64Data:   "iVBORw0KGgo=",
					OriginalSize: 1234,
				}},
			},
		}},
	}}

	out := toAPIMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 apiMessage; got %d", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("expected role=user; got %s", out[0].Role)
	}
	if len(out[0].Content) != 1 {
		t.Fatalf("expected 1 content block; got %d", len(out[0].Content))
	}

	tr := out[0].Content[0]
	if tr.Type != "tool_result" {
		t.Errorf("expected type=tool_result; got %s", tr.Type)
	}
	if tr.ToolUseID != "toolu_001" {
		t.Errorf("expected tool_use_id=toolu_001; got %s", tr.ToolUseID)
	}

	// Content should be []blockContentItem, not a plain string.
	items, ok := tr.Content.([]blockContentItem)
	if !ok {
		t.Fatalf("expected Content to be []blockContentItem; got %T", tr.Content)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 content items; got %d", len(items))
	}
	if items[0].Type != "text" || items[0].Text != "Image analysis result:" {
		t.Errorf("first item: got type=%s text=%q", items[0].Type, items[0].Text)
	}
	if items[1].Type != "image" {
		t.Fatalf("second item: expected type=image; got %s", items[1].Type)
	}
	if items[1].Source == nil {
		t.Fatal("image source should not be nil")
	}
	if items[1].Source.Type != "base64" {
		t.Errorf("expected source type=base64; got %s", items[1].Source.Type)
	}
	if items[1].Source.MediaType != "image/png" {
		t.Errorf("expected media_type=image/png; got %s", items[1].Source.MediaType)
	}
	if items[1].Source.Data != "iVBORw0KGgo=" {
		t.Errorf("expected base64 data; got %s", items[1].Source.Data)
	}
}

func TestToAPIMessages_PlainTextToolResult(t *testing.T) {
	// Backward compat: plain text tool results still work.
	msgs := []llm.Message{{
		Role: llm.RoleTool,
		ToolResults: []*llm.ToolResult{{
			ID:      "toolu_002",
			Content: "done",
		}},
	}}

	out := toAPIMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 apiMessage; got %d", len(out))
	}
	tr := out[0].Content[0]
	if tr.Type != "tool_result" {
		t.Errorf("expected type=tool_result; got %s", tr.Type)
	}
	content, ok := tr.Content.(string)
	if !ok {
		t.Fatalf("expected Content to be string; got %T", tr.Content)
	}
	if content != "done" {
		t.Errorf("expected content='done'; got %q", content)
	}
}

func TestToAPIMessages_ErrorStaysTextOnly(t *testing.T) {
	// Anthropic requires is_error tool_results to contain only text.
	msgs := []llm.Message{{
		Role: llm.RoleTool,
		ToolResults: []*llm.ToolResult{{
			ID:      "toolu_003",
			Content: "read failed",
			IsError: true,
			ContentBlocks: []tools.ContentBlock{
				{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{
					MIMEType: "image/png", Base64Data: "AAAA",
				}},
			},
		}},
	}}

	out := toAPIMessages(msgs)
	tr := out[0].Content[0]
	// Error with ContentBlocks: should fall back to the plain string.
	content, ok := tr.Content.(string)
	if !ok {
		t.Fatalf("error tool_result: expected string Content; got %T", tr.Content)
	}
	if content != "read failed" {
		t.Errorf("expected content='read failed'; got %q", content)
	}
}

func TestToAPIMessages_MultipleParallelResults(t *testing.T) {
	// Multiple tool results from parallel dispatch: all in one user message.
	msgs := []llm.Message{{
		Role: llm.RoleTool,
		ToolResults: []*llm.ToolResult{
			{ID: "t1", Content: "a"},
			{ID: "t2", Content: "b", ContentBlocks: []tools.ContentBlock{
				{Type: tools.ContentBlockText, Text: "b"},
				{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{
					MIMEType: "image/jpeg", Base64Data: "/9j/", OriginalSize: 42,
				}},
			}},
		},
	}}

	out := toAPIMessages(msgs)
	if len(out[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks; got %d", len(out[0].Content))
	}
	// First: plain string.
	if s, ok := out[0].Content[0].Content.(string); !ok || s != "a" {
		t.Errorf("first: got type=%T val=%v", out[0].Content[0].Content, out[0].Content[0].Content)
	}
	// Second: array with text + image.
	items, ok := out[0].Content[1].Content.([]blockContentItem)
	if !ok || len(items) != 2 {
		t.Errorf("second: expected 2 items; got %T len=%d", out[0].Content[1].Content, len(items))
	}
}

func TestToolResultRoundTripJSON(t *testing.T) {
	// Verify the wire format matches Anthropic's expected JSON.
	msgs := []llm.Message{{
		Role: llm.RoleTool,
		ToolResults: []*llm.ToolResult{{
			ID: "toolu_004",
			ContentBlocks: []tools.ContentBlock{
				{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{
					MIMEType:   "image/png",
					Base64Data: "iVBORw0KGgo=",
				}},
			},
		}},
	}}

	out := toAPIMessages(msgs)
	raw, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	// Parse back and verify the content array structure.
	var parsed struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   json.RawMessage
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	if parsed.Role != "user" {
		t.Errorf("role: got %s, want user", parsed.Role)
	}

	// Content should be a JSON array, not a string.
	inner := parsed.Content[0].Content
	if len(inner) == 0 || inner[0] != '[' {
		t.Errorf("expected content array; got %s", string(inner))
	}
}

func TestAnthropicEffort(t *testing.T) {
	// evva's effort ladder maps onto Anthropic's API ladder shifted up one
	// notch: "low" (evva) → "medium" (api), "medium" → "high",
	// "high" → "xhigh", "ultra" → "max". Even the lowest tier sends a
	// non-empty effort because evva's "low" still means fast-thinking,
	// not no-reasoning.
	tests := []struct {
		level int
		want  string
	}{
		{0, ""},
		{1, "medium"},
		{2, "high"},
		{3, "xhigh"},
		{4, "max"},
		{5, ""},
	}
	for _, tt := range tests {
		got := anthropicEffort(tt.level)
		if got != tt.want {
			t.Errorf("anthropicEffort(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// --- prompt caching -------------------------------------------------------

type cacheStubTool struct{ name string }

func (s cacheStubTool) Name() string            { return s.name }
func (s cacheStubTool) Description() string     { return "stub " + s.name }
func (s cacheStubTool) Schema() json.RawMessage { return nil }
func (s cacheStubTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	return tools.Result{}, nil
}

func cacheTestMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{
			Role:              llm.RoleAssistant,
			Content:           "on it",
			Thinking:          "hmm",
			ThinkingSignature: "sig",
			ToolCalls:         []*tools.Call{{ID: "toolu_1", Name: "read", Input: json.RawMessage(`{}`)}},
		},
		{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{ID: "toolu_1", Content: "file body"}}},
	}
}

func TestBuildRequestBody_PromptCacheBreakpoints(t *testing.T) {
	c := New(llm.APIConfig{}, "claude-test", llm.WithSystem("system prompt"))
	body := c.buildRequestBody(cacheTestMessages(), []tools.Tool{cacheStubTool{"a"}, cacheStubTool{"b"}})

	if body.Tools[0].CacheControl != nil {
		t.Errorf("first tool must not carry cache_control")
	}
	if body.Tools[1].CacheControl == nil {
		t.Errorf("last tool must carry cache_control")
	}

	sys, ok := body.System.([]block)
	if !ok || len(sys) != 1 {
		t.Fatalf("system must be a single-block array when caching is on, got %#v", body.System)
	}
	if sys[0].Text != "system prompt" || sys[0].CacheControl == nil {
		t.Errorf("system block must keep the text and carry cache_control, got %#v", sys[0])
	}

	last := body.Messages[len(body.Messages)-1]
	if last.Content[len(last.Content)-1].CacheControl == nil {
		t.Errorf("last block of last message must carry cache_control")
	}
	for _, m := range body.Messages[:len(body.Messages)-1] {
		for _, b := range m.Content {
			if b.CacheControl != nil {
				t.Errorf("only the last message may carry cache_control, found on %q block", b.Type)
			}
		}
	}

	// Exactly 3 breakpoints total — the API rejects more than 4.
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(raw, []byte(`"cache_control"`)); n != 3 {
		t.Errorf("want exactly 3 cache_control markers on the wire, got %d", n)
	}
}

func TestBuildRequestBody_PromptCacheDisabled(t *testing.T) {
	c := New(llm.APIConfig{}, "claude-test",
		llm.WithSystem("system prompt"), llm.WithPromptCacheDisabled(true))
	body := c.buildRequestBody(cacheTestMessages(), []tools.Tool{cacheStubTool{"a"}})

	if s, ok := body.System.(string); !ok || s != "system prompt" {
		t.Errorf("system must stay a plain string when caching is disabled, got %#v", body.System)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"cache_control"`)) {
		t.Errorf("no cache_control markers may appear when caching is disabled")
	}
}

func TestBuildRequestBody_CacheSkipsThinkingBlock(t *testing.T) {
	// A trailing assistant message whose last block is thinking: the
	// breakpoint must land on an earlier non-thinking block, never on the
	// thinking block itself.
	msgs := []llm.Message{{
		Role:              llm.RoleAssistant,
		Content:           "",
		Thinking:          "…",
		ThinkingSignature: "sig",
		ToolCalls:         []*tools.Call{{ID: "t1", Name: "read", Input: json.RawMessage(`{}`)}},
	}}
	c := New(llm.APIConfig{}, "claude-test")
	body := c.buildRequestBody(msgs, nil)

	last := body.Messages[0]
	for _, b := range last.Content {
		if b.Type == "thinking" && b.CacheControl != nil {
			t.Fatalf("cache_control must never land on a thinking block")
		}
	}
	if last.Content[len(last.Content)-1].CacheControl == nil {
		t.Errorf("breakpoint should land on the trailing tool_use block")
	}
}
