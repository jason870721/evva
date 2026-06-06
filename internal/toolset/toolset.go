// Package toolset is the catalog of every tool the agent can construct.
//
// Responsibilities split cleanly across the project:
//
//   - Each internal/tools/<family> package knows how to BUILD its tools and
//     reports the names it owns via Names(). It does NOT know whether those
//     tools are eagerly or lazily loaded — that policy lives one layer up.
//
//   - toolset.Build is the single name → instance resolver. One switch lists
//     every tool the agent supports; auditing the surface = reading this file.
//
//   - toolset.ToolState holds per-agent shared state (e.g. *todo.TodoStore) so
//     stateful tool families can be constructed with the right backing data.
//     The agent constructs one ToolState per agent instance, so two agents
//     built from the same profile get isolated state.
//
//   - The agent (internal/agent) decides WHICH tools to build eagerly
//     (ActiveTools — exposed every turn) vs which to mark as lazy-loadable
//     (DeferredTools — materialized on demand when first invoked).
package toolset

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/mcp"
	"github.com/johnny1110/evva/pkg/observable"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/tools/lsp"
	"github.com/johnny1110/evva/pkg/tools/todo"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// ToolState carries the shared backing state for stateful tool families.
// Accessors are lazy — state is allocated only when the first tool that needs
// it is built. Pass the same *ToolState to every Build call for an agent so
// siblings within a family share state.
//
// Cross-cutting consumers (TUI, session persistence) hold this through the
// agent (via agent.ToolState()) and read state through the typed accessors
// rather than peeking into tool internals.
type ToolState struct {
	todoStore          *todo.TodoStore
	subagentSpawner    meta.SubagentSpawner
	deferredLookup     meta.DeferredLookup
	planController     mode.PlanModeController
	worktreeController mode.WorktreeController
	readTracker        *fs.ReadTracker
	wakeupQueue        *meta.WakeupQueue
	// userPromptQueue carries prompts the user typed while a Run was
	// already in flight. The agent loop drains it between iterations
	// so the conversation stays well-formed (no orphaned tool_calls).
	// Only the root agent's queue is ever populated — subagents
	// have no user input.
	userPromptQueue *UserPromptQueue

	// questionBroker is the back-channel used by the AskUserQuestion tool.
	// Installed once at startup by the host (cmd/evva) via agent.WithQuestionBroker;
	// the broker is shared across agents so any agent's question reaches the TUI.
	questionBroker question.Broker

	// skillRegistry holds the merged catalog of user-installed skills.
	// Installed at startup by the host (cmd/evva) and read by the SKILL tool
	// through its late-bound lookup. Subagents share the pointer via
	// WithSkillRegistry on agent.New. It is an atomic.Pointer because the swarm
	// hot-swaps it at runtime (Agent.ReloadSkills, RP-10) on a different goroutine
	// than the readers (SKILL tool / Skills()).
	skillRegistry atomic.Pointer[skill.Registry]

	// cfg is the per-agent runtime configuration. Installed via SetConfig
	// after agent construction; tools that need runtime settings (web,
	// fs/glob, dev/feedback) read it through Config() at Execute time.
	cfg *config.Config

	// daemonState is the unified catalog of every background unit — bash
	// run_in_background, async subagent, monitor stream. Replaces the
	// previous trio of BgTaskStore + MonitorTaskStore + SpawnGroup.
	// Lazy-allocated on first DaemonState() access.
	daemonState *daemon.DaemonState

	// lspManager is the LSP server manager shared by all lsp_request tool
	// instances built against this ToolState. Set by the agent during New
	// after loading the LSP config; nil when no LSP servers are configured.
	lspManager *lsp.Manager

	// mcpManager holds the discovered MCP server connections. Installed once
	// at startup by agent.New (auto-load) or by the host via
	// agent.WithMcpManager; read by every dynamic mcp__<server>__<tool>
	// factory and by the list_mcp_resources / read_mcp_resource tools.
	// Subagents inherit the pointer via agent.WithMcpManager so MCP tools
	// are reachable from a subagent's tool dispatch. nil when no MCP
	// servers are configured.
	mcpManager *mcp.Manager

	// signalSender carries the callbacks the agent installs in New so
	// tool families can deliver event-driven results without importing
	// internal/agent (no cycle). Filled by SetSignalSender; consumed by
	// the DaemonHost methods below.
	signalSender SignalSender
	// Future: cronService, ...

	// TaskGroup registry — every observable.Store registered here fans its
	// changes into the ToolState's observers. The agent subscribes once
	// in New() and re-emits every change as an event.KindStoreUpdate, so
	// adding a new panel never requires touching the agent layer.
	storesMu  sync.Mutex
	stores    []observable.Store    // store 必可被觀察
	observers []observable.Observer // 觀察者
}

