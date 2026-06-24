package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/checkpoint"
	"github.com/johnny1110/evva/internal/logger"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/hooks"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/lsp"
	"github.com/johnny1110/evva/pkg/tools/todo"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
	"github.com/johnny1110/evva/pkg/ui"
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
// through it (e.g. agent.ToolState().TodoStore().List()).
//
// sink is the event consumer (nil => Discard). ParentID is empty for the root
// agent and the root's AgentID for subagents — see Option AsSubagent.
type Agent struct {
	Parent *Agent

	ID     string
	Name   string
	logger *slog.Logger
	// logClose owns the per-agent log file when the logger opened one
	// (nil otherwise). Closed in Shutdown — on Windows an open log file
	// blocks deletion of its directory.
	logClose io.Closer
	status   constant.AgentStatus

	profile Profile

	llm     llm.Client
	session *session.Session

	toolState         *toolset.ToolState
	active            map[string]tools.Tool
	deferredAllowlist map[tools.ToolName]struct{}
	exposeTools       []tools.Tool // this is used for the llm call params(sys prompt) only.

	// checkpoints is the per-turn checkpoint/rewind manager: main agent only,
	// nil for subagents or when EnableCheckpoints is off. Also installed as the
	// fs-tool capture sink at construction; /rewind lists and restores through
	// it. See docs/roadmap/PRD/checkpoint-rewind.md.
	checkpoints *checkpoint.Manager

	// agentRegistry is the merged catalog of built-in + disk-loaded agent
	// definitions. Subagent spawning routes through it; the /profile picker
	// drives off of it. Subagents inherit the parent's registry via
	// WithAgentRegistry on agent.New.
	agentRegistry *AgentRegistry

	// activePersona is the current persona's wire name ("evva" / "nono" /
	// ...). Set at construction from profile-resolution; mutated by
	// SwitchProfile. Surfaced through ProfileName() so the TUI status bar
	// can render the active persona dynamically.
	activePersona string

	// skillRefs is the snapshot the host passed in at construction. Stashed
	// so SwitchProfile can rebuild the profile with the same skill catalog
	// without re-walking the disk.
	skillRefs []sysprompt.SkillRef

	// memSnap is the EVVA.md + USER_PROFILE.md snapshot threaded in at
	// construction. Reused by SwitchProfile when rebuilding the system
	// prompt for a new persona (the snapshot itself is stable for the
	// lifetime of the process).
	//
	// memSnapSet records whether a host injected the snapshot via
	// WithMemorySnapshot. When false, New auto-loads from cfg so a host
	// doesn't have to call memdir.Load itself.
	memSnap    memdir.Snapshot
	memSnapSet bool

	// repoMap is the session-open repo-map body (built once from the LSP layer
	// when EnableRepoMap is set, or its glob fallback) — main agent only, ""
	// otherwise. Like memSnap it's a stable snapshot reused by every prompt
	// rebuild (SwitchProfile, MCP fold, …) so a /profile switch keeps the map
	// without re-querying the language server (PRD §5.6: session-open snapshot,
	// no auto-refresh).
	repoMap string

	// memDirOverride, when set (WithMemoryDir), re-homes the agent's writable
	// auto-memory: after the snapshot is resolved, MemoryDir is forced to this
	// path (write carve-out + recall target) and MemoryIndex is cleared — the
	// host owns surfacing that store's index (e.g. the swarm injects it into
	// wake prompts, RP-25), so it must never enter the static system prompt.
	memDirOverride string

	// permissionMode is the active stance the gate enforces. Subagents
	// inherit the parent's mode (CLAUDE.md). Set via WithPermissionMode at
	// construction; mutated at runtime by SetPermissionMode (e.g. the TUI's
	// Shift+Tab cycle).
	permissionMode atomic.Value // permission.Mode

	// planModeState collects every plan-mode-specific bit of session
	// state: the stashed pre-plan-mode, exit/re-entry flags that drive
	// the per-turn attachment system (internal/agent/attachments), and
	// the reminder-cycle / throttle counters. Single ownership of these
	// fields keeps the side-effects of Shift+Tab, EnterPlanMode, and
	// ExitPlanMode consistent — see permission.Transition.
	planModeState *permission.PlanModeState

	// workdir is the agent's logical working directory. Captured once at
	// construction (from cfg.WorkDir if set, otherwise os.Getwd) and
	// mutated only by SwitchWorkdir — invoked by EnterWorktree /
	// ExitWorktree to swap into and out of a `.evva/worktrees/<slug>/`
	// path. Bash `cd` commands change the shell's cwd but never this
	// field; the workdir-bound tools (Bash, fs Read/Write/Edit/Glob)
	// read this value through ToolState at construction and the agent
	// rebuilds them when the workdir changes.
	workdir   string
	workdirMu sync.RWMutex

	// worktreeSession carries the active EnterWorktree-bound session
	// state, or nil if the agent isn't in a worktree. Set by
	// BeginWorktreeSession (called from the EnterWorktree tool) and
	// cleared by EndWorktreeSession (called from ExitWorktree).
	worktreeSession atomic.Pointer[mode.WorktreeSession]

	// cfg is the runtime configuration this agent reads from. Injected via
	// WithConfig at construction; defaults to config.Get() when no option is
	// supplied so the bundled cmd/evva keeps booting with the historical
	// singleton. Downstream apps that want a non-default AppHome pass a
	// Config built by pkg/config.Load.
	cfg *config.Config

	// customTools is the list of (name, factory) pairs the WithCustomTool
	// option collected. Registered on pkg/toolset.DefaultRegistry and
	// appended to ActiveTools before toolset.Build runs. Idempotent across
	// agent constructions — a duplicate registration is a no-op.
	customTools []customToolEntry

	// permissionStore + permissionBroker are shared instances. permissionStore
	// holds project/user/session rules; permissionBroker brokers the
	// approval back-channel between the gate and the TUI. Both are
	// process-wide: one Store + one Broker built in cmd/evva/main.go and
	// inherited by every subagent.
	permissionStore  *permission.Store
	permissionBroker permission.Broker
	questionBroker   question.Broker

	// hookRegistry is the shared, process-wide hooks catalog loaded from
	// settings.json. Inherited by subagents. hookDispatcher is per-agent
	// (its baseFn bakes in the agent's identity), built in New from the
	// shared registry.
	hookRegistry   *hooks.Registry
	hookDispatcher *hooks.Dispatcher

	// mcpManagerSet records whether a host injected an MCP manager via
	// WithMcpManager (even a nil one). When false, New auto-loads the
	// manager from settings.json — mirroring memSnapSet's auto-load gate.
	mcpManagerSet bool

	sink event.Sink // event to ui

	// inboxDrainer, when set, is polled at every loop iteration boundary so a
	// busy agent can fold an incoming message into its current run (the
	// generalisation of the background-task drain). Nil is a no-op — single-
	// agent behaviour is unaffected. Set via WithInboxDrainer.
	inboxDrainer Drainer

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

	// dreaming guards the background memory-consolidation pass: maybeFireDream
	// CAS-es it true before launching the dream goroutine and runDream clears
	// it on exit, so a process never runs two dreams at once (the on-disk lock
	// guards across processes). lastDreamScanAt is the in-memory scan-throttle
	// cursor the dream gate reads/advances; touched only by the loop goroutine
	// that calls maybeFireDream, so it needs no lock. Both are main-agent-only.
	dreaming        atomic.Bool
	lastDreamScanAt time.Time

	// runStartUsage is the session-usage snapshot taken when runLoop entered
	// (RP-28): done() reports the run's own token cost as the delta from it
	// on the RunEnd event. Written and read only by the goroutine holding
	// the `running` CAS.
	runStartUsage llm.Usage

	// sessionStarted latches the SessionStart hook: the first Run() call
	// fires it; Continue() (resume, iter-limit re-entry) does not.
	// Unlike a sync.Once it is resettable — ClearSession arms it again
	// with sessionStartSource = "clear" so hooks can tell a fresh boot
	// from a mid-session wipe. sessionStartSource is a plain field, but
	// only mutated while no Run is in flight (the a.session pattern).
	sessionStarted     atomic.Bool
	sessionStartSource string

	// sessionCreatedAt is the wall-clock time the current session began
	// (first persistSession call after agent creation or after a /resume
	// load). Used to populate Snapshot.CreatedAt so the resume picker
	// can show "first saved" alongside the file's mtime ("last activity").
	// Reset to zero by ResumeSession so the next persist picks up the
	// loaded snapshot's CreatedAt instead.
	sessionCreatedAt time.Time

	// signalCh + rootCtx carry the event-driven side of the agent
	// (Phase 16). Background bash tasks and Monitor goroutines write
	// terminal results / stream events through signalCh; the signal
	// pump goroutine started in New listens for them and either
	// CAS-acquires a.running to spawn a new runLoop (idle-wake) or
	// relies on the running loop's iteration-boundary drain.
	//
	// rootCtx is the agent-lifetime context — set via WithRootContext
	// (or context.Background() when omitted). The pump exits when this
	// context is cancelled; long-lived bg / monitor goroutines bind
	// here rather than the per-call ctx so they survive past the LLM
	// call that spawned them.
	signalCh   chan AgentSignal
	rootCtx    context.Context
	rootCancel context.CancelFunc
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

	deferred := make(map[tools.ToolName]struct{}, len(profile.DeferredTools))
	for _, n := range profile.DeferredTools {
		// empty at first, lazy loading when ResolveTool is called
		deferred[n] = struct{}{}
	}

	toolState := toolset.NewToolState() // per agent per toolState.

	a := &Agent{
		Parent:            parent,
		ID:                ID,
		status:            constant.INIT,
		profile:           profile,
		session:           session.New(),
		toolState:         toolState,
		deferredAllowlist: deferred,
		signalCh:          make(chan AgentSignal, signalChanCap),
	}
	a.permissionMode.Store(permission.ModeDefault)
	a.planModeState = permission.NewPlanModeState()
	if wd, err := os.Getwd(); err == nil {
		a.workdir = wd
	}

	// adapt options params (e.g. name, sink, cfg, maxIters..)
	for _, opt := range opts {
		opt(a)
	}

	// Default rootCtx if no caller installed one. Production hosts
	// (cmd/evva, friday, etc.) thread their cancellable ctx via
	// WithRootContext so Shutdown() can tear the pump down with them.
	if a.rootCtx == nil {
		a.rootCtx, a.rootCancel = context.WithCancel(context.Background())
	}

	// Backward compat: callers that don't pass WithConfig boot against the
	// process-wide default Config. Downstream apps wanting a custom AppHome
	// pass their own *config.Config via WithConfig.
	if a.cfg == nil {
		a.cfg = config.Get()
	}
	// If the caller injected a Config with a non-empty WorkDir (the
	// AgentTool isolation path does this so a subagent runs inside a
	// pre-created worktree), that value wins over the os.Getwd captured
	// before options ran. Symmetric: cfg.WorkDir gets backfilled when
	// it was empty, so toolset accessors see the same path regardless
	// of which side wrote it first.
	if a.cfg.WorkDir != "" {
		a.workdir = a.cfg.WorkDir
	} else {
		a.cfg.WorkDir = a.workdir
	}

	// Plumb cfg into the ToolState so tools that need runtime settings
	// (web tools, fs glob, dev/feedback) read through the state pointer
	// rather than a global accessor. Must happen before toolset.Build so
	// the factories see the configured cfg.
	a.toolState.SetConfig(a.cfg)

	// Logger picks up LogDir / LogLevel / LogFormat from a.cfg.
	lgr, logClose, err := logger.OfAgent(a.cfg, parentID, ID)
	if err != nil {
		return nil, fmt.Errorf("agent: init logger: %w", err)
	}
	a.logger = lgr
	a.logClose = logClose

	// Checkpoint/rewind (main agent only, when enabled): construct the manager
	// and install it as the fs-tool capture sink BEFORE toolset.Build so the
	// edit/write factories pick it up. NewManager returns nil for an empty
	// workdir/session; we install ONLY a non-nil sink so the tools' nil-check
	// stays honest (a typed-nil interface would defeat it).
	if !a.IsSubagent() && a.cfg.GetEnableCheckpoints() {
		ret := checkpoint.Retention{MaxCount: a.cfg.GetCheckpointMaxPerSession()}
		if mgr := checkpoint.NewManager(a.workdir, a.ID, ret, a.logger); mgr != nil {
			a.checkpoints = mgr
			a.toolState.SetCheckpointSink(mgr)
		}
	}

	// Auto-load the skill registry from disk if no override was injected
	// via WithSkillRegistry. Downstream apps that want a programmatic-only
	// catalog pre-install their own (skill.NewRegistry + Add) before
	// agent.New runs; passing an empty registry disables auto-load. Done
	// here (not in cmd/evva/main.go) so every host — bundled CLI, SDK
	// consumers, examples — gets disk skills for free.
	// A registry installed via WithSkillRegistry (swarm members, SDK hosts) is the
	// explicit, authoritative catalog for THIS agent; its absence means "auto-load
	// from disk". Capture which case we're in before the auto-load so the skillRefs
	// wiring below can tell an explicit injection from the default path.
	injectedSkills := a.toolState.SkillRegistry() != nil
	if !injectedSkills {
		reg := loadDiskSkillRegistry(a.cfg)
		for _, w := range reg.Warnings {
			lgr.Warn("skill: load", "msg", w)
		}
		a.toolState.SetSkillRegistry(reg)
		if a.skillRefs == nil {
			a.skillRefs = refsFromRegistry(reg)
		}
	}
	// An explicitly injected registry must drive BOTH the skill tool AND the prompt's
	// # Skills section. WithSkillRegistry only sets the tool side, leaving a.skillRefs
	// nil — which makes resolveMainProfileWithExtra fall back to the cfg-GLOBAL catalog
	// (a different source than the tool). Derive the prompt refs from the injected
	// registry instead, coercing an empty one to a non-nil empty slice so it advertises
	// "no skills" rather than inheriting the global set (RP-10-2). Scoped to explicit
	// injection so evva's auto-load path stays bit-identical.
	if injectedSkills && a.skillRefs == nil {
		refs := refsFromRegistry(a.toolState.SkillRegistry())
		if refs == nil {
			refs = []sysprompt.SkillRef{}
		}
		a.skillRefs = refs
	}

	// Auto-load the LSP config and install the Manager on ToolState.
	// When no config file exists the manager stays nil and the lsp_request
	// tool returns a clean error at Execute time. Only the root agent gets
	// an LSP manager — subagents inherit nil.
	if !a.IsSubagent() && a.toolState.LSPManager() == nil {
		if lspCfg, lspErr := lsp.LoadConfig(a.workdir, a.cfg.AppHome); lspErr != nil {
			lgr.Warn("lsp: config load", "err", lspErr)
		} else if lspCfg != nil && len(lspCfg.Servers) > 0 {
			rootURI := "file://" + a.workdir
			mgr := lsp.NewManager(lspCfg.Servers, rootURI, lgr)
			mgr.SetDaemonState(a.toolState.DaemonState())
			a.toolState.SetLSPManager(mgr)
			lgr.Info("lsp: manager started", "servers", len(lspCfg.Servers))
		}
	}

	// Auto-load the EVVA.md + USER_PROFILE.md snapshot when no host injected
	// one via WithMemorySnapshot. SwitchProfile reuses a.memSnap to rebuild a
	// persona's prompt, so loading it here means a host no longer has to call
	// memdir.Load itself. Warnings surface on the agent logger (matching the
	// skill-load warnings above), not the host's stderr.
	if !a.memSnapSet {
		a.memSnap = memdir.Load(a.workdir, a.cfg.AppHome, a.cfg.GetEnableAutoMemory())
		for _, w := range a.memSnap.Warnings {
			lgr.Warn("memory: load", "msg", w)
		}
	}
	// Re-home the writable memory store when the host asked for it
	// (WithMemoryDir). Applied to BOTH snapshot paths — auto-loaded and
	// host-injected — and after them, so the override always wins. Clearing
	// MemoryIndex keeps the re-homed store's index out of the static prompt:
	// the host surfaces it on its own channel (cache discipline, RP-25).
	if a.memDirOverride != "" {
		a.memSnap.MemoryDir = a.memDirOverride
		a.memSnap.MemoryIndex = ""
	}

	// Auto-load the MCP manager (settings.json) unless a host injected one
	// via WithMcpManager. Mirrors the skill / LSP auto-load above: one disk
	// read at startup, zero cost when nothing is configured. Done here (not
	// in cmd/evva) so every host — bundled CLI, SDK consumers, examples —
	// gets MCP for free. foldMcpIntoProfile then re-renders the MAIN prompt
	// so discovered mcp__<server>__<tool> names appear in the deferred
	// catalog and the allowlist before the LLM client is built below.
	a.autoLoadMcp(lgr)
	a.foldMcpIntoProfile()

	// An explicitly injected skill registry (WithSkillRegistry — swarm members) must
	// also drive the INITIAL prompt's # Skills section, not just later re-resolves.
	// pkg/agent.New resolved the profile BEFORE this option was applied (skills=nil →
	// cfg-global fallback), so re-render now with the injected refs (RP-10-2). When
	// MCP was discovered, foldMcpIntoProfile already re-rendered with a.skillRefs, so
	// skip the duplicate. Scoped to explicit injection so evva's path is untouched.
	if injectedSkills && a.profile.Type == MAIN && len(a.mcpDiscoveredNames()) == 0 {
		persona := a.activePersona
		if persona == "" {
			persona = "evva"
		}
		if aug, perr := resolveMainProfileWithExtra(a.cfg, a.agentRegistry, persona, a.skillRefs, a.memSnap, baseLLMOptions(a.profile.LLMOptions), a.profile.LLMProvider, a.profile.LLMModel, nil, a.repoMap); perr == nil {
			a.profile.SystemPrompt = aug.SystemPrompt
			a.profile.LLMOptions = aug.LLMOptions
		} else {
			lgr.Warn("agent: re-render prompt for injected skills", "err", perr)
		}
	}

	// Build the session-open repo map and fold it into the MAIN prompt — the
	// repo-map analog of foldMcpIntoProfile, run after the LSP manager + memory
	// + MCP are wired so the re-render carries all of them. No-op (and prompt
	// byte-identical) when EnableRepoMap is off.
	a.buildRepoMap(lgr)

	// Register any custom tools the caller staged via WithCustomTool, and
	// extend the profile's active list so they show up to the LLM. Duplicate
	// registrations are silently absorbed — agents constructed back-to-back
	// against the same custom catalog re-use the first registration.
	// Custom tools (WithCustomTool) extend the profile's active list. The same
	// merge runs on every active-set rebuild via activeToolNames, so a reload
	// never drops downstream-injected tools.
	activeNames, err := a.activeToolNames(profile.ActiveTools)
	if err != nil {
		return nil, err
	}

	// Expose tools to the llm api call, also init at first.
	exposeTools, err := toolset.Build(activeNames, toolState)
	if err != nil {
		lgr.Error("agent: build active tools failed", "error", err)
		return nil, fmt.Errorf("build active tools: %w", err)
	}
	active := make(map[string]tools.Tool, len(exposeTools))
	for _, t := range exposeTools {
		active[t.Name()] = t
	}
	a.active = active
	a.exposeTools = exposeTools

	// Apply cfg-derived defaults for fields the options didn't already set.
	// Zero values act as the "unset" sentinel.
	if a.maxIters.Load() == 0 {
		a.maxIters.Store(int64(a.cfg.DefaultMaxIterations))
	}
	if a.effort == "" {
		a.effort = a.cfg.DefaultEffort
	}

	// Single subscription bridges every store registered on the ToolState
	// (task list, subagent panel, future panels) into the agent's event
	// sink as KindStoreUpdate events.
	bindToolStateEvents(a)

	// Bind the ToolState's signal hook to the agent's signal channel so
	// background-task and monitor tools can deliver results without
	// reaching into internal/agent directly. The ToolState exposes this
	// as a narrow callback set; the agent owns the chan.
	a.toolState.SetSignalSender(toolset.SignalSender{
		NotifyDaemon: func() { a.SendSignal(AgentSignal{Kind: SignalDaemon}) },
		NotifyAlarm:  func() { a.SendSignal(AgentSignal{Kind: SignalAlarm}) },
		RootCtx:      func() context.Context { return a.rootCtx },
		AgentID:      func() string { return a.ID },
	})

	// Spawn the signal pump goroutine. Lives for the agent's rootCtx
	// lifetime; cancelled via Shutdown() or by the caller cancelling
	// the ctx threaded via WithRootContext.
	go a.signalPump()

	// Re-arm durable alarms from a previous session (root agent only, and only
	// when this profile admits the alarm tools). Future alarms get fresh timers;
	// alarms missed while the process was down are Enqueue'd on the WakeupQueue
	// so they surface on the NEXT run rather than autonomously starting one
	// before the host's UI is live. Done here — after the signal pump is up, so
	// a future alarm that fires can wake the loop. Errors are non-fatal.
	if !a.IsSubagent() && profileAllowsAlarm(a.profile) {
		sched := a.toolState.AlarmScheduler()
		if pastDue, err := sched.Rearm(); err != nil {
			a.logger.Warn("alarm.rearm", "err", err)
		} else {
			for _, f := range pastDue {
				a.toolState.WakeupQueue().Enqueue(f.Message())
			}
			if len(pastDue) > 0 {
				a.logger.Info("alarm.rearm.pastdue", "count", len(pastDue))
			}
		}
	}

	// Install ourselves as the subagent spawner and the deferred-tool
	// lookup. Only the root agent does this — subagents leave the slots
	// nil, so the corresponding tools (AGENT, TOOL_SEARCH, ENTER/EXIT_PLAN_MODE)
	// surface clear errors instead of recursing or exposing the wrong
	// agent's allowlist.
	if !a.IsSubagent() {
		a.toolState.SetSubagentSpawner(a)    // only main agent can have spawner.
		a.toolState.SetDeferredLookup(a)     // only main agent can have deferred tool lookup.
		a.toolState.SetPlanController(a)     // only main agent can flip plan mode.
		a.toolState.SetWorktreeController(a) // only main agent can enter/exit a worktree.
	}
	// Install the default permission + question brokers and the sink bridge
	// when the host didn't supply its own. Root-only: subagents inherit the
	// root's wired brokers via spawn.go. Must run before SetQuestionBroker
	// below so the broker it plumbs is the finalized one.
	wireBrokers(a)

	// Build the per-agent hook dispatcher. Each agent (root + every
	// subagent) builds its own dispatcher so the base-payload factory
	// bakes in that agent's identity. The *Registry is shared — loaded
	// once, consumed by the whole agent tree. nil-safe: a nil registry
	// yields a dispatcher whose Has() always returns false.
	a.hookDispatcher = hooks.NewDispatcher(
		a.hookRegistry,
		a.logger,
		a.hookBaseFactory,
		a.workdir,
	)

	// Question broker is process-wide and shared by root and subagents alike.
	a.toolState.SetQuestionBroker(a.questionBroker)

	// Read from a.profile (not the param) so foldMcpIntoProfile's prompt
	// re-render — which swapped a.profile.LLMOptions with the MCP-aware
	// WithSystem — is the prompt the client is built with.
	effortOpts := append(a.profile.LLMOptions, llm.WithEffort(llm.ParseEffort(a.effort)))
	effortOpts = append(effortOpts, runtimeLLMOptions(a.cfg)...)
	llmClient, err := buildLLMClient(a.cfg, a.profile.LLMProvider, a.profile.LLMModel, effortOpts)
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

// Shutdown cancels the agent's root context, which:
//   - tears down the signal pump goroutine,
//   - propagates cancellation to every detached background bash task
//     and Monitor goroutine that bound to RootCtx at spawn time.
//
// Safe to call multiple times — the underlying CancelFunc is idempotent.
// Call this from the host's process-exit path (cmd/evva does so when
// its TUI ctx is cancelled) to avoid leaking goroutines that outlive
// the session.
func (a *Agent) Shutdown() {
	// Close MCP sessions before cancelling the root context so stdio
	// subprocess servers exit cleanly (SDK sends Cancel, closes stdin)
	// rather than waiting on EOF. Root-only: subagents share the parent's
	// manager and must not tear it down. Idempotent.
	if !a.IsSubagent() {
		if mgr := a.toolState.McpManager(); mgr != nil {
			mgr.Shutdown()
		}
	}
	// Stop pending alarm timers so none fires mid-teardown. Durable alarms stay
	// on disk and are re-armed next start; only allocated when alarms were used.
	if a.toolState.HasAlarmScheduler() {
		a.toolState.AlarmScheduler().Stop()
	}
	if a.rootCancel != nil {
		a.rootCancel()
	}
	// Last: release the log file so its directory is deletable (Windows
	// refuses while open). A background goroutine that logs after this
	// hits a closed-file write error, which slog swallows.
	if a.logClose != nil {
		_ = a.logClose.Close()
	}
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
	return a.cfg.SetDefaultEffort(level)
}

// LLMTemperature returns the current runtime temperature or nil (provider default).
func (a *Agent) LLMTemperature() *float64 { return a.cfg.LLMTemperature() }

// SetLLMTemperature updates the session-only temperature. nil unsets
// (provider default). Validates 0 ≤ v ≤ 2. Applies to live LLM client
// immediately. Not persisted to disk.
func (a *Agent) SetLLMTemperature(v *float64) error {
	if err := a.cfg.SetLLMTemperature(v); err != nil {
		return err
	}
	if v == nil {
		a.llm.Apply(llm.UnsetTemperature())
	} else {
		a.llm.Apply(llm.WithTemperature(*v))
	}
	return nil
}

// LLMTopK returns the current runtime top_k or nil (provider default).
func (a *Agent) LLMTopK() *int { return a.cfg.LLMTopK() }

// SetLLMTopK updates the session-only top_k. nil unsets (provider default).
// Validates v > 0. Applies to live LLM client immediately. Not persisted.
func (a *Agent) SetLLMTopK(v *int) error {
	if err := a.cfg.SetLLMTopK(v); err != nil {
		return err
	}
	if v == nil {
		a.llm.Apply(llm.UnsetTopK())
	} else {
		a.llm.Apply(llm.WithTopK(*v))
	}
	return nil
}

// LLMTopP returns the current runtime top_p or nil (provider default).
func (a *Agent) LLMTopP() *float64 { return a.cfg.LLMTopP() }

// SetLLMTopP updates the session-only top_p. nil unsets (provider default).
// Validates 0 ≤ v ≤ 1. Applies to live LLM client immediately. Not persisted.
func (a *Agent) SetLLMTopP(v *float64) error {
	if err := a.cfg.SetLLMTopP(v); err != nil {
		return err
	}
	if v == nil {
		a.llm.Apply(llm.UnsetTopP())
	} else {
		a.llm.Apply(llm.WithTopP(*v))
	}
	return nil
}

// activeToolNames returns base (a profile's ActiveTools) extended with every
// WithCustomTool name, (re-)ensuring each custom factory is registered on the
// pkg/toolset DefaultRegistry. New builds the initial active set through it, and
// every active-set REBUILD (SwitchProfile, ResumeSnapshot, SwitchWorkdir) MUST
// too: a rebuild that uses profile.ActiveTools alone silently drops downstream-
// injected tools — e.g. a swarm member losing its task_*/send_message tools
// after a restart-resume, leaving it deadlocked with only its profile tools.
func (a *Agent) activeToolNames(base []tools.ToolName) ([]tools.ToolName, error) {
	if len(a.customTools) == 0 {
		return base, nil
	}
	reg := pubtoolset.DefaultRegistry()
	out := append([]tools.ToolName{}, base...)
	for _, ct := range a.customTools {
		if !reg.Has(ct.name) {
			if err := reg.Register(ct.name, ct.factory); err != nil {
				return nil, fmt.Errorf("agent: register custom tool %q: %w", ct.name, err)
			}
		}
		out = append(out, ct.name)
	}
	return out, nil
}

// SwitchProfile rebuilds the agent for a new persona — different system
// prompt, different active/deferred tool lists, fresh session. Mirrors
// SwitchLLM's running-guard discipline.
//
// The toolState is preserved so observable subscriptions (the TUI
// panels) keep working across the swap; only the TodoStore is cleared
// since its entries belong to a single session. The LLM client is
// rebuilt because the new profile carries its own WithSystem option.
//
// MUST be called while no Run is in flight. Returns ErrRunInProgress
// when the running guard is set. Persists the new persona name to
// evva-config.yml so the next launch boots into it.
func (a *Agent) SwitchProfile(name string) error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: only the root agent can switch profile")
	}
	if a.running.Load() {
		return ErrRunInProgress
	}
	if name == "" {
		return fmt.Errorf("agent: profile name is required")
	}

	// Thread the live MCP catalog through the rebuild so the new persona's
	// prompt advertises mcp__<server>__<tool> names and the deferred
	// allowlist still admits them after the switch (acceptance A16). The
	// *mcp.Manager lives on the reused toolState, so no re-connect happens.
	newProfile, err := resolveMainProfileWithExtra(a.cfg, a.agentRegistry, name, a.skillRefs, a.memSnap, baseLLMOptions(a.profile.LLMOptions), a.cfg.DefaultProvider, a.cfg.DefaultModel, a.mcpDiscoveredNames(), a.repoMap)
	if err != nil {
		return err
	}

	// Rebuild the active-tool map from the new profile. Reuses the
	// existing toolState so observers (UI panels) keep their subscriptions.
	// activeToolNames re-merges custom tools so the switch doesn't drop them.
	activeNames, err := a.activeToolNames(newProfile.ActiveTools)
	if err != nil {
		return err
	}
	exposeTools, err := toolset.Build(activeNames, a.toolState)
	if err != nil {
		return fmt.Errorf("agent: build active tools: %w", err)
	}
	active := make(map[string]tools.Tool, len(exposeTools))
	for _, t := range exposeTools {
		active[t.Name()] = t
	}
	deferred := make(map[tools.ToolName]struct{}, len(newProfile.DeferredTools))
	for _, n := range newProfile.DeferredTools {
		deferred[n] = struct{}{}
	}

	effortOpts := append(newProfile.LLMOptions, llm.WithEffort(llm.ParseEffort(a.effort)))
	effortOpts = append(effortOpts, runtimeLLMOptions(a.cfg)...)
	client, err := buildLLMClient(a.cfg, newProfile.LLMProvider, newProfile.LLMModel, effortOpts)
	if err != nil {
		return fmt.Errorf("agent: build llm client: %w", err)
	}

	a.profile = newProfile
	a.active = active
	a.deferredAllowlist = deferred
	a.exposeTools = exposeTools
	a.llm = client
	a.session = session.New()
	a.activePersona = name
	a.toolState.TodoStore().Clear()

	a.logger.Info("agent: profile switched", "persona", name, "provider", client.Name(), "model", client.Model())
	return a.cfg.SetDefaultProfile(name)
}

