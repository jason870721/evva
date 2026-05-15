package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/johnny1110/evva/internal/constant"

	"github.com/johnny1110/evva/internal/observable"
	"github.com/johnny1110/evva/internal/tools"
)

// SpawnerLookup is the function shape a ToolState method (or any closure)
// satisfies to provide late-bound access to a SubagentSpawner. AgentTool
// keeps the lookup, not the spawner, so the order in which the agent and
// the tool are constructed doesn't matter — the spawner can be installed
// after the tool already exists.
type SpawnerLookup func() SubagentSpawner

// SpawnGroupDomain is the observable.Change.Domain value carried by every
// SpawnGroup change. Subscribers switch on this string and type-assert
// Change.Payload to SubagentSnapshot.
const SpawnGroupDomain = "subagent"

// SubagentSnapshot is the typed payload carried in observable.Change.Payload
// for every "subagent" domain change. Each notification ships a full snapshot
// so consumers can render the row without keeping their own state.
//
// Async marks subagents whose results must be picked up via
// SpawnGroup.DrainCompleted (the main agent loop does this between turns).
// Sync subagents deliver their result through the tool return channel and
// are Remove'd as soon as the spawner finishes, so they never sit in
// DrainCompleted's queue.
type SubagentSnapshot struct {
	Name    string
	ID      string
	Type    string // "explore", "general-purpose", ...
	Status  string
	Async   bool
	JobDesc string // prompt summary
	Summary string // result summary (set on Report)
	Err     string // error message (set on Crush)
}

type spawnedAgent struct {
	snap SubagentSnapshot
	done bool // true once phase ∈ {done, crushed}
}

// SpawnGroup is the per-agent panel of in-flight subagents. It is an
// observable.Store: every mutation fans through the framework so the TUI
// (and any other subscriber) can re-render without per-store wiring.
//
// Lifecycle: Add → optional Status updates → Report | Crush → Remove (sync) /
// DrainCompleted (async). Sync subagents are short-lived in the panel —
// the spawner calls Remove right after the child returns. Async subagents
// stay in the panel until the parent loop drains them between turns.
type SpawnGroup struct {
	observable.Observable

	mu     sync.Mutex
	agents map[string]*spawnedAgent
	order  []string // insertion order for stable Drain
}

func NewSpawnGroup() *SpawnGroup {
	return &SpawnGroup{agents: map[string]*spawnedAgent{}}
}

// Domain identifies this store on the change stream.
func (g *SpawnGroup) Domain() string { return SpawnGroupDomain }

// Add records a new subagent in the init phase. async marks subagents
// whose result will be delivered through DrainCompleted instead of the
// usual tool-return path.
func (g *SpawnGroup) Add(name, id, agentType, jobDesc string, async bool) {
	snap := SubagentSnapshot{
		Name:    name,
		ID:      id,
		Type:    agentType,
		Status:  constant.INIT.String(),
		Async:   async,
		JobDesc: jobDesc,
	}
	g.mu.Lock()
	g.agents[id] = &spawnedAgent{snap: snap}
	g.order = append(g.order, id)
	g.mu.Unlock()

	g.Notify(observable.Change{Domain: SpawnGroupDomain, Op: "added", ID: id, Payload: snap})
}

// Status updates the lifecycle phase of an in-flight subagent and notifies
// observers. No-op when the id is unknown.
func (g *SpawnGroup) Status(id string, status constant.AgentStatus) {
	g.mu.Lock()
	a, ok := g.agents[id]
	if !ok {
		g.mu.Unlock()
		return
	}
	a.snap.Status = status.String()
	snap := a.snap
	g.mu.Unlock()

	g.Notify(observable.Change{Domain: SpawnGroupDomain, Op: "status", ID: id, Payload: snap})
}

// Report marks a subagent as completed and records its result summary.
// Async subagents in this state are picked up by DrainCompleted; sync
// subagents are immediately Remove'd by the spawner.
func (g *SpawnGroup) Report(id, summary string) {
	g.mu.Lock()
	a, ok := g.agents[id]
	if !ok {
		g.mu.Unlock()
		return
	}
	a.snap.Status = constant.READY_REPORT.String()
	a.snap.Summary = summary
	a.done = true
	snap := a.snap
	g.mu.Unlock()

	g.Notify(observable.Change{Domain: SpawnGroupDomain, Op: "report", ID: id, Payload: snap})
}

// Crush marks a subagent as failed.
func (g *SpawnGroup) Crush(id string, err error) {
	msg := "subagent crushed"
	if err != nil {
		msg = err.Error()
	}
	g.mu.Lock()
	a, ok := g.agents[id]
	if !ok {
		g.mu.Unlock()
		return
	}
	a.snap.Status = constant.CRUSHED.String()
	a.snap.Err = msg
	a.done = true
	snap := a.snap
	g.mu.Unlock()

	g.Notify(observable.Change{Domain: SpawnGroupDomain, Op: "crushed", ID: id, Payload: snap})
}