func NewToolState() *ToolState {
	return &ToolState{}
}

// RegisterStore plugs a TaskGroup into the unified change stream. ToolState
// subscribes to store and re-publishes every Change to its own observers.
// Lazy accessors below call this on first allocation, so callers that just
// use the typed accessors get registration for free.
func (s *ToolState) RegisterStore(store observable.Store) {
	if store == nil {
		return
	}
	s.storesMu.Lock()
	s.stores = append(s.stores, store) // add into ToolState.stores
	s.storesMu.Unlock()
	store.Subscribe(s.fanout) // s.fanout 是觀察者
}

// Subscribe registers an observer that receives every Change from every
// registered TaskGroup. The agent uses this to bridge into its event sink.
func (s *ToolState) Subscribe(fn observable.Observer) {
	if fn == nil {
		return
	}
	s.storesMu.Lock()
	s.observers = append(s.observers, fn)
	s.storesMu.Unlock()
}

func (s *ToolState) fanout(c observable.Change) {
	s.storesMu.Lock()
	obsSnapshot := append([]observable.Observer(nil), s.observers...)
	s.storesMu.Unlock()
	for _, fn := range obsSnapshot {
		fn(c) // 將 Change 散給所有觀察 ToolState 的訂閱者
	}
}

// TodoStore returns the todo subsystem's backing store, allocating one on
// first use. The todo_write tool constructed against the same ToolState
// shares it. First-use also registers the store on the change stream so
// the agent's event bridge picks up every todo mutation without per-store
// wiring.
func (s *ToolState) TodoStore() *todo.TodoStore {
	if s.todoStore == nil {
		s.todoStore = todo.NewTodoStore()
		s.RegisterStore(s.todoStore)
	}
	return s.todoStore
}

// SubagentSpawner returns the currently-installed spawner, or nil if none.
// Used as the lookup closure passed into meta.NewAgent at build time so the
// AGENT tool sees a late-bound spawner without an init ordering hazard.
func (s *ToolState) SubagentSpawner() meta.SubagentSpawner {
	return s.subagentSpawner
}

// SetSubagentSpawner installs the spawner the AGENT tool will delegate to.
// The agent layer calls this exactly once, after constructing its Agent
// struct, so the *Agent itself satisfies meta.SubagentSpawner.
func (s *ToolState) SetSubagentSpawner(sp meta.SubagentSpawner) {
	s.subagentSpawner = sp
}

// DeferredLookup returns the currently-installed lookup for TOOL_SEARCH,
// or nil if none. Used as the late-bound lookup passed into
// meta.NewToolSearch.
func (s *ToolState) DeferredLookup() meta.DeferredLookup {
	return s.deferredLookup
}

// SetDeferredLookup installs the lookup TOOL_SEARCH will read from. The
// agent layer calls this exactly once (root agent only); the *Agent itself
// satisfies meta.DeferredLookup.
func (s *ToolState) SetDeferredLookup(d meta.DeferredLookup) {
	s.deferredLookup = d
}

// PlanController returns the currently-installed plan-mode controller,
// or nil if none. The EnterPlanMode / ExitPlanMode tools read through
// this lookup at Execute time so the *Agent can register itself after
// agent.New returns without an init ordering hazard.
func (s *ToolState) PlanController() mode.PlanModeController {
	return s.planController
}

// SetPlanController installs the controller EnterPlanMode / ExitPlanMode
// will mutate. The agent layer calls this exactly once after construction;
// only the root agent satisfies the interface (subagents inherit nil and
// the tools surface a clear error if invoked there).
func (s *ToolState) SetPlanController(c mode.PlanModeController) {
	s.planController = c
}

// WorktreeController returns the currently-installed worktree controller,
// or nil if none. The EnterWorktree / ExitWorktree tools read through
// this lookup at Execute time so the *Agent can install itself after
// agent.New returns without an init ordering hazard.
func (s *ToolState) WorktreeController() mode.WorktreeController {
	return s.worktreeController
}

// SetWorktreeController installs the controller EnterWorktree /
// ExitWorktree will read through. Called once after construction; only
// the root agent satisfies the interface (subagents leave the slot nil
// and the tools surface a clear error if invoked there).
func (s *ToolState) SetWorktreeController(c mode.WorktreeController) {
	s.worktreeController = c
}

// ReadTracker returns the session read-tracker shared by all fs tools,
// allocating one on first use.
func (s *ToolState) ReadTracker() *fs.ReadTracker {
	if s.readTracker == nil {
		s.readTracker = fs.NewReadTracker()
	}
	return s.readTracker
}

