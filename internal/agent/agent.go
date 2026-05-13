package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/logger"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/pkg/common"
)

// Agent runs a chat loop against an llm.Client, configured by a Profile that
// specifies the system prompt, standalone tools, and skill bundles. The loop
// itself is identical across profiles — only configuration varies. Tool
// dispatch in the loop will land on top of this skeleton; for now Send is a
// single-turn smoke test that simply returns whatever the model emits.
//
// Each Agent owns a dedicated *slog.Logger scoped to its ID. When LOG_DIR is
// set, that logger writes to {LOG_DIR}/{ID}/{ID}.log — one file per agent —
// without polluting stdout. With LOG_DIR unset, logs fall back to stdout.
type Agent struct {
	ID     string
	logger *slog.Logger

	// configuration
	profile Profile

	// llm + history
	llm     llm.Client
	session *session.Session

	// tools registered from profile (standalone + skill-produced)
	tools *tools.Registry
}

// New constructs an agent with a fresh ID, a per-agent logger, and the given
// profile applied. Returns an error rather than calling log.Fatal so callers
// (TUI, CLI) decide how to handle log-init failure. The global slog default
// is intentionally not touched — that's the caller's choice, not a constructor
// side-effect.
//
// If profile.SystemPrompt is set, it is applied to the client via llm.WithSystem.
// An empty SystemPrompt leaves whatever the caller already configured on the
// client untouched — useful when the caller wants full control over the client.
func New(profile Profile) (*Agent, error) {
	// init sessionID(agentID) and logger
	ID := common.GenUUID()
	lgr, err := logger.OfAgent("", ID)
	if err != nil {
		return nil, fmt.Errorf("agent: init logger: %w", err)
	}

	// init tools
	toolRegistry, err := tools.NewRegistry(profile.Tools...)
	if err != nil {
		lgr.Error("agent: init tools registry failed", "error", err)
		return nil, fmt.Errorf("agent: init tools: %w", err)
	}

	// init llm
	llmClient, err := llm.Of(
		profile.LLMProvider,
		profile.LLMModel,
		profile.LLMOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("agent: init llm client failed: %w", err)
	}
	lgr.Info("agent: init llm client success.", "provider", llmClient.Name(), "model", llmClient.Model())

	// build & return agent
	return &Agent{
		ID:      ID,
		logger:  lgr,
		profile: profile,
		llm:     llmClient,
		session: session.New(),
		tools:   toolRegistry,
	}, nil
}

// Send issues a single user turn and returns the assistant response.
// History accumulates in the session so callers can chain follow-up turns.
// Tools from the profile are exposed to the model — when tool dispatch lands,
// resp.ToolCall will drive the next turn; for now callers see it in the response.
// Cancellation is honored via ctx — see llm.ErrInterrupted.
func (a *Agent) Send(ctx context.Context, prompt string) (llm.Response, error) {
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: prompt})

	registered := a.tools.All()
	a.logger.Debug("llm call",
		"profile", a.profile.Type.String(),
		"messages", len(a.session.Messages),
		"tools", len(registered),
		"prompt_bytes", len(prompt),
	)

	resp, err := a.llm.Complete(ctx, a.session.Messages, registered)
	if err != nil {
		a.logger.Error("llm call failed", "err", err)
		return llm.Response{}, err
	}

	a.logger.Debug("llm call ok",
		"content_bytes", len(resp.Content),
		"thinking_bytes", len(resp.Thinking),
		"tool_call", resp.ToolCall != nil,
	)

	a.session.Append(llm.Message{
		Role:     llm.RoleAssistant,
		Content:  resp.Content,
		Thinking: resp.Thinking,
	})
	return resp, nil
}

// Session exposes the conversation history for inspection or TUI rendering.
func (a *Agent) Session() *session.Session { return a.session }

// Logger exposes the agent's logger so callers can emit records that share
// the agent's structured context (agentId, log file routing).
func (a *Agent) Logger() *slog.Logger { return a.logger }

// Profile returns the profile this agent was constructed with.
func (a *Agent) Profile() Profile { return a.profile }
