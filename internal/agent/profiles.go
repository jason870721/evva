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
	"fmt"
	"slices"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/tools/dev"
	"github.com/johnny1110/evva/internal/tools/memory"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/internal/tools/ux"
	"github.com/johnny1110/evva/internal/toolset"
	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/cron"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/tools/lsp"
	"github.com/johnny1110/evva/pkg/tools/monitor"
	"github.com/johnny1110/evva/pkg/tools/notebook"
	"github.com/johnny1110/evva/pkg/tools/shell"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/tools/util"
	"github.com/johnny1110/evva/pkg/tools/web"
)

// AgentType enumerates the kinds of agent we know how to bootstrap.
// Profiles in agent/profiles are keyed off these values; the value also
// appears in logs to identify which kind of agent emitted a record.
type AgentType int

const (
	MAIN AgentType = iota
	EXPLORE
	GENERAL_PURPOSE
	PLAN
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
	case PLAN:
		return "plan"
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

// Main returns the full-kit profile: fs/shell/meta/skill are active; the rest
// are deferred (loaded on demand via TOOL_SEARCH).
//
// The system prompt is built via sysprompt.MainAgent. Skills are advertised
// (Main is the only agent that surfaces them), and the EVVA.md / USER_PROFILE.md
// memory snapshot is threaded into the prompt under labeled headings. Callers
// pass an empty memdir.Snapshot{} when memory injection is not desired.
//
// Streaming is on by default — the user-facing UX win is large and the
// chunk adapter falls back cleanly for providers without native streaming.
// Callers who want the old buffered behavior can pass WithStream(false) at
// agent construction.
func Main(cfg *config.Config, provider constant.LLMProvider, model constant.Model, skills []sysprompt.SkillRef, mem memdir.Snapshot, options []llm.Option) Profile {
	// nil skills means "auto-load from cfg's skill dirs" — keeps downstream
	// SDK callers (pkg/agent.New passes nil) and cmd/evva from having to
	// re-implement the disk walk. An explicit empty slice still suppresses
	// the # Skills section.
	if skills == nil {
		skills = refsFromRegistry(loadDiskSkillRegistry(cfg))
	}
	activeTools := slices.Concat(fs.Names(), shell.Names(), meta.Names(), skill.Names(), todo.Names())
	// enter/exit_plan_mode are always-active on Main so the model can flip
	// the session into ModePlan without a tool_search round-trip. The
	// worktree pair stays deferred (Phase 10).
	activeTools = append(activeTools, tools.ENTER_PLAN_MODE, tools.EXIT_PLAN_MODE)
	// Auto-memory tools — registered only when the user has the feature
	// enabled. The sysprompt's auto-memory guidance section is gated on the
	// same flag (see ctx.EnableAutoMemory below), so prompt and toolset
	// stay consistent.
	if cfg.GetEnableAutoMemory() {
		activeTools = append(activeTools, memory.Names()...)
	}
	// dev env tools for collect agent feedback
	if cfg.IsDevelopment() {
		activeTools = append(activeTools, dev.Names()...)
	}
	deferredTools := slices.Concat(
		daemon.Names(),
		lsp.Names(),
		monitor.Names(),
		modeDeferredNames(),
		notebook.Names(),
		ux.Names(),
		cron.Names(),
		web.Names(),
		util.Names(),
	)

	ctx := sysprompt.DetectContext(cfg.AppName, cfg.AppHome, cfg.AppEnv)
	ctx.Skills = skills
	ctx.WorkdirMemory = mem.WorkdirMemory
	ctx.UserProfile = mem.UserProfile
	ctx.ProjectMemoryIndex = mem.ProjectMemoryIndex
	ctx.EnableAutoMemory = cfg.GetEnableAutoMemory()
	ctx.DeferredTools = deferredToolSpecs(deferredTools)
	ctx.Model = string(model)
	sp := sysprompt.MainAgent.BuildSystemPrompt(ctx)
	options = append(options, llm.WithSystem(sp))

	return Profile{
		Type:          MAIN,
		SystemPrompt:  sp,
		ActiveTools:   activeTools,
		DeferredTools: deferredTools,
		LLMProvider:   provider,
		LLMModel:      model,
		LLMOptions:    options,
		Stream:        false,
	}
}

// ResolveMainProfile is the single entry point for picking a main-tier
// Profile by persona name. Used by both bootstrap (cmd/evva/main.go) and
// the runtime /profile switch (Agent.SwitchProfile).
//
// Built-in "evva" routes through Main(...) verbatim — the same full-kit
// active/deferred tool lists, the same memdir + skills wiring.
// Disk-loaded main personas route through mainProfileFromDiskAgent which
// uses the def's own tool lists and BuildSystemPrompt body, gated by the
// def's OmitMemory / AdvertiseSkills flags from meta.yml.
//
// Empty name defaults to "evva". Unknown or non-main names return an
// error so callers (bootstrap fallback, the /profile picker) can surface
// the failure.
func ResolveMainProfile(cfg *config.Config, reg *AgentRegistry, name string, skills []sysprompt.SkillRef, mem memdir.Snapshot, options []llm.Option) (Profile, error) {
	if name == "" {
		name = "evva"
	}
	// nil skills means "auto-load from cfg's skill dirs" for every main-tier
	// persona (Main does this too, but disk personas route around it). Lifting
	// it here keeps the public pkg/agent.ResolveMainProfile from re-walking the
	// disk — it can pass nil and rely on this. An explicit empty slice still
	// suppresses the # Skills section.
	if skills == nil {
		skills = refsFromRegistry(loadDiskSkillRegistry(cfg))
	}
	if reg == nil {
		// No registry — only the built-in evva is reachable. Accept the
		// "evva" name; everything else is unknown.
		if name != "evva" {
			return Profile{}, fmt.Errorf("agent: unknown main profile %q (no registry)", name)
		}
		return Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, skills, mem, options), nil
	}
	def, ok := reg.Get(name)
	if !ok {
		return Profile{}, fmt.Errorf("agent: unknown main profile %q", name)
	}
	if !def.IsMain() {
		return Profile{}, fmt.Errorf("agent: %q is not a main-tier persona", name)
	}
	if def.Name == "evva" {
		return Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, skills, mem, options), nil
	}
	return mainProfileFromDiskAgent(def, cfg, cfg.DefaultProvider, cfg.DefaultModel, skills, mem, options), nil
}

