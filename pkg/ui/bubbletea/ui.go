// Package bubbletea is evva's reference terminal UI: it satisfies the
// public ui.UI contract (pkg/ui) and drives any agent through
// ui.Controller, so it depends only on pkg/* — a downstream host embeds
// it exactly the way cmd/evva does. Built from focused components, a focus
// stack, and a declarative layout engine rather than a god-object root
// model.
//
// Wiring (host responsibility):
//
//	tui := bubbletea.New(evvaHome)
//	ag, _ := agent.New(agent.Config{...}, agent.WithSink(tui), agent.WithRootContext(ctx))
//	tui.Attach(ag.Controller())
//	tui.Run(ctx)
package bubbletea

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/app"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
)

// UI is the v2 bubbletea TUI. Construct with New(); attach an agent via
// Attach() before calling Run().
type UI struct {
	program *tea.Program
	model   *app.App

	mu         sync.Mutex
	controller ui.Controller
}

// New builds a UI ready to be Attached and Run. evvaHome is the user's
// config directory (typically ~/.evva); it is plumbed through to the
// app model for future banner / settings resolution.
//
// Mouse capture is on via tea.WithMouseCellMotion so the wheel
// scrolls the transcript viewport. The trade-off (no native
// drag-select unless the user holds Shift/Alt) is documented in the
// approved plan; the Ctrl+Y yank mode is the canonical clean-copy
// path for users on terminals where Shift-bypass doesn't work
// (tmux, screen).
func New(evvaHome string) *UI {
	u := &UI{model: app.New(evvaHome)}
	u.program = tea.NewProgram(u.model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	u.model.SetProgram(u.program)
	return u
}

// Emit satisfies event.Sink. Called from the agent goroutine; forwards
// to the bubbletea main loop via Send so all state mutation stays on
// one goroutine.
func (u *UI) Emit(e event.Event) {
	if u.program == nil {
		return
	}
	u.program.Send(events.AgentEventMsg{Event: e})
}

// Attach hands the UI its agent controller. Must be called before Run.
func (u *UI) Attach(c ui.Controller) {
	u.mu.Lock()
	u.controller = c
	u.model.Attach(c)
	u.mu.Unlock()
}

// Run starts the bubbletea program and blocks until exit. ctx
// cancellation triggers a clean shutdown via a QuitMsg.
func (u *UI) Run(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			u.program.Send(events.QuitMsg{})
		case <-done:
		}
	}()
	_, err := u.program.Run()
	close(done)
	return err
}
