// Package bubbletea is the reference TUI implementation of internal/ui.UI.
//
// Layout (top → bottom):
//
//	┌──────────────────────────────────────┐
//	│ transcript (scrollable viewport)     │
//	├──────────────────────────────────────┤
//	│ tasks panel       (when non-empty)   │
//	├──────────────────────────────────────┤
//	│ subagents panel   (when non-empty)   │
//	├──────────────────────────────────────┤
//	│ > input textarea                     │
//	├──────────────────────────────────────┤
//	│ status bar                           │
//	└──────────────────────────────────────┘
//
// State machine: idle → running (on Enter) → idle | iter-limit. Ctrl+C
// cancels the in-flight run when running, quits the UI when idle.
package bubbletea

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/ui"
)

// UI is the bubbletea implementation. Construct with New(); attach an
// agent via Attach() before calling Run().
//
// Emit is called from the agent goroutine; everything else runs on
// bubbletea's main goroutine. Cross-goroutine handoff is via p.Send.
type UI struct {
	program *tea.Program
	model   *rootModel

	// Controller wiring. Set by Attach; read by the model on submit.
	// Guarded by mu only at Attach-time; the model copies the reference
	// once Run starts and bubbletea's main goroutine owns it afterward.
	mu         sync.Mutex
	controller ui.Controller
}

// New builds a UI ready to be Attached and Run.
func New() *UI {
	u := &UI{model: newRootModel()}
	// Program is created up-front so Emit can Send before Run starts.
	// Bubbletea queues messages on the program's internal channel and
	// drains them once Run begins.
	u.program = tea.NewProgram(u.model, tea.WithAltScreen())
	u.model.program = u.program
	return u
}

// Emit satisfies event.Sink. The agent calls this from its goroutine;
// we forward to bubbletea's main loop without blocking.
func (u *UI) Emit(e event.Event) {
	if u.program == nil {
		return
	}
	u.program.Send(eventMsg{Event: e})
}

// Attach hands the UI its agent controller. Must be called before Run.
func (u *UI) Attach(c ui.Controller) {
	u.mu.Lock()
	u.controller = c
	u.model.controller = c
	u.mu.Unlock()
}

// Run starts the bubbletea program and blocks until the UI exits.
// ctx cancellation triggers a clean shutdown.
func (u *UI) Run(ctx context.Context) error {
	// Forward ctx cancellation into the bubbletea program so external
	// signals (SIGINT in the parent, parent ctx.Report(), ...) wind us
	// down cleanly.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			u.program.Send(quitMsg{})
		case <-done:
		}
	}()

	_, err := u.program.Run()
	close(done)
	return err
}

// --- model ---------------------------------------------------------------

type rootModel struct {
	controller ui.Controller
	// program is the back-reference used by goroutines launched from
	// Update (Controller.Run / Continue) to send results back to the
	// main loop via Send. Set once by UI.New right after tea.NewProgram.
	program *tea.Program

	width  int
	height int

	transcript transcript
	viewport   viewport.Model
	input      textarea.Model

	state    runState
	usage    llm.Usage
	hintText string // surfaces under the status bar after a transient event
	model    string // populated on first event or query

	// runCancel cancels the current Controller.Run goroutine. Set when
	// state == stateRunning; cleared on completion.
	runCancel context.CancelFunc
}

func newRootModel() *rootModel {
	ta := textarea.New()
	ta.Placeholder = "Type a prompt and press Enter (Shift+Enter for newline) — Ctrl+C to quit"
	ta.Prompt = "> "
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.YPosition = 0

	return &rootModel{
		input:    ta,
		viewport: vp,
		state:    stateIdle,
	}
}

func (m *rootModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.handleResize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case eventMsg:
		return m.handleAgentEvent(msg.Event)

	case runDoneMsg:
		return m.handleRunDone(msg.Err)

	case quitMsg:
		m.cancelRunIfAny()
		return m, tea.Quit
	}

	// Forward unhandled messages to sub-models for things like textarea
	// blink ticks and viewport mouse events.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *rootModel) handleResize(w, h int) {
	m.width = w
	m.height = h
	m.layoutSizes()
}

// layoutSizes recomputes sub-component sizes. The transcript viewport
// gets whatever's left after fixed-height regions (panels + input +
// status). Panels are zero-height when their stores are empty.
func (m *rootModel) layoutSizes() {
	if m.width == 0 || m.height == 0 {
		return
	}
	taskHeight := lineCount(m.taskPanel())
	subHeight := lineCount(m.subPanel())
	inputHeight := m.input.Height() + 2 // +2 for border
	statusHeight := 1

	vpHeight := m.height - taskHeight - subHeight - inputHeight - statusHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	m.input.SetWidth(m.width - 4)
	m.refreshViewport()
}

