package agent

import (
	"context"
	"log/slog"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/ui"
)

// Skill is the UI-facing view of a user-installed skill.
type Skill struct {
	Name        string
	Description string
}

// SessionInfo is a snapshot of the agent's conversation state (message count
// and cumulative token usage). External callers get a point-in-time copy.
type SessionInfo struct {
	// MessageCount is the number of messages on the session transcript —
	// counts user prompts, assistant turns, and tool results.
	MessageCount int
	// InputTokens is the cumulative input-token total across every LLM
	// call in this session.
	InputTokens int
	// OutputTokens is the cumulative output-token total across every
	// LLM call in this session.
	OutputTokens int
	// LastInputTokens is the input-token count of the most recent LLM
	// call. Useful for surfacing per-turn cost in a status bar.
	LastInputTokens int
}

// ResumableSession is one row in /resume — a persisted session the host
// can present to the user and rehydrate via Agent.ResumeSession.
type ResumableSession struct {
	ID              string
	FirstUserPrompt string
	UpdatedAt       int64 // unix nano of last save
	CreatedAt       int64 // unix nano of first save
	Profile         string
	Provider        string
	Model           string
	MessageCount    int
}

// Agent is the public API for creating and driving an evva agent
// programmatically. It is implemented by a wrapper around the internal agent.
type Agent interface {
	// Run drives the agent for a single user turn.
	Run(ctx context.Context, prompt string) (string, error)

	// Continue resumes an iter-limit-paused run without appending a new
	// user message.
	Continue(ctx context.Context) (string, error)

	// Session returns a snapshot of the conversation state.
	Session() SessionInfo

	// Logger exposes the agent's structured logger.
	Logger() *slog.Logger

	// Model returns the model id the agent is currently bound to.
	Model() string

	// AgentID returns the agent's unique identifier.
	AgentID() string

	// MaxIterations / SetMaxIterations exposes the loop cap.
	MaxIterations() int
	SetMaxIterations(int)

	// SwitchLLM rebuilds the agent's LLM client with a new (provider, model)
	// pair and clears the conversation history.
	SwitchLLM(provider constant.LLMProvider, model constant.Model) error

	// SwitchProfile reconstructs the agent under a new persona.
	SwitchProfile(name string) error

	// ProfileName returns the active persona's wire identity.
	ProfileName() string

	// ListMainProfiles enumerates the personas available for switching.
	ListMainProfiles() []ProfileChoice

	// Effort returns the current effort level name ("low"|"medium"|"high"|"ultra").
	Effort() string

	// SetEffort updates the effort level at runtime.
	SetEffort(level string) error

	// Skills returns the merged catalog of user-installed skills.
	Skills() []Skill

	// Compact forces an immediate compaction of the current session.
	Compact(ctx context.Context, kind string) error

	// PermissionModeName returns the agent's current permission stance
	// as a string ("default", "accept_edits", "plan", "bypass", "auto").
	PermissionModeName() string

	// CyclePermissionMode advances the mode in Shift+Tab order and
	// returns the new mode name.
	CyclePermissionMode() string

	// RespondPermission delivers the user's approval/denial back to
	// the blocked tool goroutine.
	RespondPermission(id string, decision PermissionDecision) error

	// RespondQuestion delivers the user's answers back to the blocked
	// AskUserQuestion tool goroutine.
	RespondQuestion(id string, resp QuestionResponse) error

	// ListSessions enumerates persisted sessions for the agent's workdir,
	// sorted by mtime descending. The second return is a slice of
	// non-fatal warnings collected while scanning (corrupt files, etc.).
	ListSessions() ([]ResumableSession, []string)

	// ResumeSession reloads a session by id, swapping the live agent's
	// transcript + profile + LLM with the persisted state. Returns an
	// error when the file is missing, unreadable, or a Run is currently
	// in flight.
	ResumeSession(id string) error

	// Controller returns the agent as a ui.Controller — the narrow seam a
	// UI implementation (e.g. pkg/ui/bubbletea) drives the agent through.
	// The public Agent interface and ui.Controller share method names with
	// different payload types (DTO vs ui.* views), so they cannot live on
	// one concrete type; this accessor hands a host the ui-typed view to
	// pass to UI.Attach.
	Controller() ui.Controller

	// Shutdown cancels the agent's root context, tearing down the signal
	// pump and every background worker (bash tasks, monitors, subagents).
	// Safe to call once at host exit (typically via defer).
	Shutdown()
}

// SkillReloader is the optional runtime skill-reload seam. The base Agent interface
// stays unchanged (Stable — no new method); a host that needs to hot-swap a live
// agent's skill catalog — the swarm web UI adding/removing a member's skills
// (RP-10) — type-asserts an Agent to SkillReloader. ReloadSkills installs the new
// catalog on the skill tool AND re-renders the prompt's # Skills section; an empty
// registry advertises "no skills". Call at a run boundary (no Run in flight); the
// re-render costs a one-turn KV-cache miss.
type SkillReloader interface {
	ReloadSkills(reg *skill.Registry) error
}

// QuestionResponse is the payload returned through Agent.RespondQuestion.
type QuestionResponse struct {
	// Answers maps question text → answer string (single label; comma-joined for
	// multi-select). Kept for back-compat; prefer MultiAnswers for multi-select.
	Answers map[string]string
	// MultiAnswers maps question text → the chosen option labels (native
	// multi-select form). Additive in v2.x — authoritative when set.
	MultiAnswers map[string][]string
	Annotations  map[string]QuestionAnnotation
}

// QuestionAnnotation captures the preview content (if any) of the option the
// user selected, plus any free-text notes they added.
type QuestionAnnotation struct {
	Notes   string
	Preview string
}

// PermissionDecision is the payload returned through Agent.RespondPermission.
type PermissionDecision struct {
	Behavior string // "allow" | "deny"
	Reason   string
	AddRule  *PermissionRuleSeed
}

// PermissionRuleSeed is the minimum info needed to construct a
// session-scope allow rule.
type PermissionRuleSeed struct {
	ToolName string
	Content  string // empty means tool-wide
}

// ProfileChoice is one row in the /profile picker.
type ProfileChoice struct {
	Name      string
	WhenToUse string
}
