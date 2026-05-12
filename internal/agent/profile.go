package agent

import "github.com/johnny1110/evva/internal/tools"

// Profile is the configuration an Agent runs under: which kind of agent it
// is, what system prompt it presents, and which tool *names* are exposed to
// the model. Two agents with the same Profile behave identically — the loop,
// dispatch, and lifecycle are shared in the Agent type; only configuration
// varies.
//
// Resolution from name → instance happens once at agent init, via tools.Build.
// Stateful tool groups (e.g. the six task tools sharing one *Store) get fresh
// state per Build call, so two agents constructed from the same Profile end
// up with isolated state. See internal/agent/profiles for preset builders.
type Profile struct {
	Type         AgentType
	SystemPrompt string
	Tools        []tools.ToolName
}
