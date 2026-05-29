// Package ui defines the contract between evva's core agent and any UI
// implementation that drives it. The agent layer never imports a concrete
// UI; swapping a bubbletea TUI for a web frontend or a JSON-over-stdout
// bridge means changing one line in cmd/evva/main.go.
//
// Wiring sequence (host responsibility):
//
//  1. ui := bubbletea.New()                            // construct UI first
//  2. ag := agent.New(profile, agent.WithSink(ui), ...) // agent emits to UI
//  3. ui.Attach(ag)                                     // UI gets controller
//  4. ui.Run(ctx)                                       // blocks until exit
//
// The UI receives events as an event.Sink (agent → UI) and drives the
// agent through a Controller (UI → agent). Both interfaces are
// deliberately narrow; UIs that want richer access can type-assert the
// Controller back to *agent.Agent at their own risk.
package ui

import (
	"context"
	"log/slog"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/todo"
)

// UI is the contract a TUI / GUI / web frontend implementation satisfies.
//
// Emit is called from the agent loop's goroutine (per Sink's contract,
// the agent serializes per-agent emits internally). The UI must hand the
// event off to its own render loop without blocking — bubbletea
// implementations typically forward via tea.Program.Send().
//
// Run blocks the calling goroutine until the UI exits (user quit, ctx
// cancelled, fatal error). It is the host's main blocking call.
type UI interface {
	event.Sink

	// Attach hands the UI the controller it uses to drive the agent.
	// Called by the host once, between agent construction and Run.
	Attach(Controller)

	// Run starts the UI's input/render loop and blocks until exit.
	Run(ctx context.Context) error
}

// Skill is the UI-facing view of a user-installed skill — just the name
// and a one-line description for the slash-command suggestion panel. The
// ui package deliberately does not expose Path or Source: the UI never
// needs to read the SKILL.md file itself, the agent (via the SKILL tool)
// does that.
type Skill struct {
	Name        string
	Description string
}

// MCPServerInfo is the UI-facing view of one configured MCP server — the
// flattened snapshot the /mcp panel renders. Like Skill / ProfileChoice it
// deliberately exposes only plain types so the ui package never imports
// pkg/mcp; the agent layer maps mcp.ServerState into this shape.
type MCPServerInfo struct {
	Name          string // server name (the mcpServers key)
	Transport     string // "stdio" | "http"
	Status        string // "connected" | "pending" | "failed" | "needs-auth" | "disabled"
	Scope         string // "user" | "project" — which settings.json it came from
	Detail        string // one-line summary: the launch command (stdio) or url (http)
	ToolCount     int    // tools discovered (0 unless connected)
	ResourceCount int    // 1 when the server advertises resources, else 0
	Error         string // connection error, populated for failed / needs-auth
}