// ResolveMainProfileAutoMem is ResolveMainProfile with the EVVA.md /
// USER_PROFILE.md snapshot auto-loaded from cfg, so callers (cmd/evva and the
// public pkg/agent.ResolveMainProfile) don't have to thread a memdir.Snapshot.
// Skills auto-load via ResolveMainProfile's nil path. Returns the resolved
// Profile plus any non-fatal memory-load warnings.
func ResolveMainProfileAutoMem(cfg *config.Config, reg *AgentRegistry, name string, options []llm.Option) (Profile, []string, error) {
	mem := memdir.Load(cfg.WorkDir, cfg.AppHome, cfg.GetEnableAutoMemory())
	prof, err := ResolveMainProfile(cfg, reg, name, nil, mem, options)
	return prof, mem.Warnings, err
}

// mainProfileFromDiskAgent builds a MAIN-tier Profile from a disk-loaded
// AgentDefinition. Mirrors the subagent-tier profileFromDiskAgent in
// spawn.go; the deltas are Type=MAIN, opt-in memory injection, opt-in
// skills advertisement.
//
// Tool lists come straight from the def's ActiveTools / DeferredTools
// (loaded from tools.yml). The deferred catalog is rendered into the
// prompt so disk personas see their lazy-loadable tools the same way
// built-in evva does.
func mainProfileFromDiskAgent(def sysprompt.AgentDefinition, cfg *config.Config, provider constant.LLMProvider, model constant.Model, skills []sysprompt.SkillRef, mem memdir.Snapshot, options []llm.Option) Profile {
	ctx := sysprompt.DetectContext(cfg.AppName, cfg.AppHome, cfg.AppEnv)
	if def.AdvertiseSkills {
		ctx.Skills = skills
	}
	if !def.OmitMemory {
		ctx.WorkdirMemory = mem.WorkdirMemory
		ctx.UserProfile = mem.UserProfile
		ctx.ProjectMemoryIndex = mem.ProjectMemoryIndex
		ctx.EnableAutoMemory = cfg.GetEnableAutoMemory()
	}
	ctx.DeferredTools = deferredToolSpecs(def.DeferredTools)
	ctx.Model = string(model)
	body := def.BuildSystemPrompt(ctx)
	sp := sysprompt.ComposeDiskMainPrompt(body, ctx, def)
	options = append(options, llm.WithSystem(sp))
	return Profile{
		Type:          MAIN,
		SystemPrompt:  sp,
		ActiveTools:   def.ActiveTools,
		DeferredTools: def.DeferredTools,
		LLMProvider:   provider,
		LLMModel:      model,
		LLMOptions:    options,
	}
}

