// Package profiles supplies preset agent.Profile constructors.
//
// A profile picks two tool-name lists — ActiveTools (eager) and DeferredTools
// (lazy via TOOL_SEARCH) — and an LLM target. The tool packages themselves
// don't know about active/deferred; that split is purely a profile-level
// scheduling decision.
//
// Adding a new profile = one function composing name lists from the family
// Names() helpers.
package profiles

import (
	"slices"

	"github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/llm"
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

const mainSystemPrompt = `You are evva, a helpful coding assistant operating
in a terminal. You have full read/write access to the local filesystem and
may use any registered tool. Be concise; prefer running tools over guessing.`

const exploreSystemPrompt = `You are an explorer agent. Your access is
read-only — you may inspect files but never modify them. Answer questions
about the codebase concisely and cite file paths when relevant.`

const generalSystemPrompt = `You are a focused sub-agent. Complete the task
described in the user prompt and return a short summary. Stay in scope.`

// Main returns the full-kit profile: fs/shell/meta are active; the rest are
// deferred (loaded on demand via TOOL_SEARCH).
func Main(provider constant.LLMProvider, model constant.Model, options []llm.Option) agent.Profile {
	return agent.Profile{
		Type:         agent.MAIN,
		SystemPrompt: mainSystemPrompt,
		ActiveTools:  slices.Concat(fs.Names(), shell.Names(), meta.Names()),
		DeferredTools: slices.Concat(
			task.Names(),
			monitor.Names(),
			mode.Names(),
			notebook.Names(),
			ux.Names(),
			cron.Names(),
			web.Names(),
		),
		LLMProvider: provider,
		LLMModel:    model,
		LLMOptions:  options,
	}
}

// Explore returns a read-only profile: just READ_FILE (and WEB_SEARCH for
// docs lookup). Useful for sub-agents whose job is to inspect without risk
// of modification.
func Explore(provider constant.LLMProvider, model constant.Model, options []llm.Option) agent.Profile {
	return agent.Profile{
		Type:         agent.EXPLORE,
		SystemPrompt: exploreSystemPrompt,
		ActiveTools:  []tools.ToolName{tools.READ_FILE, tools.WEB_SEARCH},
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}

// General returns a minimal profile carrying only the tool names the caller
// supplies as active. No deferred tools. Useful for narrow-purpose sub-agents.
func General(toolset ...tools.ToolName) agent.Profile {
	return agent.Profile{
		Type:         agent.GENERAL_PURPOSE,
		SystemPrompt: generalSystemPrompt,
		ActiveTools:  toolset,
	}
}