// baseLLMOptions strips any prior WithSystem entries from opts so
// ResolveMainProfile's freshly-appended WithSystem is the only one in
// play. The current profile's options carry the *previous* persona's
// system prompt — re-using them without filtering would let the old
// prompt clobber the new one when llm.Apply runs the options in order.
func baseLLMOptions(opts []llm.Option) []llm.Option {
	if len(opts) == 0 {
		return nil
	}
	out := make([]llm.Option, 0, len(opts))
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		var probe llm.LLMParams
		opt(&probe)
		if probe.System != "" {
			continue
		}
		out = append(out, opt)
	}
	return out
}

// runtimeLLMOptions returns sampling-parameter Options for any
// temperature / top_k / top_p currently set on the runtime config.
// nil (unset) values are skipped so the provider default is used.
func runtimeLLMOptions(cfg *config.Config) []llm.Option {
	var opts []llm.Option
	if t := cfg.LLMTemperature(); t != nil {
		opts = append(opts, llm.WithTemperature(*t))
	}
	if k := cfg.LLMTopK(); k != nil {
		opts = append(opts, llm.WithTopK(*k))
	}
	if p := cfg.LLMTopP(); p != nil {
		opts = append(opts, llm.WithTopP(*p))
	}
	return opts
}

// applyRuntimeLLMParams pushes the current runtime sampling params
// onto the live LLM client. Called after SwitchLLM so a new client
// picks up any session-level overrides.
func (a *Agent) applyRuntimeLLMParams() {
	if a.cfg == nil {
		return
	}
	if t := a.cfg.LLMTemperature(); t != nil {
		a.llm.Apply(llm.WithTemperature(*t))
	}
	if k := a.cfg.LLMTopK(); k != nil {
		a.llm.Apply(llm.WithTopK(*k))
	}
	if p := a.cfg.LLMTopP(); p != nil {
		a.llm.Apply(llm.WithTopP(*p))
	}
}