// modeDeferredNames returns the mode-package tools that stay deferred on
// the Main profile. enter/exit_plan_mode are pulled out into ActiveTools
// (they need to be wire-callable without a tool_search round-trip);
// worktree stays deferred until Phase 10 lands a real implementation.
func modeDeferredNames() []tools.ToolName {
	out := make([]tools.ToolName, 0, 2)
	for _, n := range mode.Names() {
		if n == tools.ENTER_PLAN_MODE || n == tools.EXIT_PLAN_MODE {
			continue
		}
		out = append(out, n)
	}
	return out
}

// deferredToolSpecs flattens a list of deferred tool names into the prompt
// shape sysprompt.PromptContext consumes. Each name is resolved through
// toolset.Describe — names that don't resolve (unknown, registration race)
// are dropped rather than erroring; the resulting prompt simply omits them.
func deferredToolSpecs(names []tools.ToolName) []sysprompt.DeferredToolSpec {
	out := make([]sysprompt.DeferredToolSpec, 0, len(names))
	for _, n := range names {
		d, err := toolset.Describe(n)
		if err != nil {
			continue
		}
		out = append(out, sysprompt.DeferredToolSpec{
			Name:        d.Name,
			Description: d.Description,
			Schema:      d.Schema,
		})
	}
	return out
}

// Explore returns a read-only profile: just READ_FILE / GREP / TREE, plus
// WEB_SEARCH for docs lookup. Useful for sub-agents whose job is to inspect
// without risk of modification.
//
// The Explore prompt is self-contained (mirrors ref TS Explore agent) and
// does not include EVVA.md / USER_PROFILE.md — sysprompt.ExploreAgent
// declares OmitMemory: true.
func Explore(cfg *config.Config, provider constant.LLMProvider, model constant.Model, options []llm.Option) Profile {
	ctx := sysprompt.DetectContext(cfg.AppName, cfg.AppHome, cfg.AppEnv)
	ctx.Model = string(model)
	sp := sysprompt.ExploreAgent.BuildSystemPrompt(ctx)
	options = append(options, llm.WithSystem(sp))

	return Profile{
		Type:         EXPLORE,
		SystemPrompt: sp,
		ActiveTools:  []tools.ToolName{tools.READ_FILE, tools.WEB_SEARCH, tools.GLOB, tools.TREE, tools.GREP, tools.JSON_QUERY},
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}

// Plan returns a read-only profile for design-phase planning work — same
// tool kit as Explore (read, web_search, glob, tree, grep, json_query)
// plus an architect-flavored system prompt that asks for a step-by-step
// plan and a critical-files list. Used by the main agent during plan-mode
// Phase 2 (Design) to delegate per-perspective design takes.
//
// Plan deliberately does not get edit/write/enter_plan_mode/exit_plan_mode
// — its job is to explore and recommend, not modify state. Like Explore,
// the prompt does not include EVVA.md / USER_PROFILE.md
// (sysprompt.PlanAgent declares OmitMemory: true).
func Plan(cfg *config.Config, provider constant.LLMProvider, model constant.Model, options []llm.Option) Profile {
	ctx := sysprompt.DetectContext(cfg.AppName, cfg.AppHome, cfg.AppEnv)
	ctx.Model = string(model)
	sp := sysprompt.PlanAgent.BuildSystemPrompt(ctx)
	options = append(options, llm.WithSystem(sp))

	return Profile{
		Type:         PLAN,
		SystemPrompt: sp,
		ActiveTools:  []tools.ToolName{tools.READ_FILE, tools.WEB_SEARCH, tools.GLOB, tools.TREE, tools.GREP, tools.JSON_QUERY},
		LLMProvider:  provider,
		LLMModel:     model,
		LLMOptions:   options,
	}
}

// General returns a minimal profile carrying only the tool names the caller
// supplies as active. No deferred tools. Useful for narrow-purpose sub-agents.
//
// Like Explore, the General prompt does not include EVVA.md / USER_PROFILE.md.
func General(cfg *config.Config, provider constant.LLMProvider, model constant.Model, options []llm.Option, toolset ...tools.ToolName) Profile {
	ctx := sysprompt.DetectContext(cfg.AppName, cfg.AppHome, cfg.AppEnv)
	ctx.Model = string(model)
	sp := sysprompt.GeneralAgent.BuildSystemPrompt(ctx)
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
