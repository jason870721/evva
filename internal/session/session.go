package session

import "github.com/johnny1110/evva/internal/llm"

// Session holds the live conversation history for a single agent run.
// The agent appends every message (user, assistant, tool result) here so the
// LLM always receives the full context on the next turn.
// tools, agent, llm, tui will use it.
type Session struct {
	Messages []llm.Message
}

func New() *Session {
	return &Session{}
}

func (s *Session) Append(msg llm.Message) {
	s.Messages = append(s.Messages, msg)
}