// ResumeSnapshot swaps the live agent into a previously-persisted session
// loaded from disk. Structurally mirrors SwitchProfile: enforce the
// running guard, rebuild profile/tools/LLM under the snapshot's
// persona+provider+model, then overwrite the session.
//
// The snapshot's session-id replaces the live agent's ID so subsequent
// persistSession writes target the same file (continuing the same
// resume-list entry rather than orphaning the original).
//
// Fallbacks (the snapshot may have been written under a persona or
// model that's since been removed):
//   - Missing persona → "evva".
//   - Unknown provider → current cfg.DefaultProvider.
//   - Unknown model → provider's first listed model.
//
// MUST be called while no Run is in flight. Subagents cannot resume —
// only the root. The string-keyed ResumeSession wrapper below is what
// ui.Controller exposes; this method is the testable seam.
func (a *Agent) ResumeSnapshot(snap *session.Snapshot) error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: only the root agent can resume a session")
	}
	if a.running.Load() {
		return ErrRunInProgress
	}
	if snap == nil {
		return fmt.Errorf("agent: nil snapshot")
	}

	personaName := snap.Profile
	if personaName == "" {
		personaName = "evva"
	}
	provider, ok := constant.GetProvider(snap.Provider)
	if !ok {
		a.logger.Warn("resume: unknown provider; falling back to default",
			"want", snap.Provider, "fallback", a.cfg.DefaultProvider.Name)
		provider = a.cfg.DefaultProvider
	}
	model := constant.Model(snap.Model)
	if !modelOfferedByProvider(provider, model) {
		fallback := provider.Models[0]
		a.logger.Warn("resume: model not offered by provider; falling back to first listed",
			"want", string(model), "provider", provider.Name, "fallback", string(fallback))
		model = fallback
	}

	mcpNames := a.mcpDiscoveredNames()
	newProfile, err := resolveMainProfileWithExtra(a.cfg, a.agentRegistry, personaName, a.skillRefs, a.memSnap, baseLLMOptions(a.profile.LLMOptions), a.cfg.DefaultProvider, a.cfg.DefaultModel, mcpNames, a.repoMap)
	if err != nil {
		a.logger.Warn("resume: persona unavailable; falling back to evva", "want", personaName, "err", err)
		newProfile, err = resolveMainProfileWithExtra(a.cfg, a.agentRegistry, "evva", a.skillRefs, a.memSnap, baseLLMOptions(a.profile.LLMOptions), a.cfg.DefaultProvider, a.cfg.DefaultModel, mcpNames, a.repoMap)
		if err != nil {
			return fmt.Errorf("agent: resume fallback to evva failed: %w", err)
		}
		personaName = "evva"
	}
	// Override the profile's provider/model with the snapshot's so the
	// rebuilt LLM client matches what wrote the session.
	newProfile.LLMProvider = provider
	newProfile.LLMModel = model

	// Re-merge custom tools so a resume (e.g. swarm restart-resume) keeps the
	// downstream-injected tools instead of collapsing to the profile's list.
	activeNames, err := a.activeToolNames(newProfile.ActiveTools)
	if err != nil {
		return err
	}
	exposeTools, err := toolset.Build(activeNames, a.toolState)
	if err != nil {
		return fmt.Errorf("agent: build active tools: %w", err)
	}
	active := make(map[string]tools.Tool, len(exposeTools))
	for _, t := range exposeTools {
		active[t.Name()] = t
	}
	deferred := make(map[tools.ToolName]struct{}, len(newProfile.DeferredTools))
	for _, n := range newProfile.DeferredTools {
		deferred[n] = struct{}{}
	}

	effortOpts := append(newProfile.LLMOptions, llm.WithEffort(llm.ParseEffort(a.effort)))
	effortOpts = append(effortOpts, runtimeLLMOptions(a.cfg)...)
	client, err := buildLLMClient(a.cfg, provider, model, effortOpts)
	if err != nil {
		return fmt.Errorf("agent: build llm client: %w", err)
	}

	a.profile = newProfile
	a.active = active
	a.deferredAllowlist = deferred
	a.exposeTools = exposeTools
	a.llm = client
	a.session = session.FromSnapshot(snap.Session)
	a.activePersona = personaName
	a.ID = snap.SessionID
	a.sessionCreatedAt = snap.CreatedAt
	a.toolState.TodoStore().Clear()
	// Re-scope checkpoints to the resumed session's namespace so /rewind lists
	// that session's history (not the pre-resume one).
	a.checkpoints.SetSession(a.ID)

	a.logger.Info("agent: session resumed",
		"id", snap.SessionID,
		"persona", personaName,
		"provider", provider.Name,
		"model", string(model),
		"messages", len(snap.Session.Messages),
	)
	return nil
}

