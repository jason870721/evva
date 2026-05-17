// Package profiles supplies preset agent.Profile constructors.
//
// A profile picks two tool-name lists — ActiveTools (eager) and DeferredTools
// (lazy via TOOL_SEARCH) — an LLM target, and a system prompt. Each profile
// builds its own system prompt internally via the sysprompt package; callers
// never pass a sysprompt string in. The invariant: a distinct system prompt
// always lives behind a distinct profile constructor — never as an ad-hoc
// input — so two agents on the same Profile behave identically.
//
// Adding a new profile = one function composing name lists from the family
// Names() helpers plus a buildSysPrompt call.
package agent

import (
	"slices"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/cron"
	"github.com/johnny1110/evva/internal/tools/dev"
	"github.com/johnny1110/evva/internal/tools/fs"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/internal/tools/monitor"
	"github.com/johnny1110/evva/internal/tools/notebook"
	"github.com/johnny1110/evva/internal/tools/shell"
	"github.com/johnny1110/evva/internal/tools/skill"
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

	// Stream selects the streaming completion path. When true the agent
	// calls llm.Client.Stream and forwards each delta to the event sink
	// as KindTextChunk / KindThinkingChunk; when false it calls Complete
	// and emits a single KindText / KindThinking after the turn assembles.
	Stream bool
}

// buildSysPrompt is the shared sysprompt construction path. Each profile
// constructor invokes it with its own AgentType so the section toggles match
// the agent's responsibilities — root agents get the full harness, subagents
// get the minimum needed to orient.
//
// Skills are advertised only on MAIN. Subagents intentionally don't see the
// catalog: skill invocation is the root's job, and pushing the list into
// every subagent prompt would bloat their context for no benefit.
func buildSysPrompt(t AgentType, cfg *config.AppConfig, skills []sysprompt.SkillRef) string {
	in := sysprompt.Default(cfg.AppName, cfg.EvvaHome, cfg.AppEnv)
	switch t {
	case MAIN:
		in.Skills = skills
	case EXPLORE, GENERAL_PURPOSE:
		// Subagents skip the full harness + task-planning chapters and
		// never advertise skills. Tools-guide stays on because it covers
		// TOOL_SEARCH usage, which both subagent kinds rely on.
		in.IncludeHarness = false
		in.IncludeTaskPlanning = false
		in.Skills = nil
	}
	return sysprompt.Build(in)
}

// Main returns the full-kit profile: fs/shell/meta/skill are active; the rest
// are deferred (loaded on demand via TOOL_SEARCH).
//
// The system prompt is built internally from cfg + skills — callers no
// longer pass a prompt string. Different prompt content = different
// AgentProfile.
//
// Streaming is on by default — the user-facing UX win is large and the
// chunk adapter falls back cleanly for providers without native streaming.
// Callers who want the old buffered behavior can pass WithStream(false) at
// agent construction.
func Main(cfg *config.AppConfig, provider constant.LLMProvider, model constant.Model, skills []sysprompt.SkillRef, options []llm.Option) Profile {
	sp := buildSysPrompt(MAIN, cfg, skills)
	options = append(options, llm.WithSystem(sp))

	activeTools := slices.Concat(fs.Names(), shell.Names(), meta.Names(), skill.Names())
	// dev env tools for collect agent feedback
	if cfg.IsDevelopment() {
		activeTools = append(activeTools, dev.Names()...)
	}

	return Profile{
		Type:         MAIN,
		SystemPrompt: sp,
		ActiveTools:  activeTools,
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
		Stream:      false,
	}
}

// Explore returns a read-only profile: just READ_FILE / GREP / TREE, plus
// WEB_SEARCH for docs lookup. Useful for sub-agents whose job is to inspect
// without risk of modification.
func Explore(cfg *config.AppConfig, provider constant.LLMProvider, model constant.Model, options []llm.Option) Profile {
	sp := buildSysPrompt(EXPLORE, cfg, nil)
	options = append(options, llm.WithSystem(sp))

	return Profile{
		Type:         EXPLORE,
		SystemPrompt: sp,
		ActiveTools:  []tools.ToolName{tools.READ_FILE, tools.WEB_SEARCH, tools.TREE, tools.GREP},
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}

// General returns a minimal profile carrying only the tool names the caller
// supplies as active. No deferred tools. Useful for narrow-purpose sub-agents.
func General(cfg *config.AppConfig, provider constant.LLMProvider, model constant.Model, options []llm.Option, toolset ...tools.ToolName) Profile {
	sp := buildSysPrompt(GENERAL_PURPOSE, cfg, nil)
	options = append(options, llm.WithSystem(sp))

	return Profile{
		Type:         GENERAL_PURPOSE,
		SystemPrompt: sp,
		ActiveTools:  toolset,
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}
