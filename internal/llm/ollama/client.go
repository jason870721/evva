package ollama

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	DefaultModel = "qwen3.6"
	chatPath     = "/api/chat"
)

// Client implements llm.Client backed by a local Ollama server.
type Client struct {
	name   string
	apiURL string
	model  string
	params llm.LLMParams
}

// New builds an Ollama client from provider config and applies the given options.
// ApiSecret is ignored — Ollama is unauthenticated by default.
func New(cfg config.LLMProviderAPIConfig, model string, opts ...llm.Option) *Client {
	if model == "" {
		model = DefaultModel
	}
	c := &Client{
		name:   constant.OLLAMA.Name,
		apiURL: strings.TrimRight(cfg.ApiURL, "/"),
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
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []apiToolCall `json:"tool_calls,omitempty"`
}

type apiToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
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

type apiOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type apiRequest struct {
	Model    string       `json:"model"`
	Messages []apiMessage `json:"messages"`
	Tools    []apiTool    `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
	Options  *apiOptions  `json:"options,omitempty"`
}

type apiResponse struct {
	Message apiMessage `json:"message"`
	Done    bool       `json:"done"`
	Error   string     `json:"error,omitempty"`
}

// --- Client interface -----------------------------------------------------

func (c *Client) Complete(ctx context.Context, messages []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
	body := apiRequest{
		Model:    c.model,
		Messages: toAPIMessages(messages, c.params.System),
		Tools:    toAPITools(toolSet),
		Stream:   false,
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

	resp, err := c.params.HTTP().Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: read body: %w", llm.NormalizeErr(err))
	}
	if resp.StatusCode/100 != 2 {
		return llm.Response{}, fmt.Errorf("ollama: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	if parsed.Error != "" {
		return llm.Response{}, fmt.Errorf("ollama: %s", parsed.Error)
	}

	out := llm.Response{Content: parsed.Message.Content}
	if len(parsed.Message.ToolCalls) > 0 {
		tc := parsed.Message.ToolCalls[0]
		out.ToolCall = &tools.Call{Name: tc.Function.Name, Input: tc.Function.Arguments}
		// Ollama doesn't issue tool-call ids; synthesize one so the agent can pair
		// the eventual tool reply with this request when echoing back.
		out.ToolID = newToolID()
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
				var tc apiToolCall
				tc.Function.Name = m.ToolCall.Name
				tc.Function.Arguments = m.ToolCall.Input
				am.ToolCalls = []apiToolCall{tc}
			}
			out = append(out, am)
		case llm.RoleTool:
			out = append(out, apiMessage{Role: "tool", Content: m.Content})
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

func buildOptions(p llm.LLMParams) *apiOptions {
	if p.Temperature == nil && p.TopP == nil && p.TopK == nil && p.MaxTokens == 0 && len(p.StopSequences) == 0 {
		return nil
	}
	return &apiOptions{
		Temperature: p.Temperature,
		TopP:        p.TopP,
		TopK:        p.TopK,
		NumPredict:  p.MaxTokens,
		Stop:        p.StopSequences,
	}
}

func newToolID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "ollama_" + hex.EncodeToString(b[:])
}
