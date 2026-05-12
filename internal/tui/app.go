package tui

import "github.com/johnny1110/evva/internal/session"

// App is the root TUI component.
// It observes session state but owns no agent logic — all decisions stay in agent/.
type App struct {
	session *session.Session
}

func NewApp(s *session.Session) *App {
	return &App{session: s}
}

// Run starts the terminal UI event loop.
func (a *App) Run() error {
	// TODO: implement with bubbletea or similar
	return nil
}
