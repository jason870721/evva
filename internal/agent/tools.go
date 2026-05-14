package agent

import (
	"fmt"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/task"
	"github.com/johnny1110/evva/internal/toolset"
)

// ResolveTool returns the runnable instance for a tool name, building it on
// the fly if it's a still-unmaterialized deferred tool. This is the path the
// tool-call dispatcher takes whenever the LLM invokes a tool by name:
//
//   - If the name is already in the active map (either built at New() or
//     resolved on a previous turn), the cached instance is returned.
//   - Otherwise, if the name is in the deferred allowlist, the tool is built
//     via toolset.Build, cached in active, and returned. Its schema will be
//     advertised to the LLM from the next turn forward.
//   - Otherwise, the name is rejected — the agent never silently expands
//     beyond the profile's declared authority.
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
	built, err := toolset.Build([]tools.ToolName{name}, a.toolState)
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
//
// Part of the meta.DeferredLookup interface; the agent installs itself
// as the lookup target via toolState.SetDeferredLookup in New().
func (a *Agent) DeferredNames() []tools.ToolName {
	out := make([]tools.ToolName, 0, len(a.deferredAllowlist))
	for n := range a.deferredAllowlist {
		out = append(out, n)
	}
	return out
}

// Describe returns the metadata for a deferred tool by name. Delegates to
// toolset.Describe, which constructs a throwaway instance to read its
// static fields — no agent state is mutated and no tool is "loaded".
//
// Part of the meta.DeferredLookup interface, used by TOOL_SEARCH.
func (a *Agent) Describe(name tools.ToolName) (tools.Descriptor, error) {
	if _, ok := a.deferredAllowlist[name]; !ok {
		return tools.Descriptor{}, fmt.Errorf("agent: %q is not in the deferred allowlist", name)
	}
	return toolset.Describe(name)
}

// Agent ToolState OnChange Event Binding ============================================

func toolStateOnchangeEventBinding(a *Agent) {
	// Wire task store mutations to the event stream. Done after options so
	// the closure captures the final sink. TaskStore() lazy-allocates on
	// first call; this also forces that allocation when tasks are in scope.
	if a.hasAnyTaskTool() {
		bindTaskOnChange(a)
	}

	// TODO Other tool state onchange event add here...
}

func bindTaskOnChange(a *Agent) {
	// mount event with toolState.TaskStore()
	a.toolState.TaskStore().OnChange = func(id, status, subject string) {
		a.emit(event.KindTaskUpdate, func(e *event.Event) {
			e.TaskUpdate = &event.TaskUpdatePayload{
				TaskID:  id,
				Status:  status,
				Subject: subject,
			}
		})
	}
}

// hasAnyTaskTool reports whether the profile mentions any task tool —
// either active or deferred. Used to decide whether wiring the task store's
// OnChange hook is worth it. Agents with no task tools never need the
// emit-bridge and skip the lazy TaskStore allocation entirely.
func (a *Agent) hasAnyTaskTool() bool {
	for _, n := range a.profile.ActiveTools {
		if task.IsTaskToolName(n) {
			return true
		}
	}
	for n := range a.deferredAllowlist {
		if task.IsTaskToolName(n) {
			return true
		}
	}
	return false
}
