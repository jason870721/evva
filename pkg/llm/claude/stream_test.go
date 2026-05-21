package claude

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

// TestConsumeStream feeds a canned Anthropic SSE byte stream through
// consumeStream and asserts:
//   - chunks fire in arrival order for text + thinking deltas
//   - signature_delta accumulates but never surfaces as a chunk (opaque)
//   - input_json_delta accumulates per index into the final tool call
//   - usage stats from message_start are augmented by message_delta updates
//   - the assembled Response carries every block in arrival order
func TestConsumeStream(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":3}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reflecting "}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"on this"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"123"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello "}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"there"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"echo","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"msg\""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":":\"hi\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":2}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
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
		{Kind: llm.ChunkThinking, Delta: "reflecting "},
		{Kind: llm.ChunkThinking, Delta: "on this"},
		{Kind: llm.ChunkText, Delta: "Hello "},
		{Kind: llm.ChunkText, Delta: "there"},
	}
	if len(chunks) != len(wantChunks) {
		t.Fatalf("chunk count: got %d, want %d (chunks=%v)", len(chunks), len(wantChunks), chunks)
	}
	for i, w := range wantChunks {
		if chunks[i] != w {
			t.Errorf("chunk[%d]: got %+v, want %+v", i, chunks[i], w)
		}
	}

	if got, want := resp.Content, "Hello there"; got != want {
		t.Errorf("Content: got %q, want %q", got, want)
	}
	if got, want := resp.Thinking, "reflecting on this"; got != want {
		t.Errorf("Thinking: got %q, want %q", got, want)
	}
	if got, want := resp.ThinkingSignature, "abc123"; got != want {
		t.Errorf("ThinkingSignature: got %q, want %q", got, want)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: got %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "echo" {
		t.Errorf("tool call meta: got id=%q name=%q, want id=toolu_1 name=echo", tc.ID, tc.Name)
	}
	if got, want := string(tc.Input), `{"msg":"hi"}`; got != want {
		t.Errorf("tool call args: got %q, want %q", got, want)
	}

	// Initial usage (input/cache) from message_start, output_tokens upgraded
	// by message_delta to the final value.
	if got, want := resp.Usage.InputTokens, 10; got != want {
		t.Errorf("InputTokens: got %d, want %d", got, want)
	}
	if got, want := resp.Usage.OutputTokens, 42; got != want {
		t.Errorf("OutputTokens: got %d, want %d", got, want)
	}
	if got, want := resp.Usage.CacheReadTokens, 3; got != want {
		t.Errorf("CacheReadTokens: got %d, want %d", got, want)
	}
}

// TestConsumeStreamPing verifies ping keepalive frames don't perturb the
// accumulated state.
func TestConsumeStreamPing(t *testing.T) {
	body := strings.Join([]string{
		`event: ping`,
		`data: {"type":"ping"}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: ping`,
		`data: {"type":"ping"}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	c := &Client{}
	resp, err := c.consumeStream(context.Background(), strings.NewReader(body), llm.DiscardChunks)
	if err != nil {
		t.Fatalf("consumeStream: %v", err)
	}
	if got, want := resp.Content, "hi"; got != want {
		t.Errorf("Content: got %q, want %q", got, want)
	}
}

// TestConsumeStreamError verifies error frames abort with the server's
// reason wrapped into a Go error.
func TestConsumeStreamError(t *testing.T) {
	body := strings.Join([]string{
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"server busy"}}`,
		``,
	}, "\n")

	c := &Client{}
	_, err := c.consumeStream(context.Background(), strings.NewReader(body), llm.DiscardChunks)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "overloaded_error") || !strings.Contains(got, "server busy") {
		t.Errorf("err: got %q, want it to contain overloaded_error and server busy", got)
	}
}

// TestConsumeStreamCancel verifies the scanner loop returns
// llm.ErrInterrupted when ctx is cancelled.
func TestConsumeStreamCancel(t *testing.T) {
	body := strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
	}, "\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := &Client{}
	_, err := c.consumeStream(ctx, strings.NewReader(body), llm.DiscardChunks)
	if err != llm.ErrInterrupted {
		t.Fatalf("err: got %v, want llm.ErrInterrupted", err)
	}
}