// Controller is the narrow API a UI uses to send commands back to the
// agent. Implemented by *agent.Agent.
//
// The interface is intentionally minimal. Render state lives behind
// public typed accessors — Messages / Usage for the transcript and
// status bar, TodoStore / DaemonState for the panels — so a UI in any
// module can read it without importing evva internals.
type Controller interface {
	// Run drives the agent for a single user turn. The UI typically
	// launches this in a goroutine so its main loop stays responsive,
	// and ctx-cancels to honor user interrupts.
	Run(ctx context.Context, prompt string) (string, error)

	// Continue resumes an iter-limit-paused run without appending a new
	// user message.
	Continue(ctx context.Context) (string, error)

	// Messages returns the live conversation transcript. The UI replays
	// these to rebuild its visible scrollback on resume.
	Messages() []llm.Message

	// Usage returns the cumulative token usage for the session — the
	// status bar's running total.
	Usage() llm.Usage

	// LastTurnInputTokens returns the input-token count of the most recent
	// turn: the "how full is the prompt right now" gauge the context meter
	// reads. Prefer this over Usage().Total for context-pressure display.
	LastTurnInputTokens() int

	// TodoStore exposes the agent's todo backing store so the UI can
	// render the todo panel (List) and clear it on auto-fold (Clear).
	// Never nil.
	TodoStore() *todo.TodoStore

	// DaemonState exposes the unified daemon store (subagents, background
	// bash tasks, monitors) so the UI can render the chip strips via
	// SnapshotByKind. Returns nil until the first daemon is registered —
	// callers must nil-check.
	DaemonState() *daemon.DaemonState

	// EnqueueUserPrompt hands the agent a prompt the user typed mid-run.
	// The agent drains the queue at the next iteration boundary instead of
	// starting a second concurrent Run.
	EnqueueUserPrompt(prompt string)

	// Logger exposes the agent's structured logger so the UI can emit
	// records that share its context.
	Logger() *slog.Logger

	// Model returns the model id the agent is currently bound to.
	// Used by the TUI's status header; falls back to "-" when empty.
	Model() string

	// AgentID returns the controller's agent identifier so the UI can
	// surface it in headers / banners. Cheap accessor; safe to call
	// every render.
	AgentID() string

	// MaxIterations / SetMaxIterations exposes the loop cap so the
	// /config form can mutate it mid-session. Reads are cheap (atomic
	// load); writes take effect at the next iteration boundary.
	MaxIterations() int
	SetMaxIterations(int)

	// SwitchLLM rebuilds the agent's llm.Client with a new
	// (provider, model) pair and clears the conversation history.
	// Caller (the TUI's /model form) must ensure no Run is in flight
	// before calling — see Agent.SwitchLLM for the running guard.
	SwitchLLM(provider constant.LLMProvider, model constant.Model) error

	// SwitchProfile reconstructs the agent under a new persona — fresh
	// system prompt, fresh active/deferred tool list, fresh session.
	// Caller (the /profile picker) is responsible for ensuring no Run
	// is in flight. Persists the new persona name to evva-config.yml.
	SwitchProfile(name string) error

	// ProfileName returns the active persona's wire identity ("evva",
	// "nono", ...). Used by the TUI status bar's dynamic agent label.
	ProfileName() string

	// ListMainProfiles enumerates the personas the /profile picker
	// should surface — every agent definition with `as: ["main", ...]`
	// in the agent registry.
	ListMainProfiles() []ProfileChoice

	// Effort returns the current effort level name ("low"|"medium"|"high"|"ultra").
	Effort() string

	// SetEffort updates the effort level at runtime. Validates the name;
	// returns an error for unknown levels. Persists to config and applies
	// to the LLM client on the next completion.
	SetEffort(level string) error

	// LLMTemperature / LLMTopK / LLMTopP return the current session-level
	// sampling parameters. nil means "provider default".
	LLMTemperature() *float64
	LLMTopK() *int
	LLMTopP() *float64

	// SetLLMTemperature / SetLLMTopK / SetLLMTopP update the session-only
	// sampling parameters. nil unsets (reverts to provider default).
	// Applies to the live LLM client immediately. Never persisted to disk.
	SetLLMTemperature(v *float64) error
	SetLLMTopK(v *int) error
	SetLLMTopP(v *float64) error

	// Skills returns the merged catalog of user-installed skills (home
	// and workdir, with workdir overrides applied). The TUI's slash
	// suggestion panel surfaces each entry as `/<name>` with the
	// description; the agent decides if/when to invoke them via the
	// SKILL tool. Returns nil when no skills are installed.
	Skills() []Skill

	// MCPServers returns a snapshot of every configured MCP server and its
	// live connection status, for the read-only /mcp panel. Includes
	// disabled servers. Returns nil when no MCP servers are configured (or
	// no manager is installed) — callers render an empty state.
	MCPServers() []MCPServerInfo

	// Compact forces an immediate compaction of the current session.
	// kind is "micro" (elide older tool results, no LLM call) or
	// "full" (one LLM call producing a summary brief). Returns
	// ErrRunInProgress when a Run is currently driving the loop —
	// the TUI surfaces that as a hint rather than retrying.
	Compact(ctx context.Context, kind string) error

	// PermissionModeName returns the agent's current permission stance
	// as a string ("default", "accept_edits", "plan", "bypass", "auto").
	// Named verbosely to avoid collision with the typed Agent.PermissionMode()
	// accessor that returns the internal Mode enum.
	PermissionModeName() string

	// CyclePermissionMode advances the mode in Shift+Tab order and
	// returns the new mode name. Wraps around at the end of the cycle.
	CyclePermissionMode() string

	// RespondPermission delivers the user's approval/denial back to
	// the blocked tool goroutine. id is the RequestID from the
	// KindApprovalNeeded event payload. Returns an error only when
	// the id is unknown (already responded / cancelled).
	RespondPermission(id string, decision PermissionDecision) error

	// RespondQuestion delivers the user's answers back to the blocked
	// AskUserQuestion tool goroutine. id is the RequestID from the
	// KindQuestionNeeded event payload.
	RespondQuestion(id string, resp QuestionResponse) error

	// ListSessions enumerates persisted sessions scoped to the agent's
	// current workdir, sorted by last-write time descending. The
	// /resume picker calls this to populate its rows. Warnings carries
	// any corrupt-file messages the store collected — the UI may
	// surface them as hints or ignore them.
	ListSessions() ([]SessionInfo, []string)

	// ResumeSession swaps the live agent's state with the session
	// identified by id. Returns ErrRunInProgress (via the agent layer)
	// when a Run is in flight, or an error when the file is missing or
	// unreadable. Successful resume invalidates the prior transcript;
	// the TUI re-renders from Session().Messages.
	ResumeSession(id string) error
}

