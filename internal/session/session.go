package session

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/johnny1110/evva/pkg/llm"
)

// Session holds the live conversation history for a single agent run.
// The agent appends every message (user, assistant, tool result) here so the
// LLM always receives the full context on the next turn.
// tools, agent, llm, tui will use it.
type Session struct {
	// LLM context payload
	Messages []llm.Message
	// msgMu guards ASSIGNMENTS to Messages, not reads of it through the
	// field or GetMessages — those stay agent-goroutine-owned and
	// unlocked, as they always were, because the agent loop passes the
	// live slice straight to the provider on every turn.
	//
	// The lock exists for CopyMessages: the /context overlay builds its
	// ledger from the UI goroutine while the loop may be appending, and
	// a torn slice header there would panic the TUI rather than merely
	// report a stale byte count.
	msgMu sync.RWMutex
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
	//
	// Atomic because it is the one session field read LIVE across
	// goroutines: the agent loop writes it mid-run (RecordTurn) while the
	// context meters — the swarm web roster (Service.Roster) and the TUI
	// status bar — read it from their own goroutines. Everything else on
	// the session stays owned by the agent loop.
	lastTurnInputTokens atomic.Int64
	// spanCompacted records that the middle rung of the context ladder
	// (span compaction — summarize the oldest span in place) has run on
	// this session, so the next escalation goes full.
	//
	// It persists as "micro_compacted" for snapshot compatibility: the
	// field predates the ladder, when the middle rung was the
	// placeholder-eliding "micro compact" that CTX-2 replaced with
	// pruning. Renaming the JSON key would strand every session file
	// written before v1.17 for no behavioral gain.
	spanCompacted bool
	// fullCompact: compress all session message (level-2 compact)
	fullCompactCount int

	// pins is the set of tool-result IDs the operator has exempted from
	// every rung of the context ladder. Keyed by ID rather than message
	// index because compaction and /rewind replace Messages wholesale —
	// an index-keyed pin would silently start protecting the wrong block.
	//
	// Guarded because this is the second field read across goroutines:
	// the TUI's /context overlay toggles pins from the UI goroutine while
	// the agent loop plans against them mid-run.
	pinsMu sync.RWMutex
	pins   map[string]struct{}
}

func New() *Session {
	return &Session{}
}

// Pin exempts a tool result from pruning, span compaction, and full
// compaction. Unknown IDs are accepted: a pin set before the result
// arrives is still a valid intent, and BuildLedger simply won't find a
// block to mark until it does.
func (s *Session) Pin(toolID string) {
	if toolID == "" {
		return
	}
	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()
	if s.pins == nil {
		s.pins = make(map[string]struct{})
	}
	s.pins[toolID] = struct{}{}
}

// Unpin removes a pin. No-op when absent.
func (s *Session) Unpin(toolID string) {
	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()
	delete(s.pins, toolID)
}

// TogglePin flips the pin on toolID and reports the resulting state.
func (s *Session) TogglePin(toolID string) bool {
	if toolID == "" {
		return false
	}
	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()
	if s.pins == nil {
		s.pins = make(map[string]struct{})
	}
	if _, ok := s.pins[toolID]; ok {
		delete(s.pins, toolID)
		return false
	}
	s.pins[toolID] = struct{}{}
	return true
}

// IsPinned reports whether toolID is pinned.
func (s *Session) IsPinned(toolID string) bool {
	s.pinsMu.RLock()
	defer s.pinsMu.RUnlock()
	_, ok := s.pins[toolID]
	return ok
}

// PinSet returns a copy of the pin set for ledger construction. A copy
// because BuildLedger runs on the agent goroutine while the UI may be
// toggling.
func (s *Session) PinSet() map[string]struct{} {
	s.pinsMu.RLock()
	defer s.pinsMu.RUnlock()
	out := make(map[string]struct{}, len(s.pins))
	for id := range s.pins {
		out[id] = struct{}{}
	}
	return out
}

