package openai

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
	"github.com/johnny1110/evva/pkg/tools"
)

// streamChunk is one SSE frame off OpenAI's streaming chat completions
// endpoint. Most fields mirror apiResponse; deltas live under choices[i].Delta
// instead of choices[i].Message. The terminal frame (when include_usage is
// set) populates Usage; tool-call argument fragments accumulate per-index
// in Delta.ToolCalls[i].Function.Arguments.
//
// OpenAI Chat Completions does NOT stream reasoning content — the model's
// thinking stays opaque. Only the Responses API surfaces reasoning
// summaries. ChunkThinking is therefore never emitted by this client.
type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string                `json:"role,omitempty"`
			Content   string                `json:"content,omitempty"`
			ToolCalls []streamToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details,omitempty"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// streamToolCallDelta is one streaming fragment of a tool call. The Index is
// stable across fragments for the same call; ID and Function.Name appear on
// the first fragment, Function.Arguments accumulates over subsequent ones.
type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// Stream is the chunked variant of Complete. It opens a streaming SSE
// connection to OpenAI's chat endpoint, forwards each delta to sink as a
// Chunk, and returns the fully-assembled Response (content, tool calls,
// usage) once the server emits its terminal data: [DONE] frame.
//
// On context cancellation the underlying request transport aborts; the
// scanner loop honors ctx.Err() between reads and returns
// llm.ErrInterrupted in that case so the agent's interrupt path treats it
// like any other cancelled completion.
func (c *Client) Stream(ctx context.Context, messages []llm.Message, toolSet []tools.Tool, sink llm.ChunkSink) (llm.Response, error) {
	if c.apiKey == "" {
		return llm.Response{}, fmt.Errorf("openai: missing API key (type in /config to setup)")
	}
	if sink == nil {
		sink = llm.DiscardChunks
	}

	params := stripSamplingForReasoning(c.params, c.model)
	body := apiRequest{
		Model:           c.model,
		Messages:        toAPIMessages(messages, params.System),
		Temperature:     params.Temperature,
		TopP:            params.TopP,
		MaxTokens:       params.MaxTokens,
		Stop:            params.StopSequences,
		Tools:           toAPITools(toolSet),
		ReasoningEffort: openaiEffort(c.params.Effort),
		Stream:          true,
		StreamOptions:   &apiStreamOptions{IncludeUsage: true},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("authorization", "Bearer "+c.apiKey)

	resp, err := c.params.HTTP().Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return llm.Response{}, fmt.Errorf("openai: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	return c.consumeStream(ctx, resp.Body, sink)
}

// consumeStream is the SSE decoding loop, factored out for testability with
// a synthetic io.Reader.
func (c *Client) consumeStream(ctx context.Context, body io.Reader, sink llm.ChunkSink) (llm.Response, error) {
	scanner := bufio.NewScanner(body)
	// Tool-call argument JSON can run long. 1 MB is plenty headroom above
	// bufio's 64 KB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var (
		out         llm.Response
		text        strings.Builder
		toolBuffers = map[int]*streamingToolCall{}
		toolOrder   []int
	)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return llm.Response{}, llm.ErrInterrupted
			}
			return llm.Response{}, err
		}
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				break
			}
			continue
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return llm.Response{}, fmt.Errorf("openai: decode stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return llm.Response{}, fmt.Errorf("openai: %s: %s", chunk.Error.Type, chunk.Error.Message)
		}

		for _, ch := range chunk.Choices {
			if d := ch.Delta.Content; d != "" {
				text.WriteString(d)
				sink.OnChunk(llm.Chunk{Kind: llm.ChunkText, Delta: d})
			}
			for _, tc := range ch.Delta.ToolCalls {
				buf, exists := toolBuffers[tc.Index]
				if !exists {
					buf = &streamingToolCall{}
					toolBuffers[tc.Index] = buf
					toolOrder = append(toolOrder, tc.Index)
				}
				if tc.ID != "" {
					buf.id = tc.ID
				}
				if tc.Function.Name != "" {
					buf.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					buf.args.WriteString(tc.Function.Arguments)
				}
			}
		}

		if chunk.Usage != nil {
			out.Usage = llm.Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
			if d := chunk.Usage.PromptTokensDetails; d != nil {
				out.Usage.CacheReadTokens = d.CachedTokens
			}
			if d := chunk.Usage.CompletionTokensDetails; d != nil {
				out.Usage.ReasoningTokens = d.ReasoningTokens
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return llm.Response{}, llm.ErrInterrupted
		}
		return llm.Response{}, fmt.Errorf("openai: stream: %w", llm.NormalizeErr(err))
	}

	out.Content = text.String()
	for _, idx := range toolOrder {
		buf := toolBuffers[idx]
		args := buf.args.String()
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, &tools.Call{
			ID:    buf.id,
			Name:  buf.name,
			Input: json.RawMessage(args),
		})
	}
	return out, nil
}

type streamingToolCall struct {
	id   string
	name string
	args strings.Builder
}
