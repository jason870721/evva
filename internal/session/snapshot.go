package session

import (
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/llm"
)

// SnapshotVersion is the on-disk schema version. Bump on breaking
// changes to the JSON layout; older files become unreadable, which the
// store's List path tolerates by skipping with a warning rather than
// aborting the picker.
const SnapshotVersion = 1

// PreviewMaxBytes caps the persisted first-user-prompt preview. The
// resume overlay renders only 150 chars, but we store 200 so trailing
// truncation never produces a half-word in the visible window.
const PreviewMaxBytes = 200

// Snapshot is the JSON shape of one persisted session.
//
// SessionID identifies the file on disk and equals the live agent's UUID
// at the moment the session was first saved; the agent overwrites its own
// ID with this value on resume so subsequent writes target the same file.
//
// Profile + Provider + Model capture the agent setup at save time so the
// resume code can rebuild the right persona and LLM client even if the
// user's defaults have changed since.
type Snapshot struct {
	Version          int          `json:"version"`
	SessionID        string       `json:"session_id"`
	Workdir          string       `json:"workdir"`
	WorkdirSlug      string       `json:"workdir_slug"`
	Profile          string       `json:"profile"`
	Provider         string       `json:"provider"`
	Model            string       `json:"model"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	FirstUserPrompt  string       `json:"first_user_prompt"`
	Session          SessionState `json:"session"`
}

// SessionState carries the live conversation fields persisted alongside
// the snapshot envelope. The unexported Session fields are surfaced via
// the SetCompactState / SetLastTurnInputTokens accessors on rehydrate.
type SessionState struct {
	Messages            []llm.Message `json:"messages"`
	Usage               llm.Usage     `json:"usage"`
	LastTurnInputTokens int           `json:"last_turn_input_tokens"`
	MicroCompacted      bool          `json:"micro_compacted"`
	FullCompactCount    int           `json:"full_compact_count"`
}

// ToSnapshot copies the live session into a JSON-friendly DTO. The
// caller fills in the envelope fields (SessionID, Workdir, Profile, etc.)
// — Session has no view of those.
func (s *Session) ToSnapshot() SessionState {
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return SessionState{
		Messages:            msgs,
		Usage:               s.Usage,
		LastTurnInputTokens: s.lastTurnInputTokens,
		MicroCompacted:      s.microCompacted,
		FullCompactCount:    s.fullCompactCount,
	}
}

// FromSnapshot returns a fresh *Session rehydrated from the persisted
// state. The slice is copied so callers can mutate the snapshot's
// Messages without aliasing into the live session (and vice versa).
func FromSnapshot(state SessionState) *Session {
	msgs := make([]llm.Message, len(state.Messages))
	copy(msgs, state.Messages)
	s := New()
	s.Messages = msgs
	s.SetUsage(state.Usage)
	s.SetLastTurnInputTokens(state.LastTurnInputTokens)
	s.SetCompactState(state.MicroCompacted, state.FullCompactCount)
	return s
}

// FirstUserPromptPreview returns up to PreviewMaxBytes from the first
// RoleUser message's content. Whitespace at both ends is trimmed; CR/LF
// inside the body are flattened to single spaces so the preview always
// fits on one line in the resume picker.
//
// Empty result means the session has no user messages yet (rare —
// typically only when the file is saved between session start and the
// first user prompt being routed).
func FirstUserPromptPreview(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		body := strings.TrimSpace(m.Content)
		if body == "" {
			continue
		}
		body = strings.ReplaceAll(body, "\r\n", " ")
		body = strings.ReplaceAll(body, "\n", " ")
		body = strings.ReplaceAll(body, "\r", " ")
		body = collapseSpaces(body)
		if len(body) > PreviewMaxBytes {
			body = body[:PreviewMaxBytes]
		}
		return body
	}
	return ""
}

func collapseSpaces(s string) string {
	// Tight loop: replace runs of whitespace with a single space.
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}
