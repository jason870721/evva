package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
)

// SpawnerLookup is the function shape a ToolState method (or any closure)
// satisfies to provide late-bound access to a SubagentSpawner. AgentTool
// keeps the lookup, not the spawner, so the order in which the agent and
// the tool are constructed doesn't matter — the spawner can be installed
// after the tool already exists.
type SpawnerLookup func() SubagentSpawner

// AgentTool is the LLM-facing handle for spawning subagents. The actual
// work is delegated to a SubagentSpawner installed by the agent layer.
// The subagent's lifecycle (init, phase updates, terminal report) is
// surfaced through the parent's DaemonState — see internal/agent/agent_daemon.go.
type AgentTool struct {
	lookup SpawnerLookup
}

// NewAgent constructs an AgentTool that reads its spawner via lookup at
// Execute time. lookup may be nil (yields a clear runtime error if the
// model invokes the tool); it may also return nil (same outcome).
func NewAgent(lookup SpawnerLookup) *AgentTool {
	return &AgentTool{lookup: lookup}
}

func (t *AgentTool) Name() string { return string(tools.AGENT) }

func (t *AgentTool) Description() string {
	return `Launch a new agent to handle complex, multi-step tasks. Each agent type has specific capabilities and tools available to it.

Available agent types and the tools they have access to:
- explore: Fast read-only search agent for locating code. Use it to find files by pattern (e.g. "src/**/*.go"), grep for symbols or keywords, or answer "where is X defined / which files reference Y." Do NOT use it for code review, cross-file consistency checks, or open-ended analysis — it reads excerpts rather than whole files and will miss content past its read window. When calling, specify search breadth: "quick" for a single targeted lookup, "medium" for moderate exploration, or "very thorough" to search across multiple locations and naming conventions.
- plan: Software architect agent for designing implementation plans. Use when you need a step-by-step plan and a list of critical files to modify, especially during plan-mode Phase 2 (Design). Read-only — returns a written plan, never edits files. Multiple Plan agents in parallel can produce different perspectives (simplicity vs performance vs maintainability) on the same problem.
- general-purpose: General-purpose agent for researching complex questions, searching for code, and executing multi-step tasks. Use when searching for a keyword or file and you are not confident you will find the right match in the first few tries.

When using the agent tool, specify a subagent_type parameter to select which agent type to use. If omitted, the general-purpose agent is used.

When NOT to use the agent tool:
- If you want to read a specific file path, use the read or glob tool instead — finding the match is faster.
- If you are searching for a specific class definition like "class Foo", use grep or glob directly — faster than a subagent round-trip.
- If you are searching for code within a specific file or set of 2–3 files, use read instead — faster.
- Trivial work: typo fixes, single-line edits, status checks. Three messages is faster than one subagent.

Usage notes:
- Always include a short description (3–5 words) summarizing what the agent will do.
- Launch multiple agents concurrently whenever possible — emit several agent tool_use blocks in one assistant turn when the work is independent. They execute in parallel.
- When the agent is done, it returns a single message back to you. The result is NOT visible to the user. To show the user the result, send a text message back with a concise summary.
- You can optionally run agents in the background by setting ` + "`async_mode: true`" + `. When async, the spawner returns an ack immediately and the eventual summary is injected into your next turn — do NOT sleep, poll, or proactively check on its progress. Continue with other work or respond to the user instead.
- Foreground vs background: use foreground (default) when you need the agent's results before you can proceed; use async when you have genuinely independent work to do in parallel.
- Each agent invocation starts fresh — provide a complete task description in ` + "`prompt`" + `.
- The agent's outputs should generally be trusted.
- Clearly tell the agent whether you expect it to write code or just to do research (search, file reads, web fetches, etc.), since it is not aware of the user's intent.
- If the user specifies that they want you to run agents "in parallel", you MUST send a single message with multiple agent tool_use blocks. For example, if you need to launch both a build-validator agent and a test-runner agent in parallel, send a single message with both tool calls.
- ` + "`level: 2`" + ` costs more — only request it when the task genuinely needs deeper reasoning (subtle bug hunts, architectural calls). Routine searches stay at level 1.
- Subagents cannot spawn subagents — the hierarchy is exactly one layer deep.
- With ` + "`isolation: \"worktree\"`" + `, the subagent runs inside a fresh git worktree created at ` + "`<repo>/.evva/worktrees/`" + `. The worktree is auto-removed if the subagent makes no changes; otherwise the path and branch are returned in the result so the user can inspect or merge.
- With ` + "`isolation: \"sandbox\"`" + `, the subagent gets that same worktree AND runs its bash commands inside a container, so it cannot touch the rest of the host filesystem or (when configured) the network. Use it for work on untrusted input or unreviewed install steps. Requires a configured container runtime; the spawn fails rather than silently running unsandboxed.

## Writing the prompt

Brief the agent like a smart colleague who just walked into the room — it hasn't seen this conversation, doesn't know what you've tried, doesn't understand why this task matters.
- Explain what you're trying to accomplish and why.
- Describe what you've already learned or ruled out.
- Give enough context about the surrounding problem that the agent can make judgment calls rather than just following a narrow instruction.
- If you need a short response, say so ("report in under 200 words").
- Lookups: hand over the exact command. Investigations: hand over the question — prescribed steps become dead weight when the premise is wrong.

Terse command-style prompts produce shallow, generic work.

**Never delegate understanding.** Don't write "based on your findings, fix the bug" or "based on the research, implement it." Those phrases push synthesis onto the agent instead of doing it yourself. Write prompts that prove you understood: include file paths, line numbers, what specifically to change.

Example usage:

<example>
user: "What's left on this branch before we can ship?"
assistant: <thinking>A survey question across git state, tests, and config. I'll delegate it and ask for a short report so the raw command output stays out of my context.</thinking>
agent({
  "name": "ship-audit",
  "description": "Branch ship-readiness audit",
  "subagent_type": "general-purpose",
  "prompt": "Audit what's left before this branch can ship. Check: uncommitted changes, commits ahead of main, whether tests exist, whether CI-relevant files changed. Report a punch list — done vs. missing. Under 200 words."
})
<commentary>
The prompt is self-contained: it states the goal, lists what to check, and caps the response length. The agent's report comes back as the tool result; relay the findings to the user.
</commentary>
</example>

<example>
user: "where is the auth middleware wired?"
assistant: <thinking>I'll use explore — read-only, fast, and the answer is a file/line lookup, not a synthesis task.</thinking>
agent({
  "name": "auth-locate",
  "description": "Find auth middleware",
  "subagent_type": "explore",
  "prompt": "Locate the file and exact line where the auth middleware is wired into the HTTP router. Report file:line for both the middleware function definition and its registration. Under 100 words."
})
</example>`
}

