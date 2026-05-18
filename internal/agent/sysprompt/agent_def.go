package sysprompt

// AgentDefinition is the Go-side seam for a built-in agent. Phase 2's
// internal/agent/loader/ will define an AgentRegistry interface that this
// struct satisfies; for Phase 0 it is a concrete struct since the only
// agents are built-ins. Disk-authored agents (Phase 2) construct an
// AgentDefinition where BuildSystemPrompt is a closure that returns the
// on-disk system_prompt.md body.
//
// Field semantics:
//
//   - Name              wire identifier ("main", "explore", "general-purpose").
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
}

// Built-in agent registry. Three vars today; Phase 7 adds PlanAgent;
// Phase 6 may add more main-tier personas (nono, noen) as siblings.
var (
	MainAgent = AgentDefinition{
		Name:              "main",
		WhenToUse:         "", // Main is the root persona — not delegated to.
		OmitMemory:        false,
		AdvertiseSkills:   true,
		BuildSystemPrompt: buildMainPrompt,
	}

	ExploreAgent = AgentDefinition{
		Name:              subagentExplore,
		WhenToUse:         exploreWhenToUse,
		OmitMemory:        true,
		AdvertiseSkills:   false,
		BuildSystemPrompt: buildExplorePrompt,
	}

	GeneralAgent = AgentDefinition{
		Name:              subagentGeneral,
		WhenToUse:         generalWhenToUse,
		OmitMemory:        true,
		AdvertiseSkills:   false,
		BuildSystemPrompt: buildGeneralPrompt,
	}
)
