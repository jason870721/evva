package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/internal/tools"
)

// Stream is the chunked variant of Complete. Ollama's /api/chat with
// stream:true emits newline-delimited JSON: one apiResponse-shaped object
// per line. message.content and message.thinking carry incremental deltas
// (Ollama 0.1+ semantics); the final line has done:true and the eval
// counts.
//
// Ollama doesn't fragment tool-call arguments across frames — when
// message.tool_calls is populated it carries the full call. We still
// accumulate by index so a future Ollama version that does fragment
// arguments would work without further changes.
func (c *Client) Stream(ctx context.Context, messages []llm.Message, toolSet []tools.Tool, sink llm.ChunkSink) (llm.Response, error) {
	if sink == nil {
		sink = llm.DiscardChunks
	}

	body := apiRequest{
		Model:    c.model,
		Messages: toAPIMessages(messages, c.params.System),
		Tools:    toAPITools(toolSet),
		Stream:   true,
		Think:    true,
		Options:  buildOptions(c.params),
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/x-ndjson")

	resp, err := c.params.HTTP().Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return llm.Response{}, fmt.Errorf("ollama: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	return consumeStream(ctx, resp.Body, sink)
}

// consumeStream parses Ollama's NDJSON streaming body. Each line is decoded
// as an apiResponse; deltas in message.content / message.thinking fire
// chunks to sink, the final line populates usage. The function is
// package-level (not a method) so tests can pump synthetic readers without
// constructing a Client.
func consumeStream(ctx context.Context, body io.Reader, sink llm.ChunkSink) (llm.Response, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var (
		out         llm.Response
		text        strings.Builder
		thinking    strings.Builder
		toolBuffers []*tools.Call
	)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return llm.Response{}, llm.ErrInterrupted
			}
			return llm.Response{}, err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var frame apiResponse
		if err := json.Unmarshal(line, &frame); err != nil {
			return llm.Response{}, fmt.Errorf("ollama: decode stream frame: %w", err)
		}
		if frame.Error != "" {
			return llm.Response{}, fmt.Errorf("ollama: %s", frame.Error)
		}

		if d := frame.Message.Content; d != "" {
			text.WriteString(d)
			sink.OnChunk(llm.Chunk{Kind: llm.ChunkText, Delta: d})
		}
		if d := frame.Message.Thinking; d != "" {
			thinking.WriteString(d)
			sink.OnChunk(llm.Chunk{Kind: llm.ChunkThinking, Delta: d})
		}
		for _, tc := range frame.Message.ToolCalls {
			toolBuffers = append(toolBuffers, &tools.Call{
				ID:    newToolID(),
				Name:  tc.Function.Name,
				Input: tc.Function.Arguments,
			})
		}

		if frame.Done {
			out.Usage.InputTokens = frame.PromptEvalCount
			out.Usage.OutputTokens = frame.EvalCount
			// Ollama emits done as the terminal line — break early so we
			// don't loop on any trailing whitespace after EOF.
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return llm.Response{}, llm.ErrInterrupted
		}
		return llm.Response{}, fmt.Errorf("ollama: stream: %w", llm.NormalizeErr(err))
	}

	out.Content = text.String()
	out.Thinking = thinking.String()
	out.ToolCalls = toolBuffers
	return out, nil
}