// Pins returns the pinned IDs, for snapshot persistence.
func (s *Session) Pins() []string {
	s.pinsMu.RLock()
	defer s.pinsMu.RUnlock()
	out := make([]string, 0, len(s.pins))
	for id := range s.pins {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SetPins replaces the pin set. Used by the resume path to rehydrate a
// snapshot.
func (s *Session) SetPins(ids []string) {
	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()
	s.pins = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			s.pins[id] = struct{}{}
		}
	}
}

// Ledger builds the block ledger for the current history. systemPrompt
// is supplied by the caller because the prompt lives on the LLM client,
// not in Messages — see CategorySystem.
func (s *Session) Ledger(systemPrompt string) Ledger {
	return BuildLedger(s.CopyMessages(), systemPrompt, s.PinSet())
}

func (s *Session) Append(msg llm.Message) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.Messages = append(s.Messages, msg)
}

func (s *Session) GetMessages() []llm.Message {
	return s.Messages
}

// CopyMessages returns a shallow copy of the history taken under the
// write lock. Callers off the agent goroutine — the /context overlay —
// must use this rather than GetMessages.
func (s *Session) CopyMessages() []llm.Message {
	s.msgMu.RLock()
	defer s.msgMu.RUnlock()
	out := make([]llm.Message, len(s.Messages))
	copy(out, s.Messages)
	return out
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
//
// The prompt-size figure sums InputTokens with the cache read/creation
// counts: Anthropic's input_tokens covers ONLY the uncached suffix of the
// prompt (the three usage fields are disjoint), so with prompt caching
// active InputTokens alone collapses to a few hundred tokens per call and
// the auto-compact threshold would never trigger. Providers without cache
// reporting leave the cache fields zero and the sum degrades to the old
// behavior.
func (s *Session) RecordTurn(u llm.Usage) {
	s.AddUsage(u)
	s.lastTurnInputTokens.Store(int64(u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens))
}

// LastTurnInputTokens returns the InputTokens from the most recent
// agent turn (zero before the first turn completes, or right after a
// full-compact reset). This is the canonical "how full is the prompt
// right now" signal — preferred over Usage.Total for ratio checks.
func (s *Session) LastTurnInputTokens() int {
	return int(s.lastTurnInputTokens.Load())
}

// SetLastTurnInputTokens overrides the cached turn-input figure. Used by
// the resume path to rehydrate a snapshot's previously-recorded value;
// production code should prefer RecordTurn so the cumulative Usage is
// kept in sync.
func (s *Session) SetLastTurnInputTokens(n int) {
	s.lastTurnInputTokens.Store(int64(n))
}

// SetUsage overrides the cumulative usage total. Same caveat as
// SetLastTurnInputTokens: only the resume path should use it. Production
// turns flow through AddUsage / RecordTurn.
func (s *Session) SetUsage(u llm.Usage) {
	s.Usage = u
}

// SetCompactState rehydrates the span/full compaction counters. Used by
// session.FromSnapshot to round-trip persisted state; not for live use.
func (s *Session) SetCompactState(span bool, fullCount int) {
	s.spanCompacted = span
	s.fullCompactCount = fullCount
}

// IsSpanCompacted reports whether the ladder's middle rung has already
// run. The prune rung deliberately does NOT set it: pruning is free and
// re-runnable, so gating it behind a one-shot flag would throw away the
// cheapest tier after a single use.
func (s *Session) IsSpanCompacted() bool {
	return s.spanCompacted
}

// SpanCompact installs the post-span-compaction history and marks the
// middle rung spent.
func (s *Session) SpanCompact(messages []llm.Message) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.spanCompacted = true
	s.Messages = messages
}

// ReplaceMessages swaps the history without touching any ladder state.
// This is the prune rung's setter — pruning rewrites content in place
// and must stay repeatable as the session grows new material to prune.
func (s *Session) ReplaceMessages(messages []llm.Message) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
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
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.spanCompacted = false
	s.fullCompactCount++
	s.Messages = messages
	s.lastTurnInputTokens.Store(int64(briefTokens))
	s.Usage = llm.Usage{InputTokens: briefTokens}
}

func (s *Session) GetFullCompactCount() int {
	return s.fullCompactCount
}
