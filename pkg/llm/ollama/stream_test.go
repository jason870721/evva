package ollama

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

// TestConsumeStream feeds canned NDJSON frames through consumeStream and
// verifies the chunk order, the accumulated content/thinking, the final
// usage from the done frame, and that tool calls land with synthesized IDs.
func TestConsumeStream(t *testing.T) {
	body := strings.Join([]string{
		`{"model":"qwen3","message":{"role":"assistant","thinking":"let me ","content":""},"done":false}`,
		`{"model":"qwen3","message":{"role":"assistant","thinking":"think…"},"done":false}`,
		`{"model":"qwen3","message":{"role":"assistant","content":"Hello "},"done":false}`,
		`{"model":"qwen3","message":{"role":"assistant","content":"world"},"done":false}`,
		`{"model":"qwen3","message":{"role":"assistant","tool_calls":[{"function":{"name":"echo","arguments":{"msg":"hi"}}}]},"done":false}`,
		`{"model":"qwen3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":34}`,
	}, "\n")

	var chunks []llm.Chunk
	sink := llm.ChunkFunc(func(c llm.Chunk) { chunks = append(chunks, c) })

	resp, err := consumeStream(context.Background(), strings.NewReader(body), sink)
	if err != nil {
		t.Fatalf("consumeStream: %v", err)
	}

	wantChunks := []llm.Chunk{
		{Kind: llm.ChunkThinking, Delta: "let me "},
		{Kind: llm.ChunkThinking, Delta: "think…"},
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
	if got, want := resp.Thinking, "let me think…"; got != want {
		t.Errorf("Thinking: got %q, want %q", got, want)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: got %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "echo" {
		t.Errorf("tool name: got %q, want echo", tc.Name)
	}
	if !strings.HasPrefix(tc.ID, "ollama_") {
		t.Errorf("tool id: got %q, want ollama_ prefix", tc.ID)
	}
	if got, want := string(tc.Input), `{"msg":"hi"}`; got != want {
		t.Errorf("tool input: got %q, want %q", got, want)
	}

	if got, want := resp.Usage.InputTokens, 12; got != want {
		t.Errorf("InputTokens: got %d, want %d", got, want)
	}
	if got, want := resp.Usage.OutputTokens, 34; got != want {
		t.Errorf("OutputTokens: got %d, want %d", got, want)
	}
}

// TestConsumeStreamError verifies an inline error field aborts with the
// server's message wrapped into a Go error.
func TestConsumeStreamError(t *testing.T) {
	body := `{"error":"model not found"}` + "\n"
	_, err := consumeStream(context.Background(), strings.NewReader(body), llm.DiscardChunks)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "model not found") {
		t.Errorf("err: got %q, want it to contain \"model not found\"", got)
	}
}

// TestConsumeStreamCancel verifies the scanner loop returns
// llm.ErrInterrupted when ctx is cancelled.
func TestConsumeStreamCancel(t *testing.T) {
	body := `{"model":"qwen3","message":{"role":"assistant","content":"hi"},"done":false}` + "\n"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := consumeStream(ctx, strings.NewReader(body), llm.DiscardChunks)
	if err != llm.ErrInterrupted {
		t.Fatalf("err: got %v, want llm.ErrInterrupted", err)
	}
}