// modelOfferedByProvider reports whether `m` appears in provider.Models.
func modelOfferedByProvider(provider constant.LLMProvider, m constant.Model) bool {
	if string(m) == "" {
		return false
	}
	for _, candidate := range provider.Models {
		if candidate == m {
			return true
		}
	}
	return false
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
	effortOpts := append(newProfile.LLMOptions, llm.WithEffort(llm.ParseEffort(a.effort)))
	client, err := buildLLMClient(a.cfg, provider, model, effortOpts)
	if err != nil {
		return fmt.Errorf("agent: build llm client: %w", err)
	}

	a.profile = newProfile
	a.llm = client
	a.session = session.New()
	a.applyRuntimeLLMParams()
	a.logger.Info("agent: llm switched", "provider", provider.Name, "model", string(model))
	return nil
}

// ClearSession starts a fresh conversation under the SAME persona, LLM
// client, and tool wiring: empty history, zeroed usage, cleared todos, and
// a NEW session id — so the next persistSession writes a new snapshot file
// instead of overwriting the old one, which stays on disk and remains
// loadable via /resume. The SessionStart hook latch is re-armed with
// source "clear" (the reserved source in hooks.SessionStartPayload), so
// hooks can tell a mid-session wipe from a process boot.
//
// MUST be called while no Run is in flight — returns ErrRunInProgress
// otherwise, mirroring SwitchLLM / ResumeSnapshot. Subagents cannot clear
// (their transcripts are ephemeral and parent-owned by design).
func (a *Agent) ClearSession() error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: only the root agent can clear its session")
	}
	if a.running.Load() {
		return ErrRunInProgress
	}

	oldID := a.ID
	a.session = session.New()
	a.ID = common.GenUUID()
	a.sessionCreatedAt = time.Time{}
	a.toolState.TodoStore().Clear()
	// /clear starts a fresh checkpoint namespace under the new session id.
	a.checkpoints.SetSession(a.ID)
	a.sessionStartSource = "clear"
	a.sessionStarted.Store(false)

	a.logger.Info("agent: session cleared", "old", oldID, "new", a.ID)
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

