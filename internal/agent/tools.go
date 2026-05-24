package agent

import (
	"fmt"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/observable"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/ui"
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
	a.resolveMu.Lock()
	defer a.resolveMu.Unlock()
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

// MarkDiscovered builds the given deferred tools and adds them to both
// a.active and a.exposeTools so they are callable on the next LLM turn.
// Called after TOOL_SEARCH returns matches — this is evva's equivalent of
// ref's tool_reference expansion.
//
// Already-active names are silently skipped (idempotent). Names not in the
// deferred allowlist are skipped — the agent never expands beyond the
// profile's declared authority.
func (a *Agent) MarkDiscovered(names []tools.ToolName) error {
	a.resolveMu.Lock()
	defer a.resolveMu.Unlock()

	var toBuild []tools.ToolName
	for _, n := range names {
		if _, ok := a.active[string(n)]; ok {
			continue
		}
		if _, ok := a.deferredAllowlist[n]; !ok {
			continue
		}
		toBuild = append(toBuild, n)
	}
	if len(toBuild) == 0 {
		return nil
	}

	built, err := toolset.Build(toBuild, a.toolState)
	if err != nil {
		return fmt.Errorf("agent: MarkDiscovered build: %w", err)
	}
	for _, t := range built {
		a.active[t.Name()] = t
	}
	a.exposeTools = append(a.exposeTools, built...)
	return nil
}

// Skills returns the user-installed skill catalog as the UI sees it —
// name + description per entry, sorted by name. Implements ui.Controller
// so the TUI's slash-suggestion panel can list skills alongside the
// built-in commands without reaching into ToolState directly.
//
// Returns nil when no registry was installed (e.g. tests / headless
// callers that didn't pass WithSkillRegistry).
func (a *Agent) Skills() []ui.Skill {
	reg := a.toolState.SkillRegistry()
	if reg == nil {
		return nil
	}
	list := reg.List()
	out := make([]ui.Skill, 0, len(list))
	for _, m := range list {
		out = append(out, ui.Skill{Name: m.Name, Description: m.Description})
	}
	return out
}

// Agent ToolState OnChange Event Binding ============================================

// bindToolStateEvents wires the ToolState's unified change stream into the
// agent's event sink. Every observable.Store registered on the ToolState
// (today: todo.TodoStore, daemon.DaemonState; tomorrow: whatever a
// developer adds) flows through here as a single KindStoreUpdate event.
//
// Adding a new panel requires no changes to this function — the new store
// auto-registers via its lazy accessor on ToolState and the subscription
// below picks up its changes automatically.
func bindToolStateEvents(a *Agent) {
	a.toolState.Subscribe(func(c observable.Change) { // Agent.emit is the final subscribe!! each store can push  emit by Notify()
		a.emit(event.KindStoreUpdate, func(e *event.Event) {
			e.StoreUpdate = &event.StoreUpdatePayload{
				Domain:  c.Domain,
				Op:      c.Op,
				ID:      c.ID,
				Payload: c.Payload,
				Time:    c.Time,
			}
		})
	})
}
