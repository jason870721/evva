package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/johnny1110/evva/internal/constant"
	"io"
	"net/http"
	"strings"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/tools"
)

const (
	DefaultModel = "deepseek-chat"
	chatPath     = "/v1/chat/completions"
)

// Client implements llm.Client backed by DeepSeek's OpenAI-compatible chat API.
type Client struct {
	name   string
	apiURL string
	apiKey string
	model  string
	params llm.LLMParams
}

// New builds a DeepSeek client from provider config and applies the given options.
// Options can be re-applied at runtime via Apply.
func New(cfg config.LLMProviderAPIConfig, model string, opts ...llm.Option) *Client {
	if model == "" {
		model = DefaultModel
	}
	c := &Client{
		name:   constant.DEEPSEEK.Name,
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

func (c *Client) Model() string     { return c.model }
func (c *Client) SetModel(m string) { c.model = m }

// --- API wire types -------------------------------------------------------

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// ReasoningContent is populated by deepseek-reasoner on response only.
	// It is intentionally never set on outbound messages — DeepSeek rejects
	// requests that include reasoning_content in prior assistant turns.
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	ToolCalls        []apiToolCall `json:"tool_calls,omitempty"`
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
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Stop        []string     `json:"stop,omitempty"`
	Tools       []apiTool    `json:"tools,omitempty"`
}

type apiResponse struct {
	Choices []struct {
		Message      apiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- Client interface -----------------------------------------------------

func (c *Client) Complete(ctx context.Context, messages []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
	if c.apiKey == "" {
		return llm.Response{}, fmt.Errorf("deepseek: missing API key")
	}

	body := apiRequest{
		Model:       c.model,
		Messages:    toAPIMessages(messages, c.params.System),
		Temperature: c.params.Temperature,
		TopP:        c.params.TopP,
		MaxTokens:   c.params.MaxTokens,
		Stop:        c.params.StopSequences,
		Tools:       toAPITools(toolSet),
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("deepseek: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, fmt.Errorf("deepseek: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+c.apiKey)

	resp, err := c.params.HTTP().Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("deepseek: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("deepseek: read body: %w", llm.NormalizeErr(err))
	}
	if resp.StatusCode/100 != 2 {
		return llm.Response{}, fmt.Errorf("deepseek: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.Response{}, fmt.Errorf("deepseek: decode response: %w", err)
	}
	if parsed.Error != nil {
		return llm.Response{}, fmt.Errorf("deepseek: %s: %s", parsed.Error.Type, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return llm.Response{}, fmt.Errorf("deepseek: empty choices")
	}

	msg := parsed.Choices[0].Message
	out := llm.Response{
		Content:  msg.Content,
		Thinking: msg.ReasoningContent,
	}
	if len(msg.ToolCalls) > 0 {
		call := msg.ToolCalls[0]
		out.ToolCall = &tools.Call{
			Name:  call.Function.Name,
			Input: json.RawMessage(call.Function.Arguments),
		}
		out.ToolID = call.ID
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
			if m.ToolCall != nil {
				tc := apiToolCall{ID: m.ToolID, Type: "function"}
				tc.Function.Name = m.ToolCall.Name
				tc.Function.Arguments = string(m.ToolCall.Input)
				am.ToolCalls = []apiToolCall{tc}
			}
			out = append(out, am)
		case llm.RoleTool:
			out = append(out, apiMessage{
				Role:       "tool",
				Content:    m.Content,
				ToolCallID: m.ToolID,
			})
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
