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

	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/toolset"
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

// Controller is the narrow API a UI uses to send commands back to the
// agent. Implemented by *agent.Agent.
//
// The interface is intentionally minimal. State the UI wants to render
// (tasks, subagents, usage) lives behind Session and ToolState — the UI
// reads those via the typed accessors on each side, not through bespoke
// Controller methods.
type Controller interface {
	// Run drives the agent for a single user turn. The UI typically
	// launches this in a goroutine so its main loop stays responsive,
	// and ctx-cancels to honor user interrupts.
	Run(ctx context.Context, prompt string) (string, error)

	// Continue resumes an iter-limit-paused run without appending a new
	// user message.
	Continue(ctx context.Context) (string, error)

	// Session exposes the conversation history. The UI reads cumulative
	// usage from Session().Usage and replays Session().Messages on
	// resume.
	Session() *session.Session

	// ToolState exposes the shared backing-store registry. UIs that want
	// to render task or subagent panels read state through
	// ToolState().TaskStore() / ToolState().AgentGroup(), and
	// subscribe to observable.Change events via ToolState().Subscribe().
	ToolState() *toolset.ToolState

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

	// Effort returns the current effort level name ("low"|"medium"|"high"|"ultra").
	Effort() string

	// SetEffort updates the effort level at runtime. Validates the name;
	// returns an error for unknown levels. Persists to config and applies
	// to the LLM client on the next completion.
	SetEffort(level string) error

	// Skills returns the merged catalog of user-installed skills (home
	// and workdir, with workdir overrides applied). The TUI's slash
	// suggestion panel surfaces each entry as `/<name>` with the
	// description; the agent decides if/when to invoke them via the
	// SKILL tool. Returns nil when no skills are installed.
	Skills() []Skill

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
