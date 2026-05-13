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
//   - toolset.Builders holds per-agent shared state (e.g. *task.Store) so
//     stateful tool families can be constructed with the right backing data.
//     The agent constructs one Builders per agent instance, so two agents
//     built from the same profile get isolated state.
//
//   - The agent (internal/agent) decides WHICH tools to build eagerly
//     (ActiveTools — exposed every turn) vs which to mark as lazy-loadable
//     (DeferredTools — materialized on demand via TOOL_SEARCH).
package toolset

import (
	"encoding/json"
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

// Descriptor is the LLM-facing metadata for a tool — name, description, JSON
// input schema. Describing a tool does NOT make it callable; it's the cheap
// "show the model what this tool would look like if loaded" path that
// TOOL_SEARCH uses to surface deferred-tool schemas on demand.
type Descriptor struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Describe returns the metadata for a tool name without registering the tool
// with any agent. Internally this constructs a throwaway instance just long
// enough to read its static metadata fields — for stateful tools the
// short-lived backing state (e.g. an empty *task.Store) is immediately
// garbage-collected.
//
// Use Build when you actually need to call Execute.
func Describe(name tools.ToolName) (Descriptor, error) {
	t, err := buildOne(name, &Builders{})
	if err != nil {
		return Descriptor{}, err
	}
	return Descriptor{
		Name:        t.Name(),
		Description: t.Description(),
		Schema:      t.Schema(),
	}, nil
}

// Builders carries the shared backing state for stateful tool families.
// Accessors are lazy — state is allocated only when the first tool that needs
// it is built. Pass the same *Builders to every Build call for an agent so
// siblings within a family share state.
//
// Cross-cutting consumers (TUI, session persistence) hold this through the
// agent (via agent.Builders()) and read state through the typed accessors
// rather than peeking into tool internals.
type Builders struct {
	taskStore *task.Store
	// Future: monitorBus, cronService, skillLoader, subAgentRunner, ...
}

// TaskStore returns the task subsystem's backing store, allocating one on
// first use. All six task tools constructed via the same Builders share it.
func (b *Builders) TaskStore() *task.Store {
	if b.taskStore == nil {
		b.taskStore = task.NewStore()
	}
	return b.taskStore
}

// Build resolves each name to a tool instance. Stateful tools pull their
// backing state from b; stateless tools are package-level singletons.
//
// Unknown names return an error — there is no silent fallback.
func Build(names []tools.ToolName, b *Builders) ([]tools.Tool, error) {
	if b == nil {
		b = &Builders{}
	}
	out := make([]tools.Tool, 0, len(names))
	for _, n := range names {
		t, err := buildOne(n, b)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func buildOne(name tools.ToolName, b *Builders) (tools.Tool, error) {
	switch name {
	// --- fs (stateless singletons) ---
	case tools.READ_FILE:
		return fs.Read, nil
	case tools.WRITE_FILE:
		return fs.Write, nil
	case tools.EDIT_FILE:
		return fs.Edit, nil

	// --- shell (stateless) ---
	case tools.BASH:
		return shell.Bash, nil

	// --- meta (stateless stubs; will take agent hooks once implemented) ---
	case tools.AGENT:
		return meta.Agent, nil
	case tools.TOOL_SEARCH:
		return meta.ToolSearch, nil
	case tools.SKILL:
		return meta.Skill, nil
	case tools.SCHEDULE_WAKEUP:
		return meta.Wakeup, nil

	// --- task (stateful — all six share one *Store via Builders) ---
	case tools.TASK_CREATE:
		return task.NewCreate(b.TaskStore()), nil
	case tools.TASK_GET:
		return task.NewGet(b.TaskStore()), nil
	case tools.TASK_LIST:
		return task.NewList(b.TaskStore()), nil
	case tools.TASK_UPDATE:
		return task.NewUpdate(b.TaskStore()), nil
	case tools.TASK_OUTPUT:
		return task.NewOutput(b.TaskStore()), nil
	case tools.TASK_STOP:
		return task.NewStop(b.TaskStore()), nil

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

	// --- shell extras (stubs — implementations TBD) ---
	case tools.GREP, tools.TREE:
		return nil, fmt.Errorf("toolset: %q has no constructor yet", name)

	default:
		return nil, fmt.Errorf("toolset: unknown tool %q", name)
	}
}