// HasWakeupQueue reports whether a WakeupQueue has already been allocated.
// The agent loop uses this to skip the drain call in runs that never built
// the SCHEDULE_WAKEUP tool (avoids the lazy allocation just to peek at an
// empty queue).
func (s *ToolState) HasWakeupQueue() bool { return s.wakeupQueue != nil }

// WakeupQueue returns the SCHEDULE_WAKEUP side-channel, allocating one on
// first use. WakeupTool Enqueue's the prompt here when its sleep finishes;
// the agent loop Drain's the queue at the top of every iteration and
// appends each entry as a RoleUser message.
func (s *ToolState) WakeupQueue() *meta.WakeupQueue {
	if s.wakeupQueue == nil {
		s.wakeupQueue = meta.NewWakeupQueue()
	}
	return s.wakeupQueue
}

// SkillRegistry returns the currently-installed skill catalog, or nil if
// none. The SKILL tool reads through this lookup at Execute time so the
// host can SetSkillRegistry any time before the model invokes it.
func (s *ToolState) SkillRegistry() *skill.Registry {
	return s.skillRegistry.Load()
}

// SetSkillRegistry installs the skill catalog the SKILL tool will read
// from. The host (cmd/evva) calls this once at startup with the merged
// home+workdir registry. Subagents inherit the same pointer via
// agent.WithSkillRegistry.
func (s *ToolState) SetSkillRegistry(r *skill.Registry) {
	s.skillRegistry.Store(r)
}

// QuestionBroker returns the question back-channel, or nil if not installed.
// The AskUserQuestion tool reads through this at Execute time.
func (s *ToolState) QuestionBroker() question.Broker {
	return s.questionBroker
}

// SetQuestionBroker installs the broker the AskUserQuestion tool will block on.
// The agent layer calls this once after construction via WithQuestionBroker.
func (s *ToolState) SetQuestionBroker(b question.Broker) {
	s.questionBroker = b
}

// Config returns the runtime configuration this ToolState was bootstrapped
// with, or nil if the agent never installed one (tests, narrow harnesses).
// Tools that need settings (TavilyAPIKey, FetchMaxBytes, AppHome, etc.)
// read through this accessor instead of importing pkg/config directly.
func (s *ToolState) Config() *config.Config {
	return s.cfg
}

// SetConfig installs the runtime configuration on this ToolState. Called
// by agent.New after the agent finishes constructing so tool factories
// don't have to thread cfg explicitly. Subagents inherit the parent's
// config by getting their own ToolState with the same pointer.
func (s *ToolState) SetConfig(cfg *config.Config) {
	s.cfg = cfg
}

// Workdir returns the configured workdir, or "" if no Config is installed.
// Together with Config() this satisfies the pkg/tools.State interface so
// downstream tool factories can read config + workdir without reaching
// for internal accessors.
func (s *ToolState) Workdir() string {
	if s.cfg == nil {
		return ""
	}
	return s.cfg.WorkDir
}

// SignalSender is the narrow callback bundle the agent installs on the
// ToolState in agent.New so background-task and monitor tools can
// deliver event-driven results without importing internal/agent (which
// would create an import cycle, since internal/agent imports
// internal/toolset). Each callback is set by SetSignalSender; the
// DaemonHost methods below invoke them.
type SignalSender struct {
	// NotifyDaemon wakes the agent loop after any daemon Emits a signal
	// (lifecycle transition or stream event). No-arg by design: the
	// signal queue in DaemonState is the durable backstop, so the wake-up
	// only needs to fire the CAS+runLoop entry. Installed by agent.New;
	// nil-safe in tests.
	NotifyDaemon func()
	RootCtx      func() context.Context
	AgentID      func() string
}

// SetSignalSender installs the signal-delivery callbacks. Called once
// per agent by agent.New; subagents get their own bundle (subagent bg
// results bubble up through the parent's sink, not the parent's
// signalCh — only root signals trigger idle-wake).
func (s *ToolState) SetSignalSender(sender SignalSender) {
	s.signalSender = sender
}

// RootCtx returns the agent-lifetime context the bg / monitor goroutines
// bind to. nil-safe when the agent hasn't installed a sender yet (tests).
func (s *ToolState) RootCtx() context.Context {
	if s.signalSender.RootCtx == nil {
		return context.Background()
	}
	return s.signalSender.RootCtx()
}

// AgentID returns the spawning agent's id, used to label snapshot rows.
// Empty when no sender is installed.
func (s *ToolState) AgentID() string {
	if s.signalSender.AgentID == nil {
		return ""
	}
	return s.signalSender.AgentID()
}

