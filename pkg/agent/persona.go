package agent

import (
	agent_impl "github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/pkg/tools"
)

// AgentDefinition is the public, closure-free description of a persona — a
// main-tier agent (selectable via /profile) and/or a subagent kind (invokable
// via the Agent tool's subagent_type). Register one on an AgentRegistry to
// teach the runtime a persona authored in your own Go code; on-disk personas
// under <AppHome>/agents/{name}/ produce the same shape via LoadDiskAgents.
//
// For a main persona, SystemPrompt is composed with evva's identity +
// environment + memory + skills sections around your body (the persona brings
// its own conduct rules). For a subagent it is used as the body directly. This
// mirrors how on-disk agents behave.
type AgentDefinition struct {
	// Name is the wire identifier ("nono", "noen", ...). Lowercase,
	// hyphenated. Matches the /profile label and the Agent tool's
	// subagent_type value.
	Name string

	// WhenToUse tells a parent agent what to delegate to this persona; shown
	// in the Agent tool's catalog. Leave empty for a main-only persona.
	WhenToUse string

	// As controls visibility: "main" (in the /profile picker), "subagent"
	// (invokable via the Agent tool), or both.
	As []string

	// InjectMemory opts the persona into EVVA.md / USER_PROFILE.md injection.
	// Main personas usually want this true; subagents usually false.
	InjectMemory bool

	// AdvertiseSkills surfaces the installed skill catalog in the prompt.
	// Usually only main personas set this.
	AdvertiseSkills bool

	// ActiveTools are eagerly exposed to the model; DeferredTools are
	// advertised by name and fetched on demand via tool_search.
	ActiveTools   []tools.ToolName
	DeferredTools []tools.ToolName

	// Model optionally overrides the provider/model the persona runs under.
	// Empty inherits the parent agent's model.
	Model string

	// SystemPrompt is the persona's prompt body (the equivalent of an on-disk
	// system_prompt.md). Empty on definitions read back from the registry for
	// built-in personas, whose prompts are assembled internally.
	SystemPrompt string

	// LongRunning marks a persona expected to stay live for a very long time
	// (e.g. a swarm member running for weeks). The prompt builder then omits
	// fragments that would drift across rebuilds and bust the prompt-cache
	// prefix — currently the "- Today:" date in the environment section. Leave
	// false for ordinary personas; the swarm subsystem sets it for its members.
	LongRunning bool
}

// IsMain reports whether the persona appears in the /profile picker.
func (d AgentDefinition) IsMain() bool { return hasAs(d.As, "main") }

// IsSubagent reports whether the persona is invokable via the Agent tool.
func (d AgentDefinition) IsSubagent() bool { return hasAs(d.As, "subagent") }

func hasAs(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func (d AgentDefinition) toSpec() agent_impl.AgentSpec {
	return agent_impl.AgentSpec{
		Name:            d.Name,
		WhenToUse:       d.WhenToUse,
		As:              d.As,
		InjectMemory:    d.InjectMemory,
		AdvertiseSkills: d.AdvertiseSkills,
		ActiveTools:     d.ActiveTools,
		DeferredTools:   d.DeferredTools,
		Model:           d.Model,
		SystemPrompt:    d.SystemPrompt,
		LongRunning:     d.LongRunning,
	}
}

func definitionFromSpec(s agent_impl.AgentSpec) AgentDefinition {
	return AgentDefinition{
		Name:            s.Name,
		WhenToUse:       s.WhenToUse,
		As:              s.As,
		InjectMemory:    s.InjectMemory,
		AdvertiseSkills: s.AdvertiseSkills,
		ActiveTools:     s.ActiveTools,
		DeferredTools:   s.DeferredTools,
		Model:           s.Model,
		SystemPrompt:    s.SystemPrompt,
		LongRunning:     s.LongRunning,
	}
}
