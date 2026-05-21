package session

import (
	"github.com/johnny1110/evva/pkg/llm"
)

// Session holds the live conversation history for a single agent run.
// The agent appends every message (user, assistant, tool result) here so the
// LLM always receives the full context on the next turn.
// tools, agent, llm, tui will use it.
type Session struct {
	// LLM context payload
	Messages []llm.Message
	// Usage is the running sum of every turn's reported token usage in this
	// session. Compaction is expected to reset Messages but leave Usage as
	// the running tab of what the user has already paid for.
	Usage llm.Usage
	// lastTurnInputTokens is the InputTokens count from the most recent
	// agent turn — i.e. how big the prompt was the last time the LLM
	// processed this session. Compaction uses this (not Usage.Total)
	// to gauge prompt-size pressure: cumulative Usage keeps growing
	// across turns and stops being a reliable "how full is the prompt
	// right now" signal, especially after a full-compact replaces
	// Messages with a tiny brief.
	lastTurnInputTokens int
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

// AddUsage folds one usage entry into the cumulative session total only.
// Use this for non-turn usage events whose input-token count does NOT
// represent the current prompt size — e.g. the LLM call inside full
// compaction, where InputTokens reflects the size of the conversation
// we just summarized, not the size of the post-compaction prompt.
func (s *Session) AddUsage(u llm.Usage) {
	s.Usage = s.Usage.Add(u)
}

// RecordTurn marks u as the most recent agent-turn usage: it folds u
// into the cumulative total AND updates lastTurnInputTokens so
// compaction can measure live prompt pressure. The agent loop calls
// this after every Complete / Stream that drove a real iteration.
func (s *Session) RecordTurn(u llm.Usage) {
	s.AddUsage(u)
	s.lastTurnInputTokens = u.InputTokens
}

// LastTurnInputTokens returns the InputTokens from the most recent
// agent turn (zero before the first turn completes, or right after a
// full-compact reset). This is the canonical "how full is the prompt
// right now" signal — preferred over Usage.Total for ratio checks.
func (s *Session) LastTurnInputTokens() int {
	return s.lastTurnInputTokens
}

// SetLastTurnInputTokens overrides the cached turn-input figure. Used by
// the resume path to rehydrate a snapshot's previously-recorded value;
// production code should prefer RecordTurn so the cumulative Usage is
// kept in sync.
func (s *Session) SetLastTurnInputTokens(n int) {
	s.lastTurnInputTokens = n
}

// SetUsage overrides the cumulative usage total. Same caveat as
// SetLastTurnInputTokens: only the resume path should use it. Production
// turns flow through AddUsage / RecordTurn.
func (s *Session) SetUsage(u llm.Usage) {
	s.Usage = u
}

// SetCompactState rehydrates the micro/full compaction counters. Used by
// session.FromSnapshot to round-trip persisted state; not for live use.
func (s *Session) SetCompactState(micro bool, fullCount int) {
	s.microCompacted = micro
	s.fullCompactCount = fullCount
}

func (s *Session) IsMicroCompacted() bool {
	return s.microCompacted
}

func (s *Session) MicroCompact(messages []llm.Message) {
	s.microCompacted = true
	s.Messages = messages
}

// FullCompact replaces Messages with the summarization brief and
// resets the in-flight compaction state. lastTurnInputTokens is set to
// briefTokens — the brief is now the entirety of the prompt the next
// turn will send, so callers (the TUI's context bar in particular) can
// read accurate "current prompt size" without waiting for the next
// thinking call to land.
//
// Cumulative Usage is also reset: in=briefTokens, out=0. The HUD reads
// as "fresh context after compact" so the user can visually confirm
// the bar drop (e.g. 80% → 40%) without the cumulative tail dragging
// the numbers up. The compaction caller is responsible for logging
// the pre-reset totals before invoking this — they're gone after.
func (s *Session) FullCompact(messages []llm.Message, briefTokens int) {
	s.microCompacted = false
	s.fullCompactCount++
	s.Messages = messages
	s.lastTurnInputTokens = briefTokens
	s.Usage = llm.Usage{InputTokens: briefTokens}
}

func (s *Session) GetFullCompactCount() int {
	return s.fullCompactCount
}