// ProfileName returns the active persona's wire name ("evva", "nono", ...).
// Used by the TUI status bar and the /profile picker's current-row marker.
// Subagents return the persona kind they were constructed under.
func (a *Agent) ProfileName() string { return a.activePersona }

// ListMainProfiles enumerates the personas the /profile picker can
// switch to. Pulls from the registry's ListMain(); subagents return nil
// (they don't drive the picker).
func (a *Agent) ListMainProfiles() []ui.ProfileChoice {
	if a.IsSubagent() || a.agentRegistry == nil {
		return nil
	}
	defs := a.agentRegistry.ListMain()
	out := make([]ui.ProfileChoice, 0, len(defs))
	for _, d := range defs {
		out = append(out, ui.ProfileChoice{Name: d.Name, WhenToUse: d.WhenToUse})
	}
	return out
}

// SubagentTypes returns the agent names that the AGENT tool's
// subagent_type enum should accept. Pulls from the registry's
// ListSubagent (so disk subagents become wire-callable as soon as the
// registry sees them). Falls back to the built-in pair when no
// registry is installed.
func (a *Agent) SubagentTypes() []string {
	if a.agentRegistry == nil {
		return []string{"explore", "plan", "general-purpose"}
	}
	defs := a.agentRegistry.ListSubagent()
	if len(defs) == 0 {
		return []string{"explore", "plan", "general-purpose"}
	}
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

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

// --- ui.Controller read-models -------------------------------------------
// These return public types so a UI in any module can render agent state
// without importing evva internals — the session / toolset containers stay
// private. They wrap the same data the concrete Session()/ToolState()
// accessors expose above.

// Compile-time proof that *Agent satisfies the public UI driving contract.
// pkg/agent.Agent.Controller() hands this view to UI.Attach.
var _ ui.Controller = (*Agent)(nil)

// Messages returns the live conversation transcript.
func (a *Agent) Messages() []llm.Message { return a.session.GetMessages() }

// Usage returns the cumulative session token usage.
func (a *Agent) Usage() llm.Usage { return a.session.Usage }

// LastTurnInputTokens returns the most recent turn's input-token count —
// the live prompt-size gauge the TUI context meter reads.
func (a *Agent) LastTurnInputTokens() int { return a.session.LastTurnInputTokens() }

// TodoStore exposes the todo backing store for the TUI's todo panel.
func (a *Agent) TodoStore() *todo.TodoStore { return a.toolState.TodoStore() }

// DaemonState exposes the unified daemon store (subagents, background bash,
// monitors). Returns nil until the first daemon registers — mirrors the
// HasDaemonState guard the strips relied on, so an empty session renders no
// strips and allocates no store.
func (a *Agent) DaemonState() *daemon.DaemonState {
	if !a.toolState.HasDaemonState() {
		return nil
	}
	return a.toolState.DaemonState()
}

// EnqueueUserPrompt hands the agent a prompt the user typed mid-run; the
// loop drains it at the next iteration boundary instead of starting a
// second concurrent Run.
func (a *Agent) EnqueueUserPrompt(prompt string) {
	a.toolState.UserPromptQueue().Enqueue(prompt)
}

// PermissionMode returns the agent's current permission stance. Safe to
// call from any goroutine.
func (a *Agent) PermissionMode() permission.Mode {
	v := a.permissionMode.Load()
	if v == nil {
		return permission.ModeDefault
	}
	return v.(permission.Mode)
}

// SetPermissionMode updates the agent's permission stance at runtime.
// Every entry path (Shift+Tab cycle, EnterPlanMode / ExitPlanMode tools,
// future SDK control messages) routes through here so the plan-mode
// transition hub runs exactly once per mode change and the TUI receives
// a single KindModeChanged event per change.
//
// Validates the mode; ignores unknown values to keep the system in a
// known-good state. Idempotent on no-op transitions: same-mode calls
// neither run side effects nor emit the change event.
//
// Mode changes don't propagate to already-spawned subagents — they
// captured the mode at spawn time. New spawns see the updated mode.
func (a *Agent) SetPermissionMode(m permission.Mode) {
	if !m.Valid() {
		return
	}
	prev := a.PermissionMode()
	if prev == m {
		return
	}
	a.permissionMode.Store(m)
	a.planModeState.Transition(prev, m)
	a.logger.Info("agent: permission mode set", "mode", string(m))
	if !a.IsSubagent() {
		a.emit(event.KindModeChanged, func(e *event.Event) {
			e.ModeChanged = &event.ModeChangedPayload{
				PrevMode: string(prev),
				Mode:     string(m),
			}
		})
	}
}

// SetPermissionModeName is the name-keyed, validating flavor of
// SetPermissionMode for callers that carry the mode as a wire string (the
// swarm web's per-member switch, future SDK control surfaces). Unlike
// SetPermissionMode — which silently ignores unknown values to keep the
// system in a known-good state — this returns an error, so an operator's
// typo surfaces as a 400 instead of a silent no-op.
func (a *Agent) SetPermissionModeName(name string) error {
	m := permission.Mode(name)
	if !m.Valid() {
		return fmt.Errorf("agent: invalid permission mode %q (want default|accept_edits|plan|bypass)", name)
	}
	a.SetPermissionMode(m)
	return nil
}

// PermissionStore exposes the shared rule store. Returns nil if the
// caller didn't install one (tests, headless CLI runs).
func (a *Agent) PermissionStore() *permission.Store { return a.permissionStore }

// PermissionBroker exposes the shared approval back-channel.
func (a *Agent) PermissionBroker() permission.Broker { return a.permissionBroker }

// Broker is an alias for PermissionBroker that satisfies
// mode.PlanModeController. Kept short so the controller interface stays
// terse — EnterPlanMode / ExitPlanMode only know the agent through this
// interface, not the full *Agent.
func (a *Agent) Broker() permission.Broker { return a.permissionBroker }

// Workdir returns the agent's current logical working directory. Used
// by the permission gate's plan-file carve-out, the EnterPlanMode tool's
// plan-file path, and the workdir-bound tool factories (Bash, fs). May
// change at runtime when EnterWorktree / ExitWorktree fires — read
// through this accessor each time you need it, don't cache.
func (a *Agent) Workdir() string {
	a.workdirMu.RLock()
	defer a.workdirMu.RUnlock()
	return a.workdir
}

// SwitchWorkdir mutates the agent's workdir and rebuilds every
// workdir-sensitive piece of session state in lockstep:
//
//  1. updates a.workdir and a.cfg.WorkDir so toolset accessors see the
//     new path immediately;
//  2. reloads the EVVA.md + USER_PROFILE.md snapshot from the new
//     workdir;
//  3. rebuilds the active-tools map so fs Read/Write/Edit/Glob and Bash
//     (which captured the OLD workdir at construction) point at the new
//     path;
//  4. re-renders the system prompt against the new memory snapshot and
//     applies it to the live LLM client.
//
// The session transcript is preserved — a worktree switch is a workdir
// move, not a persona change. Returns ErrRunInProgress if the agent is
// mid-Run (a tool changing workdir while the loop reads a.active would
// race); callers from inside tool Execute are already serialised with
// the loop, so this only blocks reentrant API misuse.
//
// Subagents reject SwitchWorkdir — the AgentTool isolation path sets
// the child's workdir at construction; mid-life changes are reserved
// for the root agent.
func (a *Agent) SwitchWorkdir(path string) error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: only the root agent can switch workdir")
	}
	if path == "" {
		return fmt.Errorf("agent: workdir path is required")
	}

	a.workdirMu.Lock()
	prev := a.workdir
	a.workdir = path
	a.workdirMu.Unlock()

	if a.cfg != nil {
		a.cfg.WorkDir = path
	}

	// Reload the workdir-bound memory snapshot. AppHome / user-profile
	// stay stable across the switch; only EVVA.md and project memory
	// change.
	enableAuto := false
	if a.cfg != nil {
		enableAuto = a.cfg.GetEnableAutoMemory()
	}
	appHome := ""
	if a.cfg != nil {
		appHome = a.cfg.AppHome
	}
	a.memSnap = memdir.Load(path, appHome, enableAuto)

	// Rebuild active tools so workdir-bound factories pick up the new
	// path. The toolState (and its registered observers) is reused — UI
	// panels stay subscribed across the switch. activeToolNames re-merges
	// custom tools so the switch doesn't drop them.
	names, err := a.activeToolNames(a.profile.ActiveTools)
	var exposeTools []tools.Tool
	if err == nil {
		exposeTools, err = toolset.Build(names, a.toolState)
	}
	if err != nil {
		// Roll back the workdir on failure so the agent stays consistent.
		a.workdirMu.Lock()
		a.workdir = prev
		a.workdirMu.Unlock()
		if a.cfg != nil {
			a.cfg.WorkDir = prev
		}
		return fmt.Errorf("agent: rebuild tools for new workdir: %w", err)
	}
	active := make(map[string]tools.Tool, len(exposeTools))
	for _, t := range exposeTools {
		active[t.Name()] = t
	}
	a.resolveMu.Lock()
	a.active = active
	a.exposeTools = exposeTools
	a.resolveMu.Unlock()

	// Re-render the system prompt against the new memory snapshot. Reuse
	// ResolveMainProfile so disk personas refresh the same way the
	// built-in does.
	if a.cfg != nil && a.activePersona != "" {
		newProfile, perr := resolveMainProfileWithExtra(a.cfg, a.agentRegistry, a.activePersona, a.skillRefs, a.memSnap, baseLLMOptions(a.profile.LLMOptions), a.cfg.DefaultProvider, a.cfg.DefaultModel, a.mcpDiscoveredNames(), a.repoMap)
		if perr == nil {
			a.profile.SystemPrompt = newProfile.SystemPrompt
			a.profile.LLMOptions = newProfile.LLMOptions
			a.llm.Apply(llm.WithSystem(newProfile.SystemPrompt))
		} else {
			a.logger.Warn("agent: rebuild sysprompt on workdir switch", "err", perr)
		}
	}

	a.logger.Info("agent: workdir switched", "prev", prev, "new", path)
	return nil
}