// Remove deletes an entry from the group and notifies observers. Used by
// the spawner for sync subagents (their result is delivered through the
// tool return channel, not through DrainCompleted).
func (g *SpawnGroup) Remove(id string) {
	g.mu.Lock()
	a, ok := g.agents[id]
	if !ok {
		g.mu.Unlock()
		return
	}
	snap := a.snap
	delete(g.agents, id)
	for i, oid := range g.order {
		if oid == id {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	g.mu.Unlock()

	g.Notify(observable.Change{Domain: SpawnGroupDomain, Op: "removed", ID: id, Payload: snap})
}

// Snapshot returns a stable copy of every tracked subagent in insertion
// order. Read-only; the panel's drain queue is untouched. UIs poll this
// to render without racing against in-flight goroutines.
func (g *SpawnGroup) Snapshot() []SubagentSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]SubagentSnapshot, 0, len(g.order))
	for _, id := range g.order {
		if a, ok := g.agents[id]; ok {
			out = append(out, a.snap)
		}
	}
	return out
}

// DrainCompleted atomically extracts and removes every async subagent that
// has reached a terminal phase (done or crushed). Returned snapshots are
// in insertion order. Each removal emits an Op:"removed" change so the
// TUI clears its row.
//
// Sync subagents are never returned here — the spawner removes them
// directly via Remove as soon as their tool-return path completes.
func (g *SpawnGroup) DrainCompleted() []SubagentSnapshot {
	g.mu.Lock()
	out := make([]SubagentSnapshot, 0)
	keep := make([]string, 0, len(g.order))
	for _, id := range g.order {
		a, ok := g.agents[id]
		if !ok {
			continue
		}
		// collect async agent which is done(crushed or ready_report)
		if a.done && a.snap.Async {
			out = append(out, a.snap)
			delete(g.agents, id)
			continue
		}
		keep = append(keep, id)
	}
	g.order = keep
	g.mu.Unlock()

	for _, snap := range out {
		g.Notify(observable.Change{Domain: SpawnGroupDomain, Op: "removed", ID: snap.ID, Payload: snap})
	}
	return out
}

// AgentTool is the LLM-facing handle for spawning subagents. The actual
// work is delegated to a SubagentSpawner installed by the agent layer.
type AgentTool struct {
	lookup SpawnerLookup
	group  *SpawnGroup
}

// NewAgent constructs an AgentTool that reads its spawner via lookup at
// Execute time. lookup may be nil (yields a clear runtime error if the
// model invokes the tool); it may also return nil (same outcome).
func NewAgent(lookup SpawnerLookup, spawnGroup *SpawnGroup) *AgentTool {
	return &AgentTool{lookup: lookup, group: spawnGroup}
}

func (t *AgentTool) Name() string { return string(tools.AGENT) }

func (t *AgentTool) Description() string {
	return "Spawn a sub-agent to handle a focused task in isolation. " +
		"Use for parallel/independent work, exploration that would dump a lot of context, " +
		"or any task where you want a clean conversation thread. " +
		"The sub-agent inherits the parent's LLM provider; pick model tier via `level`: " +
		"level=1 (default) for routine, level=2 for hard reasoning. " +
		"Level=2 is more expensive — only use it when the task is complex. " +
		"Sub-agents cannot themselves call this tool — the hierarchy is exactly one layer deep."
}

func (t *AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["name", "description","prompt"],
		"properties":{
			"name":{"type":"string","description":"A short nickname"},
			"description":{"type":"string","description":"A short (3-5 word) description of the task"},
			"prompt":{"type":"string","description":"The full task prompt for the sub-agent"},
			"subagent_type":{"type":"string","enum":["explore","general-purpose"],"description":"Which preset profile to use. Defaults to general-purpose. \"explore\" is read-only and good for codebase inspection."},
			"level":{"type":"integer","enum":[1,2],"default":1,"description":"Model tier within the parent's provider. 1=general, 2=thinking Defaults to 1. Use 2 only when the task genuinely needs deeper reasoning."},
			"async_mode":{"type":"boolean","default":false,"description":"Let the subagent run in the background; the spawner returns an ack immediately and the eventual summary is injected into the parent's next turn."}
		}
	}`)
}

type agentInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
	Level        int    `json:"level"`
	AsyncMode    bool   `json:"async_mode"`
}

func (t *AgentTool) Execute(ctx context.Context, input json.RawMessage) (tools.Result, error) {
	var in agentInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("agent: decode: %v", err)}, nil
	}
	if in.Prompt == "" {
		return tools.Result{IsError: true, Content: "agent: prompt is required"}, nil
	}
	if t.lookup == nil {
		return tools.Result{IsError: true, Content: "agent: no spawner lookup configured"}, nil
	}
	spawner := t.lookup() // the spawner should be main(root) agent only.
	if spawner == nil {
		// Likely cause: the AGENT tool was reached from a subagent (the agent layer only installs the spawner on root agents).
		return tools.Result{IsError: true, Content: "agent: subagent spawning is only available from the root agent"}, nil
	}

	kind := in.SubagentType
	if kind == "" {
		kind = "general-purpose"
	}

	out, err := spawner.Spawn(ctx, SpawnRequest{
		Name:      in.Name,
		Kind:      kind,
		Desc:      in.Description,
		Prompt:    in.Prompt,
		Level:     in.Level,
		AsyncMode: false,
	})

	if err != nil {
		if errors.Is(err, ErrSubagentForbidden) {
			// Recoverable — model can ditch the subagent plan and try something else.
			return tools.Result{IsError: true, Content: err.Error()}, nil
		}
		// Other errors abort the parent loop — they are Go-level failures
		// (LLM transport, tool panics) the model can't recover from.
		return tools.Result{IsError: true, Content: fmt.Sprintf("agent: %v", err)}, err
	}
	// If this is a async mode agent, output will be like "subagent running in background."
	return tools.Result{Content: out}, nil
}
