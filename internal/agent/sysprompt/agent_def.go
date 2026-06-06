package sysprompt

import "github.com/johnny1110/evva/pkg/tools"

// AgentDefinition is the Go-side seam for a built-in agent. Phase 2's
// internal/agent/loader/ will define an AgentRegistry interface that this
// struct satisfies; for Phase 0 it is a concrete struct since the only
// agents are built-ins. Disk-authored agents (Phase 2) construct an
// AgentDefinition where BuildSystemPrompt is a closure that returns the
// on-disk system_prompt.md body.
//
// Field semantics:
//
//   - Name              wire identifier ("evva", "explore", "general-purpose").
//                       Same string the Agent tool's subagent_type enum will
//                       accept (Phase 2 unifies these). Lowercase, hyphenated.
//   - WhenToUse         description shown in the Agent tool's catalog so a
//                       parent agent knows what to delegate. Empty for Main
//                       (Main is not delegated to).
//   - OmitMemory        subagents skip <workdir>/EVVA.md and
//                       <evvaHome>/USER_PROFILE.md. Matches ref TS
//                       omitClaudeMd: true on Explore/Plan.
//   - AdvertiseSkills   only the Main agent surfaces the skill catalog.
//                       Subagents don't (it would bloat their context).
//   - BuildSystemPrompt assembles the prompt from ctx. Each built-in
//                       closure-captures its own per-agent text fragments.
type AgentDefinition struct {
	Name              string
	WhenToUse         string
	OmitMemory        bool
	AdvertiseSkills   bool
	BuildSystemPrompt func(ctx PromptContext) string

	// As controls where this agent appears. Values:
	//   "main"     — selectable via /profile (Phase 6 picker).
	//   "subagent" — invokable via the Agent tool's subagent_type.
	// Both can be set; for built-ins the slice is fixed (Main is main-only;
	// Explore/General are subagent-only). Disk agents declare this via
	// meta.yml's `as:` field.
	As []string

	// ActiveTools / DeferredTools name the tools this agent's profile loads.
	// Empty means "use the built-in constructor's default" (Main, Explore,
	// General supply their own lists in agent.Main/Explore/General). For
	// disk-loaded agents these come from tools.yml.
	ActiveTools   []tools.ToolName
	DeferredTools []tools.ToolName

	// Model is the optional model override declared in meta.yml. Empty
	// means "inherit from parent" (existing built-in behavior).
	Model string

	// PromptBody is the raw persona prompt body before composition, set when
	// the definition came from disk (system_prompt.md) or a public AgentSpec.
	// Empty for built-ins whose prompt is assembled from internal fragments —
	// their prompt isn't recoverable as a single string. Lets a definition
	// round-trip back to a public AgentDefinition for inspection.
	PromptBody string

	// LongRunning marks a persona that stays live for a very long time — a swarm
	// member runs for weeks. The prompt builder then drops fragments that would
	// drift across rebuilds and bust the prompt-cache prefix: currently the
	// "- Today:" date in the environment section (gated via PromptContext.OmitDate
	// in mainProfileFromDiskAgent). Built-in evva leaves it false; internal/swarm
	// sets it at member registration (RP-5).
	LongRunning bool
}

// IsMain reports whether this agent appears in the /profile picker (Phase 6).
func (d AgentDefinition) IsMain() bool {
	for _, v := range d.As {
		if v == "main" {
			return true
		}
	}
	return false
}

// IsSubagent reports whether this agent is invokable via the Agent tool's
// subagent_type parameter.
func (d AgentDefinition) IsSubagent() bool {
	for _, v := range d.As {
		if v == "subagent" {
			return true
		}
	}
	return false
}

// Built-in agent registry. Phase 11 adds PlanAgent for design-phase work
// inside plan mode; Phase 6 may add more main-tier personas (nono, noen)
// as siblings.
var (
	MainAgent = AgentDefinition{
		Name:              "evva",
		WhenToUse:         "", // Evva is the built-in root persona — not delegated to.
		OmitMemory:        false,
		AdvertiseSkills:   true,
		BuildSystemPrompt: buildMainPrompt,
		As:                []string{"main"},
	}

	ExploreAgent = AgentDefinition{
		Name:              subagentExplore,
		WhenToUse:         exploreWhenToUse,
		OmitMemory:        true,
		AdvertiseSkills:   false,
		BuildSystemPrompt: buildExplorePrompt,
		As:                []string{"subagent"},
	}

	GeneralAgent = AgentDefinition{
		Name:              subagentGeneral,
		WhenToUse:         generalWhenToUse,
		OmitMemory:        true,
		AdvertiseSkills:   false,
		BuildSystemPrompt: buildGeneralPrompt,
		As:                []string{"subagent"},
	}

	PlanAgent = AgentDefinition{
		Name:              subagentPlan,
		WhenToUse:         planWhenToUse,
		OmitMemory:        true,
		AdvertiseSkills:   false,
		BuildSystemPrompt: buildPlanPrompt,
		As:                []string{"subagent"},
	}
)
