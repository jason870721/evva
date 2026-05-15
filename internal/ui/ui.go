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
}
