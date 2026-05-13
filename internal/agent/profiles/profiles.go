// Package profiles supplies preset agent.Profile constructors.
//
// Profiles declare which tool *names* belong to each kind of agent — they
// never name individual tool instances or constructors. Resolution from
// names to instances happens once at agent init, inside tools.Build, which
// owns the state-allocation rules for stateful tool groups.
//
// Adding a new profile = one function that picks bundles.
package profiles

import (
	"slices"

	"github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/internal/tools"
)

const mainSystemPrompt = `You are evva, a helpful coding assistant operating
in a terminal. You have full read/write access to the local filesystem and
may use any registered tool. Be concise; prefer running tools over guessing.`

const exploreSystemPrompt = `You are an explorer agent. Your access is
read-only — you may inspect files but never modify them. Answer questions
about the codebase concisely and cite file paths when relevant.`

const generalSystemPrompt = `You are a focused sub-agent. Complete the task
described in the user prompt and return a short summary. Stay in scope.`

// Main returns the full-kit profile: every active tool plus every deferred
// tool. Pass extras to append additional tool names beyond the default kit.
func Main(extras ...tools.ToolName) agent.Profile {
	return agent.Profile{
		Type:         agent.MAIN,
		SystemPrompt: mainSystemPrompt,
		Tools:        slices.Concat(tools.All(), extras),
	}
}

// Explore returns a read-only profile: just READ_FILE. Useful for sub-agents
// whose job is to inspect the codebase without risk of modification.
func Explore() agent.Profile {
	return agent.Profile{
		Type:         agent.EXPLORE,
		SystemPrompt: exploreSystemPrompt,
		Tools:        tools.ReadOnly(),
	}
}

// General returns a minimal profile carrying only the tool names the caller
// supplies. Useful for narrow-purpose sub-agents that don't need fs access.
func General(toolset ...tools.ToolName) agent.Profile {
	return agent.Profile{
		Type:         agent.GENERAL_PURPOSE,
		SystemPrompt: generalSystemPrompt,
		Tools:        toolset,
	}
}
