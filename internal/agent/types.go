package agent

import (
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/tools"
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
// the model. Two agents with the same Profile behave identically — the loop,
// dispatch, and lifecycle are shared in the Agent type; only configuration
// varies.
//
// Resolution from name → instance happens once at agent init, via tools.Build.
// Stateful tool groups (e.g. the six task tools sharing one *Store) get fresh
// state per Build call, so two agents constructed from the same Profile end
// up with isolated state. See internal/agent/profiles for preset builders.
type Profile struct {
	// about agent
	Type         AgentType
	SystemPrompt string
	Tools        []tools.ToolName

	// about llm core
	LLMProvider constant.LLMProvider
	LLMModel    constant.Model
	LLMOptions  []llm.Option
}
