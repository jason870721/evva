package session

import "github.com/johnny1110/evva/internal/llm"

// Session holds the live conversation history for a single agent run.
// The agent appends every message (user, assistant, tool result) here so the
// LLM always receives the full context on the next turn.
// tools, agent, llm, tui will use it.
type Session struct {
	// LLM context payload
	Messages []llm.Message
	// microCompacted: compress tool_use result block only (level-1 compact)
	microCompacted bool
	// fullCompact: compress all session message (level-2 compact)
	fullCompactCount int
}

func New() *Session {
	return &Session{}
}

func (s *Session) Append(msg llm.Message) {
	s.Messages = append(s.Messages, msg)
}

func (s *Session) GetMessages() []llm.Message {
	return s.Messages
}

func (s *Session) IsMicroCompacted() bool {
	return s.microCompacted
}

func (s *Session) MicroCompact(messages []llm.Message) {
	s.microCompacted = true
	s.Messages = messages
}

func (s *Session) FullCompact(messages []llm.Message) {
	s.microCompacted = false
	s.fullCompactCount++
	s.Messages = messages
}

func (s *Session) GetFullCompactCount() int {
	return s.fullCompactCount
}
