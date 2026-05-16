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
//   - toolset.ToolState holds per-agent shared state (e.g. *task.TaskGroup) so
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
	"fmt"
	"sync"

	"github.com/johnny1110/evva/internal/observable"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/cron"
	"github.com/johnny1110/evva/internal/tools/fs"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/internal/tools/monitor"
	"github.com/johnny1110/evva/internal/tools/notebook"
	"github.com/johnny1110/evva/internal/tools/shell"
	"github.com/johnny1110/evva/internal/tools/task"
	"github.com/johnny1110/evva/internal/tools/ux"
	"github.com/johnny1110/evva/internal/tools/web"
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
	taskStore       *task.TaskGroup
	subagentSpawner meta.SubagentSpawner
	deferredLookup  meta.DeferredLookup
	readTracker     *fs.ReadTracker
	subAgentGroup   *meta.SpawnGroup
	// approver gates fs mutations behind user confirmation. Late-bound:
	// the host (cmd/evva) installs the right approver for the current
	// UI mode (TUI overlay or stdin prompt) via SetApprover BEFORE the
	// agent builds its tool list. nil = no gate (auto-approve).
	approver    fs.Approver
	wakeupQueue *meta.WakeupQueue
	// userPromptQueue carries prompts the user typed while a Run was
	// already in flight. The agent loop drains it between iterations
	// so the conversation stays well-formed (no orphaned tool_calls).
	// Only the root agent's queue is ever populated — subagents
	// have no user input.
	userPromptQueue *UserPromptQueue
	// Future: monitorBus, cronService, skillLoader, ...

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

// HasAgentGroupPanel reports whether a SpawnGroup has already been
// allocated. The agent loop uses this to skip drain calls in runs that
// never spawned a subagent (avoids forcing the lazy allocation just to
// peek at an empty panel).
func (s *ToolState) HasAgentGroupPanel() bool { return s.subAgentGroup != nil }

