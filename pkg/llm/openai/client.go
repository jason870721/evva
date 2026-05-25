package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

const (
	DefaultModel = "gpt-5.4-mini"
	chatPath     = "/v1/chat/completions"
)

// Client implements llm.Client backed by OpenAI's Chat Completions API.
type Client struct {
	name   string
	apiURL string
	apiKey string
	model  string
	params llm.LLMParams
}

// New builds an OpenAI client from provider config and applies the given options.
// Options can be re-applied at runtime via Apply.
func New(cfg llm.APIConfig, model string, opts ...llm.Option) *Client {
	if model == "" {
		model = DefaultModel
	}
	c := &Client{
		name:   constant.OPENAI.Name,
		apiURL: strings.TrimRight(cfg.ApiURL, "/"),
		apiKey: cfg.ApiSecret,
		model:  model,
	}
	c.params.Apply(opts...)
	return c
}

func (c *Client) Apply(opts ...llm.Option) { c.params.Apply(opts...) }

// Name provider name
func (c *Client) Name() string {
	return c.name
}

// SupportsDeferLoading reports false — OpenAI relies on automatic
// prefix-prompt caching. Mutating the tools array between turns changes
// the request prefix and invalidates the cache.
func (c *Client) SupportsDeferLoading() bool { return false }

func (c *Client) Model() string     { return c.model }
func (c *Client) SetModel(m string) { c.model = m }

// --- API wire types -------------------------------------------------------

// apiMessage mirrors the OpenAI chat-completions message shape.
//
// Content is intentionally NOT tagged omitempty: OpenAI validates the
// request body with strict deserialization and rejects an assistant
// message that only carries tool_calls if the `content` field is
// missing. Sending an explicit empty string keeps the field present while
// signalling "no textual content this turn".
//
// OpenAI Chat Completions does NOT surface reasoning content in the
// response — unlike DeepSeek's reasoning_content echo-back, OpenAI
// keeps model reasoning opaque. The Responses API (/v1/responses) does
// surface summaries, but that is a separate endpoint not targeted here.
type apiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
}

type apiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type apiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type apiRequest struct {
	Model           string            `json:"model"`
	Messages        []apiMessage      `json:"messages"`
	Temperature     *float64          `json:"temperature,omitempty"`
	TopP            *float64          `json:"top_p,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Stop            []string          `json:"stop,omitempty"`
	Tools           []apiTool         `json:"tools,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
	StreamOptions   *apiStreamOptions `json:"stream_options,omitempty"`
}

// apiStreamOptions tweaks the OpenAI-compatible SSE response. include_usage
// asks the provider to send a final delta carrying the total prompt /
// completion token counts; without it the streaming response would never
// surface a Usage struct.
type apiStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// openaiEffort maps evva effort levels to OpenAI's reasoning_effort.
//
//	0 → ""        (parameter omitted; OpenAI chooses the default)
//	1 → "low"     (evva "low")
//	2 → "medium"  (evva "medium", default — matches OpenAI's default)
//	3 → "high"    (evva "high")
//	4 → "high"    (evva "ultra" — capped; OpenAI has no "xhigh")
//
// Sending an out-of-range value would 400 from OpenAI.
func openaiEffort(effort int) string {
	switch effort {
	case 1:
		return "low"
	case 2:
		return "medium"
	case 3, 4:
		return "high"
	default:
		return ""
	}
}

// stripSamplingForReasoning returns a copy of params with Temperature / TopP
// nil-ed out for reasoning-class models. OpenAI's gpt-5 / o-series reject
// non-default sampling; the older gpt-4 family accepts them.
func stripSamplingForReasoning(p llm.LLMParams, model string) llm.LLMParams {
	if isReasoningModel(model) {
		p.Temperature = nil
		p.TopP = nil
	}
	return p
}

// isReasoningModel reports whether the model is reasoning-class.
//
// TODO(isReasoningModel): when a non-reasoning model (gpt-4*, gpt-3.5*,
// text-*) is added to constant.OPENAI.Models, grow this allowlist.
// The corresponding constant block in pkg/constant/llm.go carries a
// matching comment — update both together.
//
// Conservative: every model evva currently ships in constant.OPENAI is a
// reasoning model (gpt-5 / o-series). Returning true unconditionally is
// correct for v1.2.
func isReasoningModel(model string) bool {
	return true
}

type apiResponse struct {
	Choices []struct {
		Message      apiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
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

// --- Client interface -----------------------------------------------------

func (c *Client) Complete(ctx context.Context, messages []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
	if c.apiKey == "" {
		return llm.Response{}, fmt.Errorf("openai: missing API key (type in /config to setup)")
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
		ReasoningEffort: openaiEffort(params.Effort),
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
	req.Header.Set("authorization", "Bearer "+c.apiKey)

	resp, err := c.params.HTTP().Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: read body: %w", llm.NormalizeErr(err))
	}
	if resp.StatusCode/100 != 2 {
		return llm.Response{}, fmt.Errorf("openai: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if parsed.Error != nil {
		return llm.Response{}, fmt.Errorf("openai: %s: %s", parsed.Error.Type, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return llm.Response{}, fmt.Errorf("openai: empty choices")
	}

	msg := parsed.Choices[0].Message
	out := llm.Response{
		Content: msg.Content,
	}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, &tools.Call{
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: json.RawMessage(call.Function.Arguments),
		})
	}
	if parsed.Usage != nil {
		out.Usage = llm.Usage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		}
		if d := parsed.Usage.PromptTokensDetails; d != nil {
			out.Usage.CacheReadTokens = d.CachedTokens
		}
		if d := parsed.Usage.CompletionTokensDetails; d != nil {
			out.Usage.ReasoningTokens = d.ReasoningTokens
		}
	}
	return out, nil
}

// --- Conversion helpers ---------------------------------------------------

func toAPIMessages(msgs []llm.Message, system string) []apiMessage {
	out := make([]apiMessage, 0, len(msgs)+1)
	if system != "" {
		out = append(out, apiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, apiMessage{Role: "system", Content: m.Content})
		case llm.RoleUser:
			out = append(out, apiMessage{Role: "user", Content: m.Content})
		case llm.RoleAssistant:
			am := apiMessage{Role: "assistant", Content: m.Content}
			for _, c := range m.ToolCalls {
				tc := apiToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(c.Input)
				am.ToolCalls = append(am.ToolCalls, tc)
			}
			out = append(out, am)
		case llm.RoleTool:
			// OpenAI-style: one tool-role message per tool_call_id.
			for _, tr := range m.ToolResults {
				content := tr.Content
				if len(tr.ContentBlocks) > 0 {
					content = llm.RenderContentBlocksAsText(tr.ContentBlocks)
				}
				out = append(out, apiMessage{
					Role:       "tool",
					Content:    content,
					ToolCallID: tr.ID,
				})
			}
		}
	}
	return out
}

func toAPITools(toolSet []tools.Tool) []apiTool {
	if len(toolSet) == 0 {
		return nil
	}
	out := make([]apiTool, 0, len(toolSet))
	for _, t := range toolSet {
		var entry apiTool
		entry.Type = "function"
		entry.Function.Name = t.Name()
		entry.Function.Description = t.Description()
		entry.Function.Parameters = llm.ToolSchema(t)
		out = append(out, entry)
	}
	return out
}
