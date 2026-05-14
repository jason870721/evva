package claude

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
	DefaultModel     = "claude-sonnet-4-6"
	DefaultMaxTokens = 4096
	apiVersion       = "2023-06-01"
	messagesPath     = "/v1/messages"
)

// Client implements llm.Client backed by the Anthropic Messages API.
type Client struct {
	name   string
	apiURL string
	apiKey string
	model  string
	params llm.LLMParams
}

// New builds a Claude client from provider config and applies the given options.
// Options can be re-applied at runtime via Apply.
func New(cfg config.LLMProviderAPIConfig, model string, opts ...llm.Option) *Client {
	if model == "" {
		model = DefaultModel
	}
	c := &Client{
		name:   constant.ANTHROPIC.Name,
		apiURL: strings.TrimRight(cfg.ApiURL, "/"),
		apiKey: cfg.ApiSecret,
		model:  model,
		params: llm.LLMParams{MaxTokens: DefaultMaxTokens},
	}
	c.params.Apply(opts...)
	return c
}

// Apply merges further options at runtime. Safe to call between completions.
func (c *Client) Apply(opts ...llm.Option) { c.params.Apply(opts...) }

// Model returns the model the client is currently bound to.
func (c *Client) Model() string { return c.model }

// Name provider name
func (c *Client) Name() string {
	return c.name
}

// SetModel swaps the active model id.
func (c *Client) SetModel(m string) { c.model = m }

// --- API wire types -------------------------------------------------------

type apiMessage struct {
	Role    string  `json:"role"`
	Content []block `json:"content"`
}

// block is the union of Anthropic content block shapes. Only fields relevant
// to the active Type are populated; the rest are omitted via omitempty.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiRequest struct {
	Model         string       `json:"model"`
	Messages      []apiMessage `json:"messages"`
	System        string       `json:"system,omitempty"`
	MaxTokens     int          `json:"max_tokens"`
	Temperature   *float64     `json:"temperature,omitempty"`
	TopP          *float64     `json:"top_p,omitempty"`
	TopK          *int         `json:"top_k,omitempty"`
	StopSequences []string     `json:"stop_sequences,omitempty"`
	Tools         []apiTool    `json:"tools,omitempty"`
}

type apiResponse struct {
	Content    []block `json:"content"`
	StopReason string  `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- Client interface -----------------------------------------------------

func (c *Client) Complete(ctx context.Context, messages []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
	if c.apiKey == "" {
		return llm.Response{}, fmt.Errorf("claude: missing API key")
	}

	body := apiRequest{
		Model:         c.model,
		Messages:      toAPIMessages(messages),
		System:        c.params.System,
		MaxTokens:     c.params.MaxTokens,
		Temperature:   c.params.Temperature,
		TopP:          c.params.TopP,
		TopK:          c.params.TopK,
		StopSequences: c.params.StopSequences,
		Tools:         toAPITools(toolSet),
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = DefaultMaxTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("claude: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+messagesPath, bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, fmt.Errorf("claude: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := c.params.HTTP().Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("claude: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("claude: read body: %w", llm.NormalizeErr(err))
	}
	if resp.StatusCode/100 != 2 {
		return llm.Response{}, fmt.Errorf("claude: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.Response{}, fmt.Errorf("claude: decode response: %w", err)
	}
	if parsed.Error != nil {
		return llm.Response{}, fmt.Errorf("claude: %s: %s", parsed.Error.Type, parsed.Error.Message)
	}

	var (
		out  llm.Response
		text strings.Builder
	)

	out.ToolCalls = []*tools.Call{}
	for _, b := range parsed.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			tc := &tools.Call{ID: b.ID, Name: b.Name, Input: b.Input}
			out.ToolCalls = append(out.ToolCalls, tc)
		}
	}

	out.Content = text.String()
	return out, nil
}

// --- Conversion helpers ---------------------------------------------------

func toAPIMessages(msgs []llm.Message) []apiMessage {
	out := make([]apiMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			out = append(out, apiMessage{
				Role:    "user",
				Content: []block{{Type: "text", Text: m.Content}},
			})
		case llm.RoleAssistant:
			blocks := []block{}
			if m.Content != "" {
				blocks = append(blocks, block{Type: "text", Text: m.Content})
			}
			if m.ToolCalls != nil {
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, block{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Input,
					})
				}
			}
			out = append(out, apiMessage{Role: "assistant", Content: blocks})
		case llm.RoleTool:
			blocks := []block{}

			if m.ToolCalls != nil {
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, block{
						Type:      "tool_result",
						ToolUseID: tc.ID,
						Content:   m.Content,
						Input:     tc.Input,
					})
				}
			}

			out = append(out, apiMessage{Role: "tool", Content: blocks})
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
		out = append(out, apiTool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: llm.ToolSchema(t),
		})
	}
	return out
}
