package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/constant"

	"github.com/johnny1110/evva/internal/tools"
)

// SpawnerLookup is the function shape a ToolState method (or any closure)
// satisfies to provide late-bound access to a SubagentSpawner. AgentTool
// keeps the lookup, not the spawner, so the order in which the agent and
// the tool are constructed doesn't matter — the spawner can be installed
// after the tool already exists.
type SpawnerLookup func() SubagentSpawner

type Agent struct {
	Name     string               // random UUID 6 digits
	ID       string               // agent ID
	JobTitle string               // form prompt
	Status   constant.AgentStatus // init, thinking, tool_using...
	PSummary string               // prompt summary
	RSummary string               // Exec result summary
}

type SpawnGroup struct {
	SpawnAgents []*Agent
	OnChange    func(id string, agtype string, psummary string, rsummary string, phase int)
}

func NewSpawnGroup() *SpawnGroup {
	return &SpawnGroup{
		SpawnAgents: []*Agent{},
	}
}

func (g *SpawnGroup) Add(name, id, agtype, summary string) {
	g.SpawnAgents = append(g.SpawnAgents, &Agent{
		Name:     name,
		ID:       id,
		Status:   constant.INIT,
		PSummary: summary,
	})
	g.OnChange(id, agtype, summary, "", int(event.SubagentInit))
}

func (g *SpawnGroup) Crush(id string, err error) {
	// TODO
}

func (g *SpawnGroup) Done(id string, rSummary string) {
	// TODO
}

// AgentTool is the LLM-facing handle for spawning subagents. The actual
// work is delegated to a SubagentSpawner installed by the agent layer.
type AgentTool struct {
	lookup     SpawnerLookup
	groupPanel *SpawnGroup
}

// NewAgent constructs an AgentTool that reads its spawner via lookup at
// Execute time. lookup may be nil (yields a clear runtime error if the
// model invokes the tool); it may also return nil (same outcome).
func NewAgent(lookup SpawnerLookup, groupPanel *SpawnGroup) *AgentTool {
	return &AgentTool{lookup: lookup, groupPanel: groupPanel}
}

func (t *AgentTool) Name() string { return string(tools.AGENT) }

func (t *AgentTool) Description() string {
	return "Spawn a sub-agent to handle a focused task in isolation. " +
		"Use for parallel/independent work, exploration that would dump a lot of context, " +
		"or any task where you want a clean conversation thread. " +
		"The sub-agent inherits the parent's LLM provider; pick model tier via `level`: " +
		"level=1 (default) for routine work (Sonnet-class), level=2 for hard reasoning (Opus-class). " +
		"Level=2 is more expensive — only use it when the task is complex. " +
		"Level=3 is extra expensive — only use it when the task need high effort" +
		"Sub-agents cannot themselves call this tool — the hierarchy is exactly one layer deep."
}

func (t *AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["description","prompt"],
		"properties":{
			"name":{"type":"string","description":"short nickname"},
			"description":{"type":"string","description":"A short (3-5 word) description of the task"},
			"prompt":{"type":"string","description":"The full task prompt for the sub-agent"},
			"subagent_type":{"type":"string","enum":["explore","general-purpose"],"description":"Which preset profile to use. Defaults to general-purpose. \"explore\" is read-only and good for codebase inspection."},
			"level":{"type":"integer","enum":[1,2,3],"default":1,"description":"Model tier within the parent's provider. 1 = normal (Sonnet / DeepSeek-Flash / equivalent), 2 = big (Opus / DeepSeek-Pro / equivalent). 3 = (level + hard effort mode) Defaults to 1. Use 2 only when the task genuinely needs deeper reasoning."},
            "async_mode": {"type": "bool", "default": false", "description": "let agent run in background, report summary will be appended in conversation later once it done."}
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

	// TODO: in 2.0 this will be a goroutine func.
	out, err := spawner.Spawn(ctx, SpawnRequest{
		Name:      in.Name,
		Kind:      kind,
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
