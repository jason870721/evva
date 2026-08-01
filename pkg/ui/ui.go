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
	"github.com/johnny1110/evva/pkg/tools/workflow"
)

// UI is the contract a TUI / GUI / web frontend implementation satisfies.
//
// Emit is called from the agent loop's goroutine (per Sink's contract,
// the agent serializes per-agent emits internally). The UI must hand the
// event off to its own render loop WITHOUT BLOCKING — route it through an
// EmitQueue rather than calling tea.Program.Send inline: Send blocks
// while the loop is busy, and emitters may hold locks the render path
// reads back (see EmitQueue's doc for the TUI-freezing deadlock that
// inline Sends caused).
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

	// WorkflowTasks exposes the dynamic-workflow board so the UI can
	// render the board panel. Returns nil when the feature is off
	// (enable_dynamic_workflow) — callers must nil-check.
	WorkflowTasks() *workflow.Store

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

	// ListOutputStyles enumerates the styles the /output-style picker
	// should surface: built-ins plus disk styles (user + project tiers),
	// default first.
	ListOutputStyles() []OutputStyleChoice

	// OutputStyleName returns the style the active profile resolves —
	// the persona-declared pin when the persona carries one, else the
	// configured output_style ("default" when unset).
	OutputStyleName() string

	// SwitchOutputStyle persists the style choice and rebuilds the
	// active persona's profile so the new voice applies immediately.
	// Same contract as SwitchProfile: caller ensures no Run is in
	// flight, and the conversation resets (the system prompt changed).
	SwitchOutputStyle(name string) error

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

	// SetPermissionModeName sets the permission stance directly by wire
	// name ("default", "accept_edits", "plan", "bypass") — the random-access
	// complement of CyclePermissionMode for callers that present modes as a
	// picker (the swarm web's per-member switch). Errors on unknown names.
	// Safe to call mid-run: the gate reads the mode per tool call.
	SetPermissionModeName(name string) error

	// RespondPermission delivers the user's approval/denial back to
	// the blocked tool goroutine. id is the RequestID from the
	// KindApprovalNeeded event payload. Returns an error only when
	// the id is unknown (already responded / cancelled).
	RespondPermission(id string, decision PermissionDecision) error

	// RespondQuestion delivers the user's answers back to the blocked
	// AskUserQuestion tool goroutine. id is the RequestID from the
	// KindQuestionNeeded event payload.
	RespondQuestion(id string, resp QuestionResponse) error

	// ClearSession starts a fresh conversation under the same persona,
	// LLM, and tools: empty history, zeroed usage, cleared todos, and a
	// new session id. The old session's snapshot stays on disk and
	// remains loadable via ResumeSession. Returns ErrRunInProgress (via
	// the agent layer) when a Run is in flight — the TUI's /clear
	// surfaces that as a hint rather than retrying.
	ClearSession() error

	// ListSessions enumerates persisted sessions scoped to the agent's
	// current workdir, sorted by last-write time descending. The
	// /resume picker calls this to populate its rows. Warnings carries
	// any corrupt-file messages the store collected — the UI may
	// surface them as hints or ignore them.
	ListSessions() ([]SessionInfo, []string)

	// ListAllSessions enumerates persisted sessions across every workdir on
	// the machine, newest first — the picker's all-workdirs toggle. Same
	// contract as ListSessions otherwise.
	ListAllSessions() ([]SessionInfo, []string)

	// ResumeSession swaps the live agent's state with the session
	// identified by id. Returns ErrRunInProgress (via the agent layer)
	// when a Run is in flight, or an error when the file is missing or
	// unreadable. Successful resume invalidates the prior transcript;
	// the TUI re-renders from Session().Messages.
	//
	// Resolves across every workdir, not just the agent's own, so a row
	// picked from the all-workdirs view resumes rather than reporting a
	// missing file. The resumed session keeps writing to its original
	// slug — resuming a session does not move it.
	ResumeSession(id string) error

	// ForkSession branches the LIVE session: the current history is copied
	// into a new session id whose ParentID is the current one, the agent
	// continues in the child, and the parent's snapshot stays on disk
	// exactly as it was at the fork point.
	//
	// The child gets a fresh checkpoint namespace, which is what makes a
	// fork's /rewind unable to reach past the fork point — that invariant
	// is structural, not enforced. Returns the child's id.
	ForkSession() (string, error)

	// SetSessionTitle names a persisted session. An empty title clears it,
	// restoring the first-user-prompt preview as the picker's label.
	// Naming the LIVE session also updates the in-memory envelope so the
	// next auto-save does not overwrite it.
	SetSessionTitle(id, title string) error

	// PinSession marks a persisted session exempt from `evva sessions
	// prune`. Returns the resulting state so a toggling caller does not
	// have to re-list.
	PinSession(id string, pinned bool) error

	// DeleteSession removes a persisted session's snapshot. Refuses to
	// delete the live session — the operator would be deleting the file
	// the next auto-save immediately recreates.
	DeleteSession(id string) error

	// Checkpoints returns the current session's rewind checkpoints (per user
	// turn), newest first, for the /rewind picker. Returns nil when
	// checkpointing is disabled or the controller is a subagent.
	Checkpoints() []CheckpointInfo

	// RestoreCheckpoint rewinds to the checkpoint identified by id. mode is one
	// of "code" (rewrite captured before-images, delete since-created files),
	// "chat" (truncate the conversation to the turn boundary), or "both". It
	// returns a human-readable summary. Errors with ErrRunInProgress when a Run
	// is in flight, or when id/mode is invalid, or when a chat rewind would
	// cross a compaction boundary (CheckpointInfo.ChatRestoreOK was false). The
	// UI re-renders from Messages() after a chat/both restore.
	RestoreCheckpoint(id, mode string) (string, error)

	// Redactions returns the secrets masked out of tool results this run,
	// in first-seen order, for the /redactions overlay. Returns nil when
	// redaction is off.
	//
	// The RedactionInfo values carry the ORIGINAL secrets. They exist so
	// the operator can see what was masked and why — a false positive is
	// otherwise undiagnosable — and must be rendered UI-side only. Writing
	// one back into the transcript would hand the model the value the whole
	// wave exists to withhold.
	Redactions() []RedactionInfo

	// ContextReport returns the block-ledger breakdown behind the status
	// bar's context gauge: where the prompt's weight actually sits. topN
	// caps the returned Blocks (0 = all); Categories and TotalBytes always
	// describe the whole session.
	//
	// Safe to call from a UI goroutine mid-run — the implementation
	// snapshots the history under a lock.
	ContextReport(topN int) ContextReport

	// TogglePinnedBlock flips the pin on a tool result and reports the
	// resulting state. A pinned block is exempt from every rung of the
	// context ladder and is re-injected verbatim when compaction rewrites
	// history. Unknown ids are accepted — pinning is an intent, not an
	// assertion that the block exists yet.
	TogglePinnedBlock(toolID string) bool
}