// ReloadSkills swaps the agent's skill catalog and re-renders the system prompt to
// match — the runtime analogue of WithSkillRegistry. The new registry drives BOTH
// the skill tool (SetSkillRegistry) and the prompt's # Skills section (skillRefs +
// re-resolve); an empty registry advertises "no skills" rather than inheriting the
// cfg-global catalog (same coercion as construction, RP-10-2). The prompt swap is a
// KV-cache miss on the next turn — acceptable for an explicit, infrequent reload.
//
// MUST be called while no Run is in flight; the swarm applies it at a run boundary
// (RP-10-4). Satisfies pkg/agent.SkillReloader.
func (a *Agent) ReloadSkills(reg *skill.Registry) error {
	if reg == nil {
		reg = skill.NewRegistry()
	}
	a.toolState.SetSkillRegistry(reg)
	refs := refsFromRegistry(reg)
	if refs == nil {
		refs = []sysprompt.SkillRef{}
	}
	a.skillRefs = refs

	if a.cfg == nil || a.activePersona == "" {
		return nil
	}
	newProfile, err := resolveMainProfileWithExtra(a.cfg, a.agentRegistry, a.activePersona, a.skillRefs, a.memSnap, baseLLMOptions(a.profile.LLMOptions), a.cfg.DefaultProvider, a.cfg.DefaultModel, a.mcpDiscoveredNames(), a.repoMap)
	if err != nil {
		return fmt.Errorf("agent: reload skills: re-resolve prompt: %w", err)
	}
	a.profile.SystemPrompt = newProfile.SystemPrompt
	a.profile.LLMOptions = newProfile.LLMOptions
	a.llm.Apply(llm.WithSystem(newProfile.SystemPrompt))
	return nil
}

