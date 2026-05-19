package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnny1110/evva/internal/constant"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/llmfactory"
	"github.com/johnny1110/evva/internal/logger"
	"github.com/johnny1110/evva/internal/permission"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/internal/ui"
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
// toolState holds the shared state container toolset.Build threads into
// stateful tool constructors. The TUI and session-persist layer read state
// through it (e.g. agent.ToolState().TaskStore().List()).
//
// sink is the event consumer (nil => Discard). ParentID is empty for the root
// agent and the root's AgentID for subagents — see Option AsSubagent.
type Agent struct {
	Parent *Agent

	ID     string
	Name   string
	logger *slog.Logger
	status constant.AgentStatus

	profile Profile

	llm     llm.Client
	session *session.Session

	toolState         *toolset.ToolState
	active            map[string]tools.Tool
	deferredAllowlist map[tools.ToolName]struct{}
	exposeTools       []tools.Tool // this is used for the llm call params(sys prompt) only.

	// agentRegistry is the merged catalog of built-in + disk-loaded agent
	// definitions. Subagent spawning routes through it; Phase 6 will also
	// drive the /profile picker off of it. Subagents inherit the parent's
	// registry via WithAgentRegistry on agent.New.
	agentRegistry *AgentRegistry

	// permissionMode is the active stance the gate enforces. Subagents
	// inherit the parent's mode (CLAUDE.md). Set via WithPermissionMode at
	// construction; mutated at runtime by SetPermissionMode (e.g. the TUI's
	// Shift+Tab cycle).
	permissionMode atomic.Value // permission.Mode

	// permissionStore + permissionBroker are shared instances. permissionStore
	// holds project/user/session rules; permissionBroker brokers the
	// approval back-channel between the gate and the TUI. Both are
	// process-wide: one Store + one Broker built in cmd/evva/main.go and
	// inherited by every subagent.
	permissionStore  *permission.Store
	permissionBroker permission.Broker

	sink event.Sink // event to ui

	// maxIters is the agent loop's safety cap. Atomic so the TUI's
	// /config form can mutate it from another goroutine while the loop
	// reads it at iteration boundaries.
	maxIters atomic.Int64

	// effort is the user-facing level name. Defaults to "medium".
	effort string

	asyncMode bool

	// emitMu serializes calls into a.sink.Emit so parallel tool dispatch
	// honors the Sink contract (one agent's events delivered serially).
	emitMu sync.Mutex
	// resolveMu guards a.active during lazy deferred-tool materialization.
	// Parallel tool dispatch resolves up front in the caller's goroutine but
	// subagents may also reach in, so the lock is cheap insurance.
	resolveMu sync.Mutex
	// running is the re-entrancy guard for Run / Continue. CAS-set on
	// entry, cleared on exit. A second caller that finds it already set
	// returns ErrRunInProgress instead of appending another user message
	// — concurrent Run's would corrupt session.Messages (an unanswered
	// assistant tool_calls turn followed by a new user message is an
	// invalid request shape every provider rejects).
	running atomic.Bool
}