// ContextBlock is one row in the /context overlay: an accounted chunk of
// the prompt.
type ContextBlock struct {
	ToolID   string // pin key; empty for non-tool blocks
	Category string // "system" | "user" | "assistant" | "file" | "tool"
	ToolName string // "read", "bash", … ; empty for non-tool blocks
	Label    string // file base name, command verb, pattern — the subject
	Bytes    int    // model-visible size
	Turn     int    // 1-based user-turn ordinal
	Pinned   bool   // exempt from the ladder
	Pruned   bool   // already replaced by a tombstone
	IsError  bool   // failed tool result; never pruned
}

// ContextReport is the /context overlay's data, snapshotted at open.
type ContextReport struct {
	// Blocks are the heaviest first, capped at the requested topN.
	Blocks []ContextBlock
	// Categories totals bytes across the WHOLE session, not just Blocks.
	Categories map[string]int
	// TotalBytes is the ledger's full model-visible size.
	TotalBytes int
	// Turns is how many user turns the session has seen.
	Turns int
	// UsedTokens / LimitTokens mirror the status-bar gauge so the overlay
	// and the bar can never disagree. LimitTokens is 0 for a model with
	// no entry in the context-size table.
	UsedTokens  int
	LimitTokens int
}

// RedactionInfo is one masked secret, as shown in the /redactions overlay.
type RedactionInfo struct {
	Placeholder string // the token substituted into the transcript
	RuleID      string // which rule claimed it ("github-token")
	Why         string // that rule's human explanation
	Value       string // the original. Never render without an explicit reveal.
	Count       int    // times this value was masked this run
}

// SessionInfo is one row in the /resume picker. Lightweight by design —
// only what the picker renders, so the UI can show many entries without
// loading full message bodies.
type SessionInfo struct {
	ID              string // session-id (matches the JSON file basename)
	FirstUserPrompt string // up to 200 chars; picker truncates to 150 for display
	Title           string // operator-set name (/title); empty when never named
	Label           string // what to render: Title, else FirstUserPrompt, else a placeholder
	ParentID        string // session this was forked from; empty for a root session
	Pinned          bool   // exempt from `evva sessions prune`
	Workdir         string // absolute directory the session was started in
	UpdatedAt       int64  // unix nano of last save (file mtime); resume picker sorts desc
	CreatedAt       int64  // unix nano of first save
	Profile         string // persona name at save time
	Provider        string // LLM provider name at save time
	Model           string // LLM model id at save time
	MessageCount    int    // length of Session.Messages — picker shows "<n> msgs"
}

// CheckpointInfo is one row in the /rewind picker — a per-user-turn checkpoint.
// Like SessionInfo it carries only what the picker renders, so listing many is
// cheap.
type CheckpointInfo struct {
	ID            string // opaque id passed back to RestoreCheckpoint
	PromptPreview string // first ~200 chars of the turn's user prompt
	CreatedAt     int64  // unix nano; picker renders a relative age
	FileCount     int    // files whose before-image this checkpoint captured
	// ChatRestoreOK is false when a full compaction since this checkpoint
	// invalidated its conversation cut-point — the picker then offers code
	// restore only (rewind PRD §5.2).
	ChatRestoreOK bool
}

// QuestionResponse is the UI-side payload returned through
// Controller.RespondQuestion. It mirrors question.Response but uses plain
// Go types so the ui package doesn't import internal/question.
type QuestionResponse struct {
	// Answers maps question text → answer string. For single-select the value
	// is the chosen option label; for multi-select it is comma-separated
	// labels; for "Other" it is the user-typed free text. Kept for back-compat;
	// prefer MultiAnswers for multi-select fidelity.
	Answers map[string]string
	// MultiAnswers maps question text → the chosen option labels (the native
	// multi-select form: single-select is a one-element slice, "Other" the typed
	// text). Additive in v2.x — when set it is authoritative; Answers stays
	// populated (comma-joined) for callers that still read it. Optional; nil is fine.
	MultiAnswers map[string][]string
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

// OutputStyleChoice is one row in the /output-style picker: the style
// name, its description blurb, and where it came from ("built-in",
// "user", or "project").
type OutputStyleChoice struct {
	Name        string
	Description string
	Source      string
}
