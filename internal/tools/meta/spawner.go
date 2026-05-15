package meta

import (
	"context"
	"errors"
)

// SpawnRequest is the parsed AGENT-tool input the spawner needs to actually
// run a subagent. Passed as a struct so future fields (model overrides,
// timeout, isolation mode) don't churn the SubagentSpawner interface.
type SpawnRequest struct {
	Name string
	// Kind selects a preset profile: "explore" or "general-purpose".
	// Empty/unknown values are the spawner's responsibility (return an error).
	Kind string

	// 3~5 words desc
	Desc string

	// Prompt is the task the subagent should accomplish. Must be non-empty.
	Prompt string

	// Level selects the model tier within the parent's provider:
	//   1 = small  (smaller model — Sonnet, DeepSeek-Flash, ...).
	//   2 = medium (larger model — Opus, DeepSeek-Pro, ...).
	//   3 = Large  (larger model — Opus + hard effort, DeepSeek-Pro  + hard effort, ...).
	// Zero defaults to 1; out-of-range values clamp via
	// constant.LLMProvider.ModelForLevel.
	Level int

	AsyncMode bool // default = false
}

// SubagentSpawner is the agent-layer dependency that the AGENT tool calls
// to construct and run a child agent.
//
// The interface lives in meta (not in the agent package) so the agent
// package can implement it without causing the import cycle that would
// otherwise arise from meta importing agent. AgentTool reads the spawner
// through a late-binding lookup (see NewAgent) so the agent can install
// itself as the spawner after its own struct exists.
type SubagentSpawner interface {
	// Spawn creates a subagent per the request, runs it, and returns the
	// child's final assistant text on success.
	Spawn(ctx context.Context, req SpawnRequest) (string, error)
}

// ErrSubagentForbidden is returned by Spawn when the calling agent is
// itself a subagent — only the root agent may spawn subagents. The AGENT
// tool surfaces this as a recoverable Result.IsError so the model can
// adjust its plan instead of aborting the loop.
var ErrSubagentForbidden = errors.New("meta: subagents cannot spawn subagents")
