// Package profiles supplies preset agent.Profile constructors.
//
// A profile picks two tool-name lists — ActiveTools (eager) and DeferredTools
// (lazy via TOOL_SEARCH) — and an LLM target. The tool packages themselves
// don't know about active/deferred; that split is purely a profile-level
// scheduling decision.
//
// Adding a new profile = one function composing name lists from the family
// Names() helpers.
package agent

import (
	"slices"

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

// AgentType enumerates the kinds of agent we know how to bootstrap.
// Profiles in agent/profiles are keyed off these values; the value also
// appears in logs to identify which kind of agent emitted a record.
type AgentType int

const (
	MAIN AgentType = iota
	EXPLORE
	GENERAL_PURPOSE
)

// String returns a short human label suitable for logs and the system prompt.
func (t AgentType) String() string {
	switch t {
	case MAIN:
		return "main"
	case EXPLORE:
		return "explore"
	case GENERAL_PURPOSE:
		return "general"
	default:
		return "unknown"
	}
}

// Profile is the configuration an Agent runs under: which kind of agent it
// is, what system prompt it presents, and which tool *names* are exposed to
// the model.
//
// Tool policy is split into two lists — this split is purely an agent-level
// scheduling decision; the tool packages themselves know nothing about it:
//
//   - ActiveTools are constructed at agent.New() and exposed to the LLM in
//     every Complete call. The model can invoke them with no preamble.
//
//   - DeferredTools are advertised to the model by name only. They are
//     materialized on demand via agent.LoadDeferred (driven by TOOL_SEARCH).
//     Listing a name here is the agent's allowlist for what may be lazily
//     loaded; a profile that omits a name forbids it entirely.
//
// Two agents with the same Profile behave identically — the loop, dispatch,
// and lifecycle are shared in the Agent type; only configuration varies.
type Profile struct {
	Type         AgentType
	SystemPrompt string

	// Tool policy
	ActiveTools   []tools.ToolName
	DeferredTools []tools.ToolName

	// LLM core
	LLMProvider constant.LLMProvider
	LLMModel    constant.Model
	LLMOptions  []llm.Option
}

const mainSystemPrompt = `You are evva, a helpful coding assistant operating
in a terminal. You have full read/write access to the local filesystem and
may use any registered tool. Be concise; prefer running tools over guessing.`

const exploreSystemPrompt = `You are an explorer agent. Your access is
read-only — you may inspect files but never modify them. Answer questions
about the codebase concisely and cite file paths when relevant.`

const generalSystemPrompt = `You are a focused sub-agent. Complete the task
described in the user prompt and return a short done. Stay in scope.`

// Main returns the full-kit profile: fs/shell/meta are active; the rest are
// deferred (loaded on demand via TOOL_SEARCH).
func Main(provider constant.LLMProvider, model constant.Model, options []llm.Option) Profile {
	return Profile{
		Type:         MAIN,
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
func Explore(provider constant.LLMProvider, model constant.Model, options []llm.Option) Profile {
	return Profile{
		Type:         EXPLORE,
		SystemPrompt: exploreSystemPrompt,
		ActiveTools:  []tools.ToolName{tools.READ_FILE, tools.WEB_SEARCH, tools.TREE, tools.GREP},
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}

// General returns a minimal profile carrying only the tool names the caller
// supplies as active. No deferred tools. Useful for narrow-purpose sub-agents.
func General(provider constant.LLMProvider, model constant.Model, options []llm.Option, toolset ...tools.ToolName) Profile {
	return Profile{
		Type:         GENERAL_PURPOSE,
		SystemPrompt: generalSystemPrompt,
		ActiveTools:  toolset,
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}
