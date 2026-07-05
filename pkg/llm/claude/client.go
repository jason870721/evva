package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

const (
	DefaultModel     = "claude-sonnet-4-6"
	DefaultMaxTokens = 4096
	apiVersion       = "2023-06-01"
	messagesPath     = "/v1/messages"
)

// anthropicEffort maps evva effort levels to Anthropic's native
// output_config.effort strings. evva's "low" floor is the API's
// "medium" — evva treats "low" as fast-but-still-thinking, not no-
// reasoning, so even the lowest tier sends a non-empty effort.
//
//	0 → ""        (not set)
//	1 → "medium"  (evva "low")
//	2 → "high"    (evva "medium", default)
//	3 → "xhigh"   (evva "high")
//	4 → "max"     (evva "ultra")
func anthropicEffort(effort int) string {
	switch effort {
	case 1:
		return "medium"
	case 2:
		return "high"
	case 3:
		return "xhigh"
	case 4:
		return "max"
	default:
		return ""
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
func New(cfg llm.APIConfig, model string, opts ...llm.Option) *Client {
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

// SupportsDeferLoading currently returns false for all providers.
// The native Anthropic defer_loading + tool_reference pipeline is not yet
// wired in evva's client layer; once toAPITools annotates deferred tools
// with defer_loading:true and the response parser handles tool_reference
// blocks, this can be flipped to true.
func (c *Client) SupportsDeferLoading() bool { return false }

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
	// CacheControl marks this block as a prompt-cache breakpoint. Set only
	// by applyCacheBreakpoints; never on thinking blocks (the API rejects
	// that).
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// cacheControl is Anthropic's prompt-caching marker. The only supported
// type is "ephemeral" (5-minute TTL, refreshed on every hit).
type cacheControl struct {
	Type string `json:"type"`
}

// ephemeralCache is shared across requests — the marker is immutable.
var ephemeralCache = &cacheControl{Type: "ephemeral"}

// blockContentItem is one element of a tool_result's content array.
// Used when a tool returns multimodal content (text + image blocks).
type blockContentItem struct {
	Type   string            `json:"type"`
	Text   string            `json:"text,omitempty"`
	Source *blockImageSource `json:"source,omitempty"`
}

type blockImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// apiOutputConfig carries the Anthropic output_config.effort parameter.
// Maps from evva's user-facing effort levels to Anthropic's native effort
// strings: low→low, medium→medium, high→high, ultra→max.
type apiOutputConfig struct {
	Effort string `json:"effort"`
}

type apiTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type apiRequest struct {
	Model    string       `json:"model"`
	Messages []apiMessage `json:"messages"`
	// System is either a plain string (caching disabled) or a []block with a
	// cache_control marker on its last element (caching enabled). Anthropic
	// accepts both encodings; nil omits the field.
	System        any              `json:"system,omitempty"`
	MaxTokens     int              `json:"max_tokens"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	TopK          *int             `json:"top_k,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Tools         []apiTool        `json:"tools,omitempty"`
	OutputConfig  *apiOutputConfig `json:"output_config,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
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
		MaxTokens:     c.params.MaxTokens,
		Temperature:   c.params.Temperature,
		TopP:          c.params.TopP,
		TopK:          c.params.TopK,
		StopSequences: c.params.StopSequences,
		Tools:         toAPITools(toolSet),
	}
	if c.params.System != "" {
		body.System = c.params.System
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = DefaultMaxTokens
	}

	// Map evva effort levels to Anthropic's native output_config.effort:
	//   low → low, medium → medium, high → high, ultra → max.
	if effort := anthropicEffort(c.params.Effort); effort != "" {
		body.OutputConfig = &apiOutputConfig{Effort: effort}
	}
	if !c.params.PromptCacheDisabled {
		applyCacheBreakpoints(&body)
	}
	return body
}

// applyCacheBreakpoints marks the three prompt-cache breakpoints Anthropic
// needs to reuse work across the agent loop's serial calls (API limit: 4):
//
//  1. last tool        — caches every tool schema (they precede system in
//     the cache hierarchy and change only on deferred-tool discovery);
//  2. system block     — caches the system prompt (static per session);
//  3. last block of the last message — the sliding conversation breakpoint.
//     Call N caches [tools, system, msgs 0..N]; call N+1's prefix matches
//     that entry exactly, so each iteration re-reads everything before it
//     at the cached rate and pays full price only for the newest turn.
//
// The message breakpoint is never placed on a thinking block (the API
// rejects cache_control there); in practice the last message of an agent
// request is a user / tool_result message, so the walk rarely moves.
//
// A prefix under the model's minimum cacheable length (1024 tokens) simply
// doesn't cache — the markers are harmless.
func applyCacheBreakpoints(body *apiRequest) {
	if len(body.Tools) > 0 {
		body.Tools[len(body.Tools)-1].CacheControl = ephemeralCache
	}
	if s, ok := body.System.(string); ok && s != "" {
		body.System = []block{{Type: "text", Text: s, CacheControl: ephemeralCache}}
	}
	if len(body.Messages) == 0 {
		return
	}
	last := &body.Messages[len(body.Messages)-1]
	for i := len(last.Content) - 1; i >= 0; i-- {
		if last.Content[i].Type == "thinking" {
			continue
		}
		last.Content[i].CacheControl = ephemeralCache
		return
	}
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
