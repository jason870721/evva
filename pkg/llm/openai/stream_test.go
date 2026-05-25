package openai

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

// TestConsumeStream feeds a canned SSE byte stream through consumeStream and
// asserts both the chunk-callback order and the final assembled Response.
// Covers: text deltas, multi-fragment tool_call arguments across chunks,
// terminal usage frame with OpenAI-nested usage shape, [DONE] terminator.
// No ChunkThinking is expected — OpenAI Chat Completions does not stream
// reasoning content.
func TestConsumeStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"Hello "}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"content":"world"}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"tc_1","type":"function","function":{"name":"echo","arguments":"{\"msg\""}}]}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"hi\"}"}}]}}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":7}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var chunks []llm.Chunk
	sink := llm.ChunkFunc(func(c llm.Chunk) { chunks = append(chunks, c) })

	c := &Client{}
	resp, err := c.consumeStream(context.Background(), strings.NewReader(body), sink)
	if err != nil {
		t.Fatalf("consumeStream: %v", err)
	}

	wantChunks := []llm.Chunk{
		{Kind: llm.ChunkText, Delta: "Hello "},
		{Kind: llm.ChunkText, Delta: "world"},
	}
	if len(chunks) != len(wantChunks) {
		t.Fatalf("chunk count: got %d, want %d (chunks=%v)", len(chunks), len(wantChunks), chunks)
	}
	for i, w := range wantChunks {
		if chunks[i] != w {
			t.Errorf("chunk[%d]: got %+v, want %+v", i, chunks[i], w)
		}
	}

	if got, want := resp.Content, "Hello world"; got != want {
		t.Errorf("Content: got %q, want %q", got, want)
	}
	if resp.Thinking != "" {
		t.Errorf("Thinking: got %q, want empty (OpenAI does not stream reasoning)", resp.Thinking)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: got %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "tc_1" || tc.Name != "echo" {
		t.Errorf("tool call meta: got id=%q name=%q, want id=tc_1 name=echo", tc.ID, tc.Name)
	}
	if got, want := string(tc.Input), `{"msg":"hi"}`; got != want {
		t.Errorf("tool call args: got %q, want %q", got, want)
	}

	if got, want := resp.Usage.InputTokens, 12; got != want {
		t.Errorf("InputTokens: got %d, want %d", got, want)
	}
	if got, want := resp.Usage.OutputTokens, 34; got != want {
		t.Errorf("OutputTokens: got %d, want %d", got, want)
	}
	if got, want := resp.Usage.CacheReadTokens, 5; got != want {
		t.Errorf("CacheReadTokens: got %d, want %d", got, want)
	}
	if got, want := resp.Usage.ReasoningTokens, 7; got != want {
		t.Errorf("ReasoningTokens: got %d, want %d", got, want)
	}
}

// TestConsumeStreamCancel verifies the scanner loop respects ctx cancellation
// between SSE frames and returns llm.ErrInterrupted (not the raw context
// error) so the agent's cancel path treats it like any other interrupt.
func TestConsumeStreamCancel(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"part1"}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"content":"part2"}}]}`,
		``,
	}, "\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: the very first iteration should bail.

	c := &Client{}
	_, err := c.consumeStream(ctx, strings.NewReader(body), llm.DiscardChunks)
	if err != llm.ErrInterrupted {
		t.Fatalf("err: got %v, want llm.ErrInterrupted", err)
	}
}
