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
//   - toolset.ToolState holds per-agent shared state (e.g. *task.Store) so
//     stateful tool families can be constructed with the right backing data.
//     The agent constructs one ToolState per agent instance, so two agents
//     built from the same profile get isolated state.
//
//   - The agent (internal/agent) decides WHICH tools to build eagerly
//     (ActiveTools — exposed every turn) vs which to mark as lazy-loadable
//     (DeferredTools — materialized on demand when first invoked).
package toolset

import (
	"fmt"

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
	taskStore       *task.Store
	subagentSpawner meta.SubagentSpawner
	deferredLookup  meta.DeferredLookup
	readTracker     *fs.ReadTracker
	// Future: monitorBus, cronService, skillLoader, ...
}

// TaskStore returns the task subsystem's backing store, allocating one on
// first use. All six task tools constructed against the same ToolState share it.
func (s *ToolState) TaskStore() *task.Store {
	if s.taskStore == nil {
		s.taskStore = task.NewStore()
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
		s = &ToolState{}
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
		return fs.NewWrite(s.ReadTracker()), nil
	case tools.EDIT_FILE:
		return fs.NewEdit(s.ReadTracker()), nil

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
		return meta.NewAgent(s.SubagentSpawner), nil
	case tools.TOOL_SEARCH:
		// Same late-binding pattern as AGENT — the agent installs itself
		// as the deferred lookup via SetDeferredLookup after construction.
		return meta.NewToolSearch(s.DeferredLookup), nil
	case tools.SKILL:
		return meta.Skill, nil
	case tools.SCHEDULE_WAKEUP:
		return meta.Wakeup, nil

	// --- task (stateful — all six share one *Store via ToolState) ---
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
