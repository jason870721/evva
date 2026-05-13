package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/llmfactory"
	"github.com/johnny1110/evva/internal/logger"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/pkg/common"
)

// Agent runs a chat loop against an llm.Client, configured by a Profile.
//
// Tool lifecycle (three phases for the model's view of a tool):
//
//  1. ACTIVE — built eagerly in New() and sent (name + description + schema)
//     to the LLM on every Complete call. The model can call them with no
//     preamble.
//
//  2. DEFERRED — listed in the profile's allowlist but NOT built at startup.
//     The model sees them by name only (typically referenced in the system
//     prompt). It must call TOOL_SEARCH to fetch a deferred tool's full
//     schema; TOOL_SEARCH uses toolset.Describe, which reads metadata
//     without building. Construction is intentionally postponed.
//
//  3. RESOLVED — the first time the model actually invokes a deferred tool,
//     the dispatcher calls ResolveTool(name): the tool is built, cached in
//     the active map, executed, and remains available (with its schema sent
//     to the LLM) on every subsequent turn.
//
// builders holds the shared state container toolset.Build threads into
// stateful tool constructors. The TUI and session-persist layer read state
// through it (e.g. agent.Builders().TaskStore().List()).
type Agent struct {
	ID     string
	logger *slog.Logger

	profile Profile

	llm     llm.Client
	session *session.Session

	builders          *toolset.Builders
	active            map[string]tools.Tool
	deferredAllowlist map[tools.ToolName]struct{}
}

// New constructs an agent with a fresh ID, a per-agent logger, and the given
// profile applied. ActiveTools are built immediately; DeferredTools are
// recorded as an allowlist and only built on the first LoadDeferred call.
//
// Returns an error rather than calling log.Fatal so callers (TUI, CLI) decide
// how to handle init failure.
func New(profile Profile) (*Agent, error) {
	ID := common.GenUUID()
	lgr, err := logger.OfAgent("", ID)
	if err != nil {
		return nil, fmt.Errorf("agent: init logger: %w", err)
	}

	builders := &toolset.Builders{}

	activeTools, err := toolset.Build(profile.ActiveTools, builders)
	if err != nil {
		lgr.Error("agent: build active tools failed", "error", err)
		return nil, fmt.Errorf("agent: build active tools: %w", err)
	}
	active := make(map[string]tools.Tool, len(activeTools))
	for _, t := range activeTools {
		active[t.Name()] = t
	}

	deferred := make(map[tools.ToolName]struct{}, len(profile.DeferredTools))
	for _, n := range profile.DeferredTools {
		deferred[n] = struct{}{}
	}

	llmClient, err := llmfactory.Of(profile.LLMProvider, profile.LLMModel, profile.LLMOptions)
	if err != nil {
		return nil, fmt.Errorf("agent: init llm client: %w", err)
	}
	lgr.Info("agent: init llm client success.", "provider", llmClient.Name(), "model", llmClient.Model())

	return &Agent{
		ID:                ID,
		logger:            lgr,
		profile:           profile,
		llm:               llmClient,
		session:           session.New(),
		builders:          builders,
		active:            active,
		deferredAllowlist: deferred,
	}, nil
}

// Send issues a single user turn and returns the assistant response.
// Every currently-built tool (initial actives + any lazily loaded since) is
// sent to the model. Cancellation honors ctx — see llm.ErrInterrupted.
func (a *Agent) Send(ctx context.Context, prompt string) (llm.Response, error) {
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: prompt})

	exposed := a.exposedTools()
	a.logger.Debug("llm call",
		"profile", a.profile.Type.String(),
		"messages", len(a.session.Messages),
		"tools", len(exposed),
		"prompt_bytes", len(prompt),
	)

	resp, err := a.llm.Complete(ctx, a.session.Messages, exposed)
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

// ResolveTool returns the runnable instance for a tool name, building it on
// the fly if it's a still-unmaterialized deferred tool. This is the path the
// tool-call dispatcher takes whenever the LLM invokes a tool by name:
//
//   - If the name is already in the active map (either built at New() or
//     resolved on a previous turn), the cached instance is returned.
//   - Otherwise, if the name is in the deferred allowlist, the tool is built
//     via toolset.Build, cached in active, and returned. Its schema will be
//     advertised to the LLM from the next turn forward.
//   - Otherwise, the name is rejected.
//
// Note: TOOL_SEARCH should NOT call this — it only fetches descriptors via
// toolset.Describe. The build is triggered by the first actual invocation.
func (a *Agent) ResolveTool(name tools.ToolName) (tools.Tool, error) {
	if t, ok := a.active[string(name)]; ok {
		return t, nil
	}
	if _, ok := a.deferredAllowlist[name]; !ok {
		return nil, fmt.Errorf("agent: tool %q not in active set or deferred allowlist", name)
	}
	built, err := toolset.Build([]tools.ToolName{name}, a.builders)
	if err != nil {
		return nil, err
	}
	a.active[built[0].Name()] = built[0]
	return built[0], nil
}

// Tool returns the runnable instance for an already-built tool. Returns
// ok=false for deferred names that have not been resolved yet — call
// ResolveTool when you intend to execute.
func (a *Agent) Tool(name string) (tools.Tool, bool) {
	t, ok := a.active[name]
	return t, ok
}

// DeferredNames returns the canonical list of tool names the profile allows
// to be lazy-loaded. TOOL_SEARCH uses this to know which names it may
// describe (and the system-prompt builder uses it to advertise them).
func (a *Agent) DeferredNames() []tools.ToolName {
	out := make([]tools.ToolName, 0, len(a.deferredAllowlist))
	for n := range a.deferredAllowlist {
		out = append(out, n)
	}
	return out
}

// Session exposes the conversation history for inspection or TUI rendering.
func (a *Agent) Session() *session.Session { return a.session }

// Logger exposes the agent's logger so callers can emit records that share
// the agent's structured context.
func (a *Agent) Logger() *slog.Logger { return a.logger }

// Profile returns the profile this agent was constructed with.
func (a *Agent) Profile() Profile { return a.profile }

// Builders exposes the shared state container so the TUI / session-persist
// layer can read tool state through typed accessors (e.g. TaskStore.List()).
func (a *Agent) Builders() *toolset.Builders { return a.builders }

// exposedTools returns the current set of tools to advertise to the LLM —
// every active tool plus any deferred tools that have been loaded.
func (a *Agent) exposedTools() []tools.Tool {
	out := make([]tools.Tool, 0, len(a.active))
	for _, t := range a.active {
		out = append(out, t)
	}
	return out
}