// TaskStore returns the task subsystem's backing store, allocating one on
// first use. All six task tools constructed against the same ToolState share it.
// First-use also registers the store on the change stream so the agent's
// event bridge picks up every task mutation without per-store wiring.
func (s *ToolState) TaskStore() *task.TaskGroup {
	if s.taskStore == nil {
		s.taskStore = task.NewTaskGroup()
		s.RegisterStore(s.taskStore)
	}
	return s.taskStore
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

// ReadTracker returns the session read-tracker shared by all fs tools,
// allocating one on first use.
func (s *ToolState) ReadTracker() *fs.ReadTracker {
	if s.readTracker == nil {
		s.readTracker = fs.NewReadTracker()
	}
	return s.readTracker
}

// Approver returns the installed fs.Approver, or nil if no gate has
// been wired. The write/edit tools pass this through their constructors
// — a nil result means auto-approve, which is correct for tests and
// bench harnesses that bypass UI confirmation.
func (s *ToolState) Approver() fs.Approver {
	return s.approver
}

// SetApprover installs the fs approval gate. The host (cmd/evva) calls
// this once during startup with the right approver for the current
// mode: a TUI overlay in interactive mode, a stdin prompt for
// headless CLI runs. Must be called BEFORE the agent constructs its
// tools — buildOne snapshots the current approver into each fs tool.
func (s *ToolState) SetApprover(a fs.Approver) {
	s.approver = a
}

func (s *ToolState) AgentGroup() *meta.SpawnGroup {
	if s.subAgentGroup == nil {
		s.subAgentGroup = meta.NewSpawnGroup()
		s.RegisterStore(s.subAgentGroup)
	}
	return s.subAgentGroup
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
	t, err := buildOne(name, &ToolState{})
	if err != nil {
		return tools.Descriptor{}, err
	}
	return tools.Descriptor{
		Name:        t.Name(),
		Description: t.Description(),
		Schema:      t.Schema(),
		Tags:        TagsFor(name),
	}, nil
}

// Build resolves each name to a tool instance. Stateful tools pull their
// backing state from s; stateless tools are package-level singletons.
//
// Unknown names return an error — there is no silent fallback.
func Build(names []tools.ToolName, s *ToolState) ([]tools.Tool, error) {
	if s == nil {
		s = NewToolState()
	}
	out := make([]tools.Tool, 0, len(names))
	for _, n := range names {
		t, err := buildOne(n, s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func buildOne(name tools.ToolName, s *ToolState) (tools.Tool, error) {
	switch name {
	// --- fs (stateful — share ReadTracker via ToolState) ---
	case tools.READ_FILE:
		return fs.NewRead(s.ReadTracker()), nil
	case tools.WRITE_FILE:
		return fs.NewWrite(s.ReadTracker(), lateApprover{state: s}), nil
	case tools.EDIT_FILE:
		return fs.NewEdit(s.ReadTracker(), lateApprover{state: s}), nil

	// --- shell (stateless) ---
	case tools.BASH:
		return shell.Bash, nil
	case tools.GREP:
		return shell.Grep, nil
	case tools.TREE:
		return shell.Tree, nil

	// --- meta ---
	case tools.AGENT:
		// Lookup is late-bound: the agent installs itself via
		// SetSubagentSpawner after construction, and meta.AgentTool reads
		// through s.SubagentSpawner at Execute time.
		return meta.NewAgent(s.SubagentSpawner, s.AgentGroup()), nil
	case tools.TOOL_SEARCH:
		// Same late-binding pattern as AGENT — the agent installs itself
		// as the deferred lookup via SetDeferredLookup after construction.
		return meta.NewToolSearch(s.DeferredLookup), nil
	case tools.SKILL:
		return meta.Skill, nil
	case tools.SCHEDULE_WAKEUP:
		return meta.NewWakeup(s.WakeupQueue()), nil

	// --- task (stateful — all six share one *TaskGroup via ToolState) ---
	case tools.TASK_CREATE:
		return task.NewCreate(s.TaskStore()), nil
	case tools.TASK_GET:
		return task.NewGet(s.TaskStore()), nil
	case tools.TASK_LIST:
		return task.NewList(s.TaskStore()), nil
	case tools.TASK_UPDATE:
		return task.NewUpdate(s.TaskStore()), nil
	case tools.TASK_OUTPUT:
		return task.NewOutput(s.TaskStore()), nil
	case tools.TASK_STOP:
		return task.NewStop(s.TaskStore()), nil

	// --- monitor / mode / notebook / cron / web / ux (stateless stubs) ---
	case tools.MONITOR:
		return monitor.Monitor, nil

	case tools.ENTER_PLAN_MODE:
		return mode.EnterPlan, nil
	case tools.EXIT_PLAN_MODE:
		return mode.ExitPlan, nil
	case tools.ENTER_WORKTREE:
		return mode.EnterWorktree, nil
	case tools.EXIT_WORKTREE:
		return mode.ExitWorktree, nil

	case tools.NOTEBOOK_EDIT:
		return notebook.Edit, nil

	case tools.CRON_CREATE:
		return cron.Create, nil
	case tools.CRON_LIST:
		return cron.List, nil
	case tools.CRON_DELETE:
		return cron.Delete, nil
	case tools.REMOTE_TRIGGER:
		return cron.Trigger, nil

	case tools.WEB_FETCH:
		return web.Fetch, nil
	case tools.WEB_SEARCH:
		return web.Search, nil

	case tools.ASK_USER_QUESTION:
		return ux.AskQuestion, nil
	case tools.PUSH_NOTIFICATION:
		return ux.Notify, nil

	default:
		return nil, fmt.Errorf("toolset: unknown tool %q", name)
	}
}

// lateApprover bridges fs.Approver to the ToolState's late-bound
// approver slot. Tools snapshot this at build time; calls to Approve
// resolve the currently-installed approver on each invocation, so the
// host can SetApprover any time before the first fs mutation lands.
// A nil approver on the state means "no gate" — return approved=true.
type lateApprover struct {
	state *ToolState
}

func (l lateApprover) Approve(ctx context.Context, diff *fs.FileDiff) (fs.Decision, error) {
	a := l.state.Approver()
	if a == nil {
		return fs.Decision{Approved: true}, nil
	}
	return a.Approve(ctx, diff)
}