// --- DaemonHost implementation -------------------------------------------

// DaemonState returns the agent's daemon catalog, allocating on first use.
// Registers with the unified change stream so TUI strips + the agent's
// KindStoreUpdate bridge see lifecycle transitions.
//
// The notify closure indirects through s.signalSender at call time rather
// than snapshotting NotifyDaemon at construction. Daemon tools are built
// during toolset.Build (which lazy-allocates the catalog here) before
// agent.New gets to SetSignalSender — a direct snapshot would lock in a
// nil notify and silently swallow every wake-up. Reading dynamically
// matches the pattern used by RootCtx / AgentID below and tolerates any
// init order. Tests that build a ToolState without a sender still get a
// nil notify (wake-up no-ops, drain still works).
func (s *ToolState) DaemonState() *daemon.DaemonState {
	if s.daemonState == nil {
		s.daemonState = daemon.NewState(func() {
			if s.signalSender.NotifyDaemon != nil {
				s.signalSender.NotifyDaemon()
			}
		})
		s.RegisterStore(s.daemonState)
	}
	return s.daemonState
}

// HasDaemonState reports whether a daemon catalog has been allocated. The
// agent loop uses this to short-circuit the drain when no daemon has ever
// been registered (avoids forcing the lazy allocation just to peek at an
// empty queue).
func (s *ToolState) HasDaemonState() bool { return s.daemonState != nil }

// LSPManager returns the LSP server manager, or nil if none is configured.
// The agent installs this during New by loading the LSP config file; when
// no config exists the manager remains nil and lsp_request returns a clean
// error at Execute time.
func (s *ToolState) LSPManager() *lsp.Manager {
	return s.lspManager
}

// SetLSPManager installs the LSP Manager. Called once by the agent during
// construction after loading the LSP config.
func (s *ToolState) SetLSPManager(m *lsp.Manager) {
	s.lspManager = m
}

// McpManager returns the MCP connection manager, or nil if none is
// configured. The dynamic mcp__<server>__<tool> factories and the
// list_mcp_resources / read_mcp_resource tools read through this accessor.
func (s *ToolState) McpManager() *mcp.Manager {
	return s.mcpManager
}

// SetMcpManager installs the MCP manager. Called once by the agent during
// construction (auto-load) or via agent.WithMcpManager. Subagents inherit
// the parent's manager so MCP tools resolve without re-connecting.
func (s *ToolState) SetMcpManager(m *mcp.Manager) {
	s.mcpManager = m
}

// HasUserPromptQueue reports whether a UserPromptQueue has already been
// allocated. The agent loop uses this to skip the drain in runs that
// never had user input queued (avoids forcing the lazy allocation just
// to peek at an empty queue).
func (s *ToolState) HasUserPromptQueue() bool { return s.userPromptQueue != nil }

// UserPromptQueue returns the side-channel for prompts the user typed
// while a Run was in flight, allocating one on first use. The UI's
// submit handler Enqueue's; the agent loop's drainUserPrompts pulls
// every entry and folds them into the session as fresh RoleUser
// messages between iterations.
func (s *ToolState) UserPromptQueue() *UserPromptQueue {
	if s.userPromptQueue == nil {
		s.userPromptQueue = NewUserPromptQueue()
	}
	return s.userPromptQueue
}

// Describe returns the metadata (tools.Descriptor) for a tool name without
// registering the tool with any agent. Internally this constructs a
// throwaway instance just long enough to read its static metadata fields
// — for stateful tools the short-lived backing state is immediately
// garbage-collected.
//
// Use Build when you actually need to call Execute.
func Describe(name tools.ToolName) (tools.Descriptor, error) {
	t, err := pubtoolset.DefaultRegistry().Build(name, &ToolState{})
	if err != nil {
		return tools.Descriptor{}, err
	}
	return tools.Descriptor{
		Name:        t.Name(),
		Description: t.Description(),
		Schema:      t.Schema(),
		Tags:        pubtoolset.TagsFor(name),
		SearchHint:  pubtoolset.HintFor(name),
	}, nil
}

// Build resolves each name to a tool instance via the public default
// Registry. Stateful tools pull their backing state from s; stateless
// tools are package-level singletons.
//
// Unknown names return an error — there is no silent fallback.
//
// External hosts that need to register additional tools should call
// pkg/toolset.DefaultRegistry().Register at startup before agent
// construction.
func Build(names []tools.ToolName, s *ToolState) ([]tools.Tool, error) {
	if s == nil {
		s = NewToolState()
	}
	reg := pubtoolset.DefaultRegistry()
	out := make([]tools.Tool, 0, len(names))
	for _, n := range names {
		t, err := reg.Build(n, s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
