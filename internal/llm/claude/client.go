package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnny1110/evva/internal/constant"

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

// budgetForEffort maps user-facing effort levels (via LLMParams.Effort) to
// Anthropic thinking budget_tokens:
//
//	0 → disabled (no extended thinking)
//	1 → 1024   (low)
//	2 → 4096   (medium, default)
//	3 → 8192   (high)
//	4 → 16384  (ultra)
//	5+ → 24576 (future expansion)
//
// The tiers are deliberately coarse — the caller picks "how hard should
// it think" not "exactly how many tokens." Levels 5+ saturate at the max
// useful budget for non-Opus models.
func budgetForEffort(effort int) int {
	switch {
	case effort <= 0:
		return 0
	case effort == 1:
		return 1024
	case effort == 2:
		return 4096
	case effort == 3:
		return 8192
	case effort == 4:
		return 16384
	default:
		return 24576
	}
}

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
	Content   any             `json:"content,omitempty"` // string or []blockContentItem for multimodal tool results
	IsError   bool            `json:"is_error,omitempty"`
	// Thinking block fields. Signature is opaque crypto Anthropic generates
	// alongside each thinking block; it MUST be echoed verbatim if the
	// thinking block precedes a tool_use in a subsequent turn.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// blockContentItem is one element of a tool_result's content array.
// Used when a tool returns multimodal content (text + image blocks).
type blockContentItem struct {
	Type   string             `json:"type"`
	Text   string             `json:"text,omitempty"`
	Source *blockImageSource  `json:"source,omitempty"`
}

type blockImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// apiThinking enables extended thinking. BudgetTokens caps how many tokens
// the model may spend reasoning before producing a reply; it must be less
// than apiRequest.MaxTokens. Temperature/top_p/top_k must equal defaults
// when thinking is enabled — the client clears them on the request.
type apiThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
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
	Thinking      *apiThinking `json:"thinking,omitempty"`
	Stream        bool         `json:"stream,omitempty"`
}

type apiResponse struct {
	Content    []block `json:"content"`
	StopReason string  `json:"stop_reason"`
	Usage      *struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- Client interface -----------------------------------------------------

// buildRequestBody assembles the shared apiRequest used by both Complete and
// Stream. Stream callers set Stream=true on the returned body before
// marshaling. Extended-thinking knobs are applied here so the two paths
// agree byte-for-byte on what gets sent to Anthropic.
func (c *Client) buildRequestBody(messages []llm.Message, toolSet []tools.Tool) apiRequest {
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

	// Extended thinking: map effort tier to a budget. Anthropic requires
	// MaxTokens > BudgetTokens and rejects non-default temperature/top_p/top_k
	// while thinking is on, so clear those knobs and grow MaxTokens if needed.
	if budget := budgetForEffort(c.params.Effort); budget > 0 {
		body.Thinking = &apiThinking{Type: "enabled", BudgetTokens: budget}
		if body.MaxTokens <= budget {
			body.MaxTokens = budget + DefaultMaxTokens
		}
		body.Temperature = nil
		body.TopP = nil
		body.TopK = nil
	}
	return body
}

func (c *Client) Complete(ctx context.Context, messages []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
	if c.apiKey == "" {
		return llm.Response{}, fmt.Errorf("claude: missing API key (type in /config to setup)")
	}

	body := c.buildRequestBody(messages, toolSet)

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
		out      llm.Response
		text     strings.Builder
		thinking strings.Builder
	)

	out.ToolCalls = []*tools.Call{}
	for _, b := range parsed.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "thinking":
			thinking.WriteString(b.Thinking)
			// Only one signature is expected per response; if Anthropic ever
			// emits multiple thinking blocks the last signature wins. The
			// agent only needs *a* valid signature to round-trip.
			if b.Signature != "" {
				out.ThinkingSignature = b.Signature
			}
		case "tool_use":
			tc := &tools.Call{ID: b.ID, Name: b.Name, Input: b.Input}
			out.ToolCalls = append(out.ToolCalls, tc)
		}
	}

	out.Content = text.String()
	out.Thinking = thinking.String()
	if parsed.Usage != nil {
		out.Usage = llm.Usage{
			InputTokens:         parsed.Usage.InputTokens,
			OutputTokens:        parsed.Usage.OutputTokens,
			CacheReadTokens:     parsed.Usage.CacheReadInputTokens,
			CacheCreationTokens: parsed.Usage.CacheCreationInputTokens,
		}
	}
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
			// Thinking block (with signature) MUST precede tool_use when
			// extended thinking is on — Anthropic 400s otherwise. We replay
			// it whenever both pieces are present; signatures from prior
			// turns are passed through verbatim.
			if m.Thinking != "" && m.ThinkingSignature != "" {
				blocks = append(blocks, block{
					Type:      "thinking",
					Thinking:  m.Thinking,
					Signature: m.ThinkingSignature,
				})
			}
			if m.Content != "" {
				blocks = append(blocks, block{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, block{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Input,
				})
			}
			out = append(out, apiMessage{Role: "assistant", Content: blocks})
		case llm.RoleTool:
			// Anthropic carries tool_result blocks inside a user message,
			// not a "tool" role — that role doesn't exist in this API.
			blocks := make([]block, 0, len(m.ToolResults))
			for _, tr := range m.ToolResults {
				b := block{
					Type:      "tool_result",
					ToolUseID: tr.ID,
					IsError:   tr.IsError,
				}
				if len(tr.ContentBlocks) > 0 && !tr.IsError {
					// Multimodal content: emit an array of typed blocks.
					// Anthropic requires is_error tool_results to contain
					// only text, so fall back to the plain string for errors.
					items := make([]blockContentItem, 0, len(tr.ContentBlocks))
					for _, cb := range tr.ContentBlocks {
						switch cb.Type {
						case tools.ContentBlockText:
							items = append(items, blockContentItem{
								Type: "text",
								Text: cb.Text,
							})
						case tools.ContentBlockImage:
							if cb.Image != nil {
								items = append(items, blockContentItem{
									Type: "image",
									Source: &blockImageSource{
										Type:      "base64",
										MediaType: cb.Image.MIMEType,
										Data:      cb.Image.Base64Data,
									},
								})
							}
						}
					}
					b.Content = items
				} else {
					b.Content = tr.Content
				}
				blocks = append(blocks, b)
			}
			out = append(out, apiMessage{Role: "user", Content: blocks})
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