// SessionInfo is one row in the /resume picker. Lightweight by design —
// only what the picker renders, so the UI can show many entries without
// loading full message bodies.
type SessionInfo struct {
	ID              string // session-id (matches the JSON file basename)
	FirstUserPrompt string // up to 200 chars; picker truncates to 150 for display
	UpdatedAt       int64  // unix nano of last save (file mtime); resume picker sorts desc
	CreatedAt       int64  // unix nano of first save
	Profile         string // persona name at save time
	Provider        string // LLM provider name at save time
	Model           string // LLM model id at save time
	MessageCount    int    // length of Session.Messages — picker shows "<n> msgs"
}

// QuestionResponse is the UI-side payload returned through
// Controller.RespondQuestion. It mirrors question.Response but uses plain
// Go types so the ui package doesn't import internal/question.
type QuestionResponse struct {
	// Answers maps question text → answer string. For single-select the value
	// is the chosen option label; for multi-select it is comma-separated
	// labels; for "Other" it is the user-typed free text.
	Answers map[string]string
	// Annotations is keyed by question text. Present only when the user
	// selected an option that had a preview, or typed "Other" notes.
	Annotations map[string]QuestionAnnotation
}

// QuestionAnnotation captures the preview content (if any) of the option the
// user selected, plus any free-text notes they added.
type QuestionAnnotation struct {
	Notes   string
	Preview string
}

// PermissionDecision is the UI-side payload returned through
// Controller.RespondPermission. It mirrors permission.Decision but uses
// strings so the ui package doesn't depend on internal/permission.
type PermissionDecision struct {
	Behavior string // "allow" | "deny"
	Reason   string
	// AddRule is non-nil when the user picked "Allow for this session" —
	// the agent's gate adds it to the in-memory store before falling
	// through to tool.Execute.
	AddRule *PermissionRuleSeed
}

// PermissionRuleSeed is the minimum info the agent needs to construct a
// session-scope allow rule. Source is fixed to session by the agent.
type PermissionRuleSeed struct {
	ToolName string
	Content  string // empty means tool-wide
}

// ProfileChoice is one row in the /profile picker. Surfaces the persona
// name plus the optional when_to_use blurb the user authored in
// meta.yml so the picker can hint at what each persona is for.
type ProfileChoice struct {
	Name      string
	WhenToUse string
}
