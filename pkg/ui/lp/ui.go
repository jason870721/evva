// Package lp is evva's "low profile" terminal UI: a quiet, professional
// black + gold alternative to the bundled NEON TOKYO TUI. It satisfies the
// public ui.UI contract (pkg/ui) and drives any agent through ui.Controller,
// so it depends only on pkg/* — a host selects it with `evva -tui lp`.
//
// lp owns its identity (theme, root model, slim top status line, underline
// input, compact panels — and crucially, NO banner) while reusing evva's
// proven renderers from pkg/ui/bubbletea/components (transcript, slash,
// overlays) re-themed black + gold. Reused message types come from
// pkg/ui/bubbletea/events so the two UIs share one msg vocabulary.
//
// Wiring (host responsibility):
//
//	tui := lp.New(evvaHome)
//	ag, _ := agent.New(agent.Config{...}, agent.WithSink(tui), agent.WithRootContext(ctx))
//	tui.Attach(ag.Controller())
//	tui.Run(ctx)
package lp

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
	"github.com/johnny1110/evva/pkg/ui/lp/app"
)

// UI is the low-profile TUI. Construct with New(); attach an agent via
// Attach() before calling Run().
type UI struct {
	program *tea.Program
	model   *app.App

	mu         sync.Mutex
	controller ui.Controller
}

// New builds a UI ready to be Attached and Run. evvaHome is the user's
// config directory; it is plumbed to the app model for future settings
// resolution (lp deliberately has no banner to load).
//
// Mouse capture is on via tea.WithMouseCellMotion so the wheel scrolls
// the transcript viewport.
func New(evvaHome string) *UI {
	u := &UI{model: app.New(evvaHome, goldTheme())}
	u.program = tea.NewProgram(u.model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	u.model.SetProgram(u.program)
	return u
}

// Emit satisfies event.Sink. Called from the agent goroutine; forwards to
// the bubbletea main loop via Send so all state mutation stays on one
// goroutine.
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

// Run starts the bubbletea program and blocks until exit. ctx cancellation
// triggers a clean shutdown via a QuitMsg.
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