// WorktreeSession returns the active worktree session, or nil if the
// agent isn't currently in one. Satisfies mode.WorktreeController.
func (a *Agent) WorktreeSession() *mode.WorktreeSession {
	return a.worktreeSession.Load()
}

// BeginWorktreeSession records that the agent has entered a worktree.
// Called by the EnterWorktree tool after a successful SwitchWorkdir.
func (a *Agent) BeginWorktreeSession(s mode.WorktreeSession) {
	a.worktreeSession.Store(&s)
}

// EndWorktreeSession clears the active worktree session. Called by the
// ExitWorktree tool after a successful SwitchWorkdir back to the
// original workdir (whether the worktree was kept or removed).
func (a *Agent) EndWorktreeSession() {
	a.worktreeSession.Store(nil)
}

// PrePlanMode returns the mode that was active immediately before plan
// mode became active. Empty until the first plan-mode entry; ExitPlanMode
// falls back to ModeDefault when empty.
//
// Reads through the unified plan-mode state holder so the TUI's Shift+Tab
// path and the EnterPlanMode tool path agree on what was stashed.
func (a *Agent) PrePlanMode() permission.Mode { return a.planModeState.PrePlanMode() }

// SetPrePlanMode is retained on the PlanModeController interface for the
// EnterPlanMode tool, but new code should rely on SetPermissionMode (which
// runs the transition hub and stashes the prior mode automatically).
func (a *Agent) SetPrePlanMode(m permission.Mode) { a.planModeState.SetPrePlanMode(m) }

