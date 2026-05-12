package agent

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
