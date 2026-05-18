package sysprompt_test

// Drift guard: the prompt-side tool name constants in toolnames.go must
// match the canonical wire values in internal/tools/name.go. If anyone
// renames a tool's wire string without updating the prompt, this test
// fails at CI rather than silently shipping a stale prompt.
//
// Lives in package sysprompt_test (external test package) so the
// production sysprompt build never imports internal/tools — keeps the
// dependency arrow one-way.

import (
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/tools"
)

// We cannot access the unexported name* constants directly from the
// _test package. Instead, exercise the public surface (buildMainPrompt
// via the AgentDefinition) and assert each canonical tools.ToolName
// string appears in the rendered prompt. If a constant in toolnames.go
// drifts from the canonical value, the corresponding ToolName won't
// appear and this test fails.
//
// This is weaker than a per-constant equality check but it's enough:
// the link we actually care about is "the prompt mentions the right
// wire string." A pure equality check between unexported consts and the
// public ToolName values would need either an exported accessor or
// reflection over package internals — both costlier than this.

func TestToolNamesAppearInMainPrompt(t *testing.T) {
	ctx := sysprompt.PromptContext{
		AgentName: "evva",
		OS:        "darwin",
		Shell:     "zsh",
		WorkDir:   "/tmp",
		EvvaHome:  "/tmp/.evva",
		Env:       "dev", // include dev section so `feedback` is in the prompt
	}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)

	required := []tools.ToolName{
		tools.READ_FILE,
		tools.WRITE_FILE,
		tools.EDIT_FILE,
		tools.BASH,
		tools.GREP,
		tools.TREE,
		tools.AGENT,
		tools.TOOL_SEARCH,
		tools.SKILL,
		tools.WEB_SEARCH,
		tools.WEB_FETCH,
		tools.JSON_QUERY,
		tools.CALC,
		tools.FEEDBACK,
		tools.TASK_CREATE,
		tools.TASK_UPDATE,
		tools.TASK_LIST,
	}
	for _, name := range required {
		if !contains(prompt, string(name)) {
			t.Errorf("main prompt missing canonical tool wire name %q — likely drift between toolnames.go and internal/tools/name.go", name)
		}
	}
}

func TestExploreSubagentNameMatchesAgentDefinition(t *testing.T) {
	// Main agent's tools-guide quotes `subagent_type: "explore"`; the
	// AgentDefinition exposes the same string as ExploreAgent.Name. Both
	// must agree or the parent's prompt will reference an unknown
	// subagent kind.
	if sysprompt.ExploreAgent.Name != "explore" {
		t.Errorf("ExploreAgent.Name drift: got %q, want %q", sysprompt.ExploreAgent.Name, "explore")
	}

	ctx := sysprompt.PromptContext{AgentName: "evva"}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)
	if !contains(prompt, `subagent_type: "explore"`) {
		t.Errorf("main prompt should reference subagent_type: \"explore\" by literal string")
	}
}

func TestGeneralSubagentNameMatchesAgentDefinition(t *testing.T) {
	if sysprompt.GeneralAgent.Name != "general-purpose" {
		t.Errorf("GeneralAgent.Name drift: got %q, want %q", sysprompt.GeneralAgent.Name, "general-purpose")
	}
}

func contains(haystack, needle string) bool {
	// Tiny local helper so this test file doesn't drag in another import.
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