// New constructs an agent with a fresh ID, a per-agent logger, and the given
// profile applied. ActiveTools are built immediately; DeferredTools are
// recorded as an allowlist and only built on the first ResolveTool call.
//
// Options run after the agent struct is populated from the profile and before
// the LLM client is constructed, so they can influence either layer.
func New(parent *Agent, profile Profile, opts ...Option) (*Agent, error) {
	ID := common.GenUUID()
	parentID := ""
	if parent != nil {
		parentID = parent.ID
	}
	// init logger
	lgr, err := logger.OfAgent(parentID, ID)
	if err != nil {
		return nil, fmt.Errorf("agent: init logger: %w", err)
	}

	toolState := toolset.NewToolState() // per agent per toolState.
	// expose tools in llm api call, also init at first.
	exposeTools, err := toolset.Build(profile.ActiveTools, toolState)
	if err != nil {
		lgr.Error("agent: build active tools failed", "error", err)
		return nil, fmt.Errorf("build active tools: %w", err)
	}
	active := make(map[string]tools.Tool, len(exposeTools))
	for _, t := range exposeTools {
		active[t.Name()] = t
	}

	deferred := make(map[tools.ToolName]struct{}, len(profile.DeferredTools))
	for _, n := range profile.DeferredTools {
		// empty at first, lazy loading when ResolveTool is called
		deferred[n] = struct{}{}
	}

	cfg := config.Get()

	a := &Agent{
		Parent:            parent,
		ID:                ID,
		logger:            lgr,
		status:            constant.INIT,
		profile:           profile,
		session:           session.New(),
		toolState:         toolState,
		active:            active,
		deferredAllowlist: deferred,
		exposeTools:       exposeTools,
	}
	a.maxIters.Store(int64(cfg.DefaultMaxIterations))
	a.effort = cfg.DefaultEffort
	a.permissionMode.Store(permission.ModeDefault)

	// adapt options params (e.g. name, sink, maxIters..)
	for _, opt := range opts {
		opt(a)
	}

	// Single subscription bridges every store registered on the ToolState
	// (task list, subagent panel, future panels) into the agent's event
	// sink as KindStoreUpdate events.
	bindToolStateEvents(a)

	// Install ourselves as the subagent spawner and the deferred-tool
	// lookup. Only the root agent does this — subagents leave the slots
	// nil, so the corresponding tools (AGENT, TOOL_SEARCH) surface clear
	// errors instead of recursing or exposing the wrong agent's allowlist.
	if !a.IsSubagent() {
		a.toolState.SetSubagentSpawner(a) // only main agent can have spawner.
		a.toolState.SetDeferredLookup(a)  // only main agent can have deferred tool lookup.
	}

	effortOpts := append(profile.LLMOptions, llm.WithEffort(llm.ParseEffort(a.effort)))
	llmClient, err := llmfactory.Of(profile.LLMProvider, profile.LLMModel, effortOpts)
	if err != nil {
		return nil, fmt.Errorf("agent: init llm client: %w", err)
	}
	a.llm = llmClient

	lgr.Info("agent: init success.",
		"parent_id", a.ParentID(),
		"id", a.ID,
		"name", a.Name,
		"profile", a.profile.Type.String(),
		"provider", llmClient.Name(),
		"model", llmClient.Model(),
		"expose_tools", len(exposeTools),
		"max_iters", a.maxIters.Load(),
	)
	return a, nil
}

func (a *Agent) AgentID() string {
	return a.ID
}

// MaxIterations returns the current loop cap. Safe to call from any
// goroutine — backed by an atomic load.
func (a *Agent) MaxIterations() int {
	return int(a.maxIters.Load())
}

// SetMaxIterations updates the loop cap. Takes effect at the next
// iteration boundary (loop.go:74 reads a.maxIters via atomic.Load).
// Values <= 0 are clamped to 1.
func (a *Agent) SetMaxIterations(n int) {
	if n <= 0 {
		n = 1
	}
	a.maxIters.Store(int64(n))
}

// Effort returns the current effort level name.
func (a *Agent) Effort() string { return a.effort }

// SetEffort updates the effort level at runtime. Validates the name,
// applies it to the LLM client, and persists to config.
func (a *Agent) SetEffort(level string) error {
	n := llm.ParseEffort(level)
	if n == 0 {
		return fmt.Errorf("agent: invalid effort level %q", level)
	}
	a.effort = level
	a.llm.Apply(llm.WithEffort(n))
	a.logger.Info("agent: effort set", "level", level)
	return config.Get().SetDefaultEffort(level)
}

// SwitchLLM rebuilds a.llm with a new (provider, model) pair, updates
// a.profile so subagents inherit the new provider, and clears the
// session — provider-specific in-flight state (Anthropic
// ThinkingSignature, DeepSeek reasoning_content) is provider-locked, so
// keeping history across a swap would 400 on the next request.
//
// MUST be called while no Run is in flight. Returns ErrRunInProgress
// when the running guard is set, so the caller can refuse the swap
// instead of racing a.llm reads on the agent loop's goroutine.
func (a *Agent) SwitchLLM(provider constant.LLMProvider, model constant.Model) error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: only the root agent can switch LLM")
	}
	if a.running.Load() {
		return ErrRunInProgress
	}

	newProfile := a.profile
	newProfile.LLMProvider = provider
	newProfile.LLMModel = model
	client, err := llmfactory.Of(provider, model, newProfile.LLMOptions)
	if err != nil {
		return fmt.Errorf("agent: build llm client: %w", err)
	}

	a.profile = newProfile
	a.llm = client
	a.session = session.New()
	a.logger.Info("agent: llm switched", "provider", provider.Name, "model", string(model))
	return nil
}

func (a *Agent) ParentID() string {
	if a.Parent != nil {
		return a.Parent.ID
	}
	return ""
}

// IsSubagent reports whether this agent was constructed with AsSubagent.
// The AGENT tool checks this to enforce the "subagents cannot spawn
// subagents" invariant.
func (a *Agent) IsSubagent() bool { return a.Parent != nil }

func (a *Agent) Status() constant.AgentStatus { return a.status }

