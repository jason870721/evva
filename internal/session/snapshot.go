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

// Meta is the snapshot envelope: everything about a session except the
// conversation itself.
//
// Split out from Snapshot (v1.19) so listing can decode the envelope
// WITHOUT materializing message bodies — see store.go's Header. Embedded
// anonymously below, so encoding/json flattens it and the on-disk layout is
// byte-identical to pre-v1.19 files.
//
// SessionID identifies the file on disk and equals the live agent's UUID
// at the moment the session was first saved; the agent overwrites its own
// ID with this value on resume so subsequent writes target the same file.
//
// Profile + Provider + Model capture the agent setup at save time so the
// resume code can rebuild the right persona and LLM client even if the
// user's defaults have changed since.
type Meta struct {
	Version         int       `json:"version"`
	SessionID       string    `json:"session_id"`
	Workdir         string    `json:"workdir"`
	WorkdirSlug     string    `json:"workdir_slug"`
	Profile         string    `json:"profile"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	FirstUserPrompt string    `json:"first_user_prompt"`

	// Title is the operator's own name for this session (/title). Empty
	// means the picker falls back to FirstUserPrompt, which is what every
	// session showed before v1.19 — so an absent title is not a missing
	// title, it is the default one.
	Title string `json:"title,omitempty"`

	// ParentID names the session this one was forked from; empty for a
	// root session. The picker indents children under their parent.
	ParentID string `json:"parent_id,omitempty"`

	// ForkedAtLen is len(Messages) at the moment of the fork — how much
	// history the child inherited. Display only; the child owns its copy.
	ForkedAtLen int `json:"forked_at_len,omitempty"`

	// Pinned exempts this session from `evva sessions prune`. Retention
	// caps ignore pinned sessions entirely rather than counting them.
	Pinned bool `json:"pinned,omitempty"`
}

// Label is what a picker row should call this session: the operator's
// title when they set one, else the first-prompt preview, else a plain
// marker so a row is never blank.
func (m Meta) Label() string {
	if m.Title != "" {
		return m.Title
	}
	if m.FirstUserPrompt != "" {
		return m.FirstUserPrompt
	}
	return "(no user prompt yet)"
}

// Snapshot is the JSON shape of one persisted session: the envelope plus
// the conversation.
type Snapshot struct {
	Meta
	Session SessionState `json:"session"`
}

// ForkMeta derives a child session's envelope from its parent's.
//
// The child is a new session in every way that matters — its own id, its
// own creation time, its own checkpoint namespace (which is what makes the
// PRD's "a fork's rewind cannot cross the fork point" true for free) — and
// inherits only the parent's setup: workdir, persona, provider, model, and
// the first-prompt preview that names the branch it came from.
//
// atLen records how much history was copied. It is display metadata; the
// caller does the copying.
func ForkMeta(parent Meta, childID string, atLen int, now time.Time) Meta {
	child := parent
	child.SessionID = childID
	child.ParentID = parent.SessionID
	child.ForkedAtLen = atLen
	child.CreatedAt = now
	child.UpdatedAt = now
	// A fork is an experiment until the operator says otherwise: neither
	// the parent's pin nor its title carries over, or every branch would
	// inherit protection from retention and they would all read alike in
	// the picker.
	child.Pinned = false
	child.Title = ""
	return child
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
	// Pins are tool-result IDs the operator exempted from the context
	// ladder. Added in v1.17; absent in older snapshots, which decode to
	// nil and rehydrate as "nothing pinned" — the pre-pin behavior. That
	// is why this could be added without bumping SnapshotVersion.
	Pins []string `json:"pins,omitempty"`
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
		LastTurnInputTokens: int(s.lastTurnInputTokens.Load()),
		MicroCompacted:      s.spanCompacted,
		FullCompactCount:    s.fullCompactCount,
		Pins:                s.Pins(),
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
	s.SetPins(state.Pins)
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
			// The cap is a byte budget, but the cut must land on a rune
			// boundary — a truncated multi-byte character is stored in the
			// snapshot forever and renders as a replacement glyph in every
			// picker that shows it. ToValidUTF8 drops the partial tail.
			body = strings.ToValidUTF8(body[:PreviewMaxBytes], "")
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