func (m *rootModel) refreshViewport() {
	m.viewport.SetContent(m.transcript.String())
	m.viewport.GotoBottom()
}

func (m *rootModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.state == stateRunning {
			m.cancelRunIfAny()
			m.hintText = "cancelling…"
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyCtrlD:
		// Quit only on empty input — matches readline-style conventions.
		if strings.TrimSpace(m.input.Value()) == "" {
			return m, tea.Quit
		}
	case tea.KeyEnter:
		// Shift+Enter inserts a newline; plain Enter submits.
		if msg.Alt || msg.Type == tea.KeyShiftTab {
			break
		}
		return m.submit()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *rootModel) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if m.controller == nil {
		m.hintText = "no controller attached"
		return m, nil
	}

	switch m.state {
	case stateIterLimit:
		// Treat any Enter as "continue" when paused at iter limit. The
		// typed text is ignored — Continue() doesn't append a new user
		// message.
		m.input.SetValue("")
		m.startContinue()
	default:
		m.transcript.appendUserPrompt(text)
		m.input.SetValue("")
		m.startRun(text)
	}
	m.refreshViewport()
	return m, nil
}

func (m *rootModel) startRun(prompt string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	m.state = stateRunning
	m.hintText = ""

	p := m.program
	go func() {
		_, err := m.controller.Run(ctx, prompt)
		if p != nil {
			p.Send(runDoneMsg{Err: err})
		}
	}()
}

func (m *rootModel) startContinue() {
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	m.state = stateRunning
	m.hintText = ""

	p := m.program
	go func() {
		_, err := m.controller.Continue(ctx)
		if p != nil {
			p.Send(runDoneMsg{Err: err})
		}
	}()
}

func (m *rootModel) cancelRunIfAny() {
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
	}
}

func (m *rootModel) handleAgentEvent(e event.Event) (tea.Model, tea.Cmd) {
	if e.Kind == event.KindUsage && e.Usage != nil {
		m.usage = e.Usage.Cumulative
	}
	if e.Kind == event.KindStoreUpdate {
		// Layout may change (panel appearing/disappearing).
		m.layoutSizes()
	}
	if m.transcript.foldEvent(e) {
		m.refreshViewport()
	}
	// Fetch model name lazily if we don't have it.
	if m.model == "" && m.controller != nil {
		// Controller doesn't expose llm.Client directly; UIs that want the
		// model name today read it from the session's first run event or
		// the cumulative usage trace. For v1 leave blank until KindRunEnd
		// brings the Final response into the transcript (we still render
		// "model -" until then).
	}
	return m, nil
}

func (m *rootModel) handleRunDone(err error) (tea.Model, tea.Cmd) {
	m.runCancel = nil
	switch {
	case err == nil:
		m.state = stateIdle
		m.hintText = ""
	case errors.Is(err, llm.ErrInterrupted):
		m.state = stateIdle
		m.hintText = "interrupted"
	default:
		// ErrIterLimit lives in the agent package, but we don't want to
		// import it here — match on the sentinel string instead.
		if strings.Contains(err.Error(), "iteration limit") {
			m.state = stateIterLimit
			m.hintText = "press Enter to continue, Ctrl+C to quit"
		} else {
			m.state = stateIdle
			m.hintText = "error: " + truncate(err.Error(), 120)
		}
	}
	m.layoutSizes()
	return m, nil
}

func (m *rootModel) View() string {
	if m.width == 0 {
		return "initializing…"
	}
	var sections []string
	sections = append(sections, m.viewport.View())

	if p := m.taskPanel(); p != "" {
		sections = append(sections, p)
	}
	if p := m.subPanel(); p != "" {
		sections = append(sections, p)
	}
	sections = append(sections, styles.InputBorder.Render(m.input.View()))
	sections = append(sections, renderStatusBar(m.width, m.modelName(), m.usage, m.state, m.hintText))
	return strings.Join(sections, "\n")
}

func (m *rootModel) taskPanel() string {
	if m.controller == nil {
		return ""
	}
	return renderTaskPanel(m.controller.ToolState())
}

func (m *rootModel) subPanel() string {
	if m.controller == nil {
		return ""
	}
	return renderSubagentPanel(m.controller.ToolState())
}

func (m *rootModel) modelName() string {
	if m.model != "" {
		return m.model
	}
	return "-"
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