func (a *Agent) IsAsync() bool { return a.asyncMode }

// Session exposes the conversation history for inspection or TUI rendering.
func (a *Agent) Session() *session.Session { return a.session }

// Logger exposes the agent's logger so callers can emit records that share
// the agent's structured context.
func (a *Agent) Logger() *slog.Logger { return a.logger }

// Profile returns the profile this agent was constructed with.
func (a *Agent) Profile() Profile { return a.profile }

// Model returns the model id the agent's LLM client is bound to.
// Wraps llm.Client.Model() so the ui.Controller interface stays
// independent of the llm package. Empty when no client is attached.
func (a *Agent) Model() string {
	if a.llm == nil {
		return ""
	}
	return a.llm.Model()
}

// ToolState exposes the shared state container so the TUI / session-persist
// layer can read tool state through typed accessors (e.g. TaskStore.List()).
func (a *Agent) ToolState() *toolset.ToolState { return a.toolState }

// PermissionMode returns the agent's current permission stance. Safe to
// call from any goroutine.
func (a *Agent) PermissionMode() permission.Mode {
	v := a.permissionMode.Load()
	if v == nil {
		return permission.ModeDefault
	}
	return v.(permission.Mode)
}

// SetPermissionMode updates the agent's permission stance at runtime
// (Shift+Tab cycle from the TUI). Validates the mode; ignores unknown
// values to keep the system in a known-good state.
//
// Mode changes don't propagate to already-spawned subagents — they
// captured the mode at spawn time. New spawns see the updated mode.
func (a *Agent) SetPermissionMode(m permission.Mode) {
	if !m.Valid() {
		return
	}
	a.permissionMode.Store(m)
	a.logger.Info("agent: permission mode set", "mode", string(m))
}

// PermissionStore exposes the shared rule store. Returns nil if the
// caller didn't install one (tests, headless CLI runs).
func (a *Agent) PermissionStore() *permission.Store { return a.permissionStore }

// PermissionBroker exposes the shared approval back-channel.
func (a *Agent) PermissionBroker() permission.Broker { return a.permissionBroker }

// CyclePermissionMode advances the mode in Shift+Tab order and returns
// the new mode name. Implements ui.Controller.
func (a *Agent) CyclePermissionMode() string {
	next := a.PermissionMode().Next()
	a.SetPermissionMode(next)
	return string(next)
}

// PermissionModeName returns the mode as a plain string (ui.Controller
// uses a string-typed interface to avoid importing internal/permission).
func (a *Agent) PermissionModeName() string { return string(a.PermissionMode()) }

// RespondPermission forwards the user's approval choice from the TUI to
// the broker. The id ties back to a single blocked Broker.Request call.
// Returns ui.ErrUnknownPermission if the id is no longer pending (already
// answered or cancelled). Implements ui.Controller.
func (a *Agent) RespondPermission(id string, dec ui.PermissionDecision) error {
	if a.permissionBroker == nil {
		return errors.New("agent: no permission broker installed")
	}
	pd := permission.Decision{Reason: dec.Reason}
	switch dec.Behavior {
	case "allow":
		pd.Behavior = permission.BehaviorAllow
	default:
		pd.Behavior = permission.BehaviorDeny
	}
	if dec.AddRule != nil {
		pd.AddRule = &permission.Rule{
			ToolName: dec.AddRule.ToolName,
			Content:  dec.AddRule.Content,
			Behavior: permission.BehaviorAllow,
			Source:   permission.SourceSession,
		}
	}
	return a.permissionBroker.Respond(id, pd)
}

// Sink returns the agent's event sink. Used by the AGENT tool to wrap with
// BubbleUp when spawning a subagent. Returns event.Discard if no sink was
// installed.
func (a *Agent) Sink() event.Sink {
	if a.sink == nil {
		return event.Discard
	}
	return a.sink
}

// emit sends an event to the agent's sink (no-op if none installed). The
// envelope's AgentID, ParentID, and Time are filled in here so call sites
// only carry the kind-specific payload.
//
// emitMu serializes the call into a.sink.Emit — parallel tool dispatch
// invokes emit from multiple goroutines, but the Sink contract guarantees
// one agent's events are delivered serially.
func (a *Agent) emit(kind event.Kind, build func(*event.Event)) {
	if a.sink == nil {
		return
	}
	e := event.Event{
		Kind:     kind,
		AgentID:  a.ID,
		ParentID: a.ParentID(),
		Time:     time.Now(),
	}
	if build != nil {
		build(&e)
	}
	a.emitMu.Lock()
	a.sink.Emit(e)
	a.emitMu.Unlock()
}
