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