func (t *AgentTool) Schema() json.RawMessage {
	types := t.subagentTypes()
	enumJSON, _ := json.Marshal(types)
	return json.RawMessage(fmt.Sprintf(`{
		"type":"object",
		"additionalProperties":false,
		"required":["name", "description","prompt"],
		"properties":{
			"name":{"type":"string","description":"A short nickname"},
			"description":{"type":"string","description":"A short (3-5 word) description of the task"},
			"prompt":{"type":"string","description":"The full task prompt for the sub-agent"},
			"subagent_type":{"type":"string","enum":%s,"description":"Which preset profile to use. Defaults to general-purpose. \"explore\" is read-only and good for codebase inspection."},
			"level":{"type":"integer","enum":[1,2],"default":1,"description":"Model tier within the parent's provider. 1=general, 2=thinking Defaults to 1. Use 2 only when the task genuinely needs deeper reasoning."},
			"async_mode":{"type":"boolean","default":false,"description":"Let the subagent run in the background; the spawner returns an ack immediately and the eventual summary is injected into the parent's next turn."},
			"isolation":{"type":"string","enum":["worktree","sandbox"],"description":"Isolation strategy. \"worktree\" runs the subagent in a fresh git worktree under <repo>/.evva/worktrees/ on its own branch; the worktree is auto-removed when the subagent makes no changes, otherwise the path and branch are returned in the result. \"sandbox\" adds OS-level isolation on top: the same worktree, but bash runs inside a container so the rest of the host is out of reach. Requires a configured container runtime."}
		}
	}`, enumJSON))
}

// subagentTypes resolves the enum members for the schema's subagent_type
// field. Reads through the lookup so the registry contents at Schema()
// time win — Phase 6 disk subagents become wire-callable as soon as the
// registry sees them. Falls back to the built-in pair when no spawner is
// installed (tests, degenerate setups) so the schema is always valid.
func (t *AgentTool) subagentTypes() []string {
	if t.lookup != nil {
		if sp := t.lookup(); sp != nil {
			if names := sp.SubagentTypes(); len(names) > 0 {
				return names
			}
		}
	}
	return []string{"explore", "plan", "general-purpose"}
}

type agentInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
	Level        int    `json:"level"`
	AsyncMode    bool   `json:"async_mode"`
	Isolation    string `json:"isolation"`
}

func (t *AgentTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
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
	logger.Info("subagent.spawn", "kind", kind, "name", in.Name, "async", in.AsyncMode, "level", in.Level)

	isolation := strings.ToLower(strings.TrimSpace(in.Isolation))
	if isolation != "" && isolation != "worktree" && isolation != "sandbox" {
		return tools.Result{IsError: true, Content: fmt.Sprintf(`agent: unsupported isolation %q (want "worktree", "sandbox", or omit)`, in.Isolation)}, nil
	}

	out, err := spawner.Spawn(ctx, SpawnRequest{
		Name:      in.Name,
		Kind:      kind,
		Desc:      in.Description,
		Prompt:    in.Prompt,
		Level:     in.Level,
		AsyncMode: in.AsyncMode, // turn this off in dev mode.
		Isolation: isolation,
	})

	if err != nil {
		if errors.Is(err, ErrSubagentForbidden) {
			// Recoverable — model can ditch the subagent plan and try something else.
			logger.Warn("subagent.fail", "kind", kind, "reason", "forbidden", "err", err)
			return tools.Result{IsError: true, Content: fmt.Sprintf("%s \n [%s]", out, err.Error())}, nil
		}
		// Other errors abort the parent loop — they are Go-level failures
		// (LLM transport, tool panics) the model can't recover from.
		logger.Warn("subagent.fail", "kind", kind, "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("agent: %s %v", out, err)}, err
	}
	// If this is a async mode agent, output will be like "subagent running in background."
	return tools.Result{Content: out}, nil
}