// PlanName returns the user-provided plan name, set by enter_plan_mode.
// Empty means "current" — PlanFilePath resolves the default.
func (a *Agent) PlanName() string { return a.planModeState.PlanName() }

// SetPlanName stores the user-provided plan name. Called by enter_plan_mode
// when the model supplies a plan_name in its input.
func (a *Agent) SetPlanName(name string) { a.planModeState.SetPlanName(name) }

// PlanModeState exposes the unified plan-mode state holder so the
// attachment computer (internal/agent/attachments) can read the
// reminder-cycle counters without going through the agent's narrow
// permission-mode interface.
func (a *Agent) PlanModeState() *permission.PlanModeState { return a.planModeState }

// CyclePermissionMode advances the mode in Shift+Tab order and returns
// the new mode name. Implements ui.Controller.
//
// Unlike SetPermissionMode, this method does NOT emit a KindModeChanged
// event. It is called from the TUI's Update goroutine (via the Shift+Tab
// handler), and emitting would call tea.Program.Send() back into the same
// event loop — a guaranteed deadlock since bubbletea's p.msgs channel is
// unbuffered. The TUI already updates its status bar directly, so the
// event is redundant on this path.
func (a *Agent) CyclePermissionMode() string {
	next := a.PermissionMode().Next()
	if !next.Valid() || next == a.PermissionMode() {
		return string(a.PermissionMode())
	}
	prev := a.PermissionMode()
	a.permissionMode.Store(next)
	a.planModeState.Transition(prev, next)
	a.logger.Info("agent: permission mode cycled", "mode", string(next))
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

// questionAnswers folds the public response — the back-compat string Answers
// plus the additive MultiAnswers — into the canonical multi-value map. A key in
// MultiAnswers wins; a key only in Answers becomes a one-element slice.
func questionAnswers(resp ui.QuestionResponse) map[string][]string {
	out := make(map[string][]string, len(resp.Answers)+len(resp.MultiAnswers))
	for k, v := range resp.MultiAnswers {
		out[k] = v
	}
	for k, v := range resp.Answers {
		if _, ok := out[k]; !ok {
			out[k] = []string{v}
		}
	}
	return out
}

// RespondQuestion forwards the user's answers from the TUI to the question
// broker. id ties back to a single blocked question.Broker.Request call.
// Implements ui.Controller.
func (a *Agent) RespondQuestion(id string, resp ui.QuestionResponse) error {
	if a.questionBroker == nil {
		return errors.New("agent: no question broker installed")
	}
	r := question.Response{
		Answers:     questionAnswers(resp),
		Annotations: make(map[string]question.Annotation, len(resp.Annotations)),
	}
	for k, v := range resp.Annotations {
		r.Annotations[k] = question.Annotation{Notes: v.Notes, Preview: v.Preview}
	}
	return a.questionBroker.Respond(id, r)
}

// ListSessions enumerates persisted sessions for this agent's workdir,
// sorted by file mtime descending. Implements ui.Controller. Subagents
// never persist, so this returns an empty slice for them — the /resume
// command is only meaningful for the root.
func (a *Agent) ListSessions() ([]ui.SessionInfo, []string) {
	if a.IsSubagent() || a.cfg == nil || a.workdir == "" {
		return nil, nil
	}
	slug := memdir.ProjectKey(a.workdir)
	if slug == "" {
		return nil, nil
	}
	entries, warnings, err := session.List(a.cfg.AppHome, slug)
	if err != nil {
		a.logger.Warn("session.list", "err", err, "slug", slug)
		return nil, []string{err.Error()}
	}
	out := make([]ui.SessionInfo, 0, len(entries))
	for _, e := range entries {
		s := e.Snapshot
		out = append(out, ui.SessionInfo{
			ID:              s.SessionID,
			FirstUserPrompt: s.FirstUserPrompt,
			UpdatedAt:       e.MTime,
			CreatedAt:       s.CreatedAt.UnixNano(),
			Profile:         s.Profile,
			Provider:        s.Provider,
			Model:           s.Model,
			MessageCount:    len(s.Session.Messages),
		})
	}
	return out, warnings
}

// ResumeSession loads the snapshot with `id` off disk and swaps the
// live agent into it. Implements ui.Controller. The actual state-swap
// logic lives in ResumeSnapshot — this wrapper handles the disk read
// and the workdir-slug resolution.
func (a *Agent) ResumeSession(id string) error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: only the root agent can resume a session")
	}
	if a.cfg == nil || a.workdir == "" {
		return fmt.Errorf("agent: cannot resume without cfg + workdir")
	}
	slug := memdir.ProjectKey(a.workdir)
	if slug == "" {
		return fmt.Errorf("agent: cannot derive workdir slug")
	}
	snap, err := session.Load(a.cfg.AppHome, slug, id)
	if err != nil {
		return fmt.Errorf("agent: load session %q: %w", id, err)
	}
	return a.ResumeSnapshot(snap)
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

// drainLSPDiagnostics drains pending LSP diagnostics from the manager and
// injects them as a <system-reminder> block. Called at each iteration
// start alongside drainDaemonSignals.
func (a *Agent) drainLSPDiagnostics() {
	mgr := a.toolState.LSPManager()
	if mgr == nil {
		return
	}
	diags := mgr.DrainDiagnostics()
	if len(diags) == 0 {
		return
	}
	reminder := lsp.FormatDiagnosticsReminder(diags)
	a.session.Append(signalReminderMessage([]string{reminder}))
	a.logger.Debug("lsp.diagnostics.drained", "count", len(diags))
}
