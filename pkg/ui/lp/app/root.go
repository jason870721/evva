// Package app is lp's top-level tea.Model — the low-profile root. It stays
// thin: a focus stack, layout math, and message dispatch live here; the
// transcript, overlays, and slash panel are reused from
// pkg/ui/bubbletea/components re-themed black + gold, while the status line,
// input, and panels are lp's own. There is deliberately no banner and no
// thinking sprite.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/overlays"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/slash"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/status"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/transcript"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/mouse"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// App is lp's root model.
type App struct {
	evvaHome   string
	program    *tea.Program
	controller ui.Controller

	width  int
	height int

	theme      *theme.Theme
	transcript *transcript.Transcript
	view       *transcript.View
	input      *Input
	status     *StatusLine
	state      *status.State

	focus *FocusStack
	slash *slash.Panel

	runCancel   context.CancelFunc
	interrupted bool

	// lastMouseEventAt dedups synthesised arrow keys some terminals emit
	// alongside wheel events (see handleKey's up/down branch).
	lastMouseEventAt time.Time
}

// New builds a fresh App with the given (black + gold) theme. The program
// reference is wired afterwards via SetProgram.
func New(evvaHome string, th *theme.Theme) *App {
	tr := transcript.New()
	tr.SetTheme(th)
	v := transcript.NewView(tr)
	st := status.NewState()

	return &App{
		evvaHome:   evvaHome,
		theme:      th,
		transcript: tr,
		view:       v,
		input:      NewInput(th),
		status:     NewStatusLine(st),
		state:      st,
		focus:      NewFocusStack(),
		slash:      slash.New(),
	}
}

// SetProgram hands the model the program reference used by run goroutines to
// dispatch RunDoneMsg back to the main loop.
func (a *App) SetProgram(p *tea.Program) { a.program = p }

// Attach hands the model the agent controller and primes the status line.
func (a *App) Attach(c ui.Controller) {
	a.controller = c
	a.status.SetModel(c.Model())
	a.status.SetEffort(c.Effort())
	a.status.SetPermissionMode(c.PermissionModeName())
	a.status.SetContext(0, status.ContextLimitFor(c.Model()))
	a.view.MarkDirty()
	a.relayout()
}

// Init animates the cursor and the status-line spinner from the first frame.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.input.BlinkCmd(), status.SpinnerTickCmd())
}

// Update routes incoming messages.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		a.input.SetWidth(m.Width)
		a.relayout()
		return a, nil

	case events.QuitMsg:
		if a.runCancel != nil {
			a.runCancel()
		}
		return a, tea.Quit

	case events.SpinnerTickMsg:
		a.state.TickSpinner()
		return a, status.SpinnerTickCmd()

	case events.AgentEventMsg:
		return a.handleAgentEvent(m.Event)

	case events.RunDoneMsg:
		return a.handleRunDone(m.Err)

	case SubmitMsg:
		return a.handleSubmit(m)

	case tea.MouseMsg:
		if mouse.IsWheelEvent(m) {
			a.lastMouseEventAt = time.Now()
			return a, a.view.Update(m)
		}
		return a, nil

	case events.ClipboardMsg:
		if m.OK {
			a.state.SetHint(fmt.Sprintf("copied %d chars (%s)", m.Size, m.Method))
		} else if m.Err != nil {
			a.state.SetHint("clipboard: " + m.Err.Error())
		}
		return a, nil

	case overlays.YankCursorChangedMsg:
		a.view.MarkDirty()
		return a, nil

	case overlays.SearchRevealMsg:
		a.view.RevealBlock(m.BlockID)
		a.view.MarkDirty()
		return a, nil

	case overlays.CompactDoneMsg:
		if m.Err != nil {
			a.state.SetHint("compact failed: " + m.Err.Error())
		}
		return a, nil

	case overlays.ModelSwitchedMsg:
		a.transcript.Reset()
		a.status.SetModel(a.controller.Model())
		a.status.SetEffort(a.controller.Effort())
		a.status.SetUsage(llm.Usage{})
		a.status.SetContext(0, status.ContextLimitFor(a.controller.Model()))
		a.state.SetHint("switched to " + m.Provider.Name + " / " + string(m.Model) + " · history cleared")
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case overlays.ProfileSwitchedMsg:
		a.transcript.Reset()
		a.status.SetModel(a.controller.Model())
		a.status.SetEffort(a.controller.Effort())
		a.status.SetUsage(llm.Usage{})
		a.status.SetContext(0, status.ContextLimitFor(a.controller.Model()))
		a.state.SetHint("switched to " + m.Name + " · history cleared")
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case overlays.EffortSwitchedMsg:
		a.status.SetEffort(m.Level)
		a.state.SetHint("effort set to " + m.Level)
		a.view.MarkDirty()
		return a, nil

	case overlays.SessionResumedMsg:
		a.transcript.LoadFromMessages(a.controller.Messages())
		a.status.SetModel(a.controller.Model())
		a.status.SetEffort(a.controller.Effort())
		a.status.SetPermissionMode(a.controller.PermissionModeName())
		a.status.SetUsage(a.controller.Usage())
		a.status.SetContext(a.controller.LastTurnInputTokens(), status.ContextLimitFor(a.controller.Model()))
		a.state.SetHint("resumed session " + m.ID + " · type a prompt to continue")
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case overlays.ApprovalRespondedMsg:
		return a, nil

	case overlays.QuestionRespondedMsg:
		return a, nil

	case tea.KeyMsg:
		return a.handleKey(m)
	}

	// Fallthrough: route anything unclaimed to the focused modal overlay so
	// overlays that fire their own async messages (e.g. /update's check
	// results) receive them.
	if top := a.focus.Top(); top != nil && top.Modal() {
		closes, cmd := top.Update(msg)
		if closes {
			a.focus.Pop()
			a.relayout()
		}
		return a, cmd
	}
	return a, nil
}

// handleAgentEvent fans one agent event through the state machine, the status
// line, and the transcript, and drives the todo auto-fold.
func (a *App) handleAgentEvent(e event.Event) (tea.Model, tea.Cmd) {
	a.state.Apply(e)

	if e.Kind == event.KindApprovalNeeded && e.ApprovalNeeded != nil {
		if o := overlays.NewApproval(a.controller, *e.ApprovalNeeded); o != nil {
			a.focus.Push(o)
			a.view.MarkDirty()
			a.relayout()
		}
		return a, nil
	}
	if e.Kind == event.KindQuestionNeeded && e.QuestionNeeded != nil {
		if o := overlays.NewQuestion(a.controller, *e.QuestionNeeded); o != nil {
			a.focus.Push(o)
			a.view.MarkDirty()
			a.relayout()
		}
		return a, nil
	}

	if e.Usage != nil {
		a.status.SetUsage(e.Usage.Cumulative)
	}
	if e.Kind == event.KindModeChanged && e.ModeChanged != nil {
		a.status.SetPermissionMode(e.ModeChanged.Mode)
	}
	if a.transcript.IngestEvent(e) {
		a.view.MarkDirty()
	}
	if a.controller != nil {
		a.status.SetContext(
			a.controller.LastTurnInputTokens(),
			status.ContextLimitFor(a.controller.Model()),
		)
	}

	var cmd tea.Cmd
	if e.Kind == event.KindStoreUpdate && e.StoreUpdate != nil &&
		e.StoreUpdate.Domain == todo.Domain && a.controller != nil {
		if AllTodosCompleted(a.controller.TodoStore()) {
			snap := RenderTasksDoneSnapshot(a.controller.TodoStore(), a.transcriptWidth(), a.theme)
			a.transcript.AppendSynthetic(snap)
			a.view.MarkDirty()
			store := a.controller.TodoStore()
			// Clear off-goroutine: each deletion emits a store update that
			// routes back as a msg; clearing inline would re-enter Update.
			cmd = func() tea.Msg {
				store.Clear()
				return nil
			}
		}
	}
	if e.Kind == event.KindStoreUpdate {
		a.relayout()
	}
	return a, cmd
}

// relayout recomputes the transcript viewport height from current chrome.
// Vertical budget: status line + top rule + bottom rule + input + hint = 5
// rows, plus any todos panel, daemon line, and overlay/slash body.
func (a *App) relayout() {
	if a.width == 0 || a.height == 0 {
		return
	}
	used := 5
	if a.controller != nil {
		if panel := RenderTodos(a.controller.TodoStore(), a.transcriptWidth(), a.theme); panel != "" {
			used += strings.Count(panel, "\n") + 1
		}
		if line := RenderDaemons(a.controller.DaemonState(), a.transcriptWidth(), a.theme); line != "" {
			used++
		}
	}
	if top := a.focus.Top(); top != nil {
		if body := top.View(a.transcriptWidth(), a.theme); body != "" {
			used += strings.Count(body, "\n") + 1
		}
	} else if a.slashVisible() {
		if body := a.slash.View(a.input.Value(), a.controller, a.transcriptWidth(), a.theme); body != "" {
			used += strings.Count(body, "\n") + 1
		}
	}
	a.view.SetSize(a.width, max(a.height-used, 1))
}

// transcriptWidth is the column count panels and snapshots size to.
func (a *App) transcriptWidth() int {
	if a.width < 20 {
		return 20
	}
	return a.width
}

func (a *App) handleRunDone(err error) (tea.Model, tea.Cmd) {
	a.runCancel = nil
	interrupted := a.interrupted
	a.interrupted = false
	if errors.Is(err, llm.ErrInterrupted) {
		interrupted = true
	}
	a.state.OnRunDone(err, interrupted)
	return a, nil
}

// handleKey routes a key event. Precedence: modal overlay → globals → slash
// panel → input.
func (a *App) handleKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Wheel-derived arrow-key dedup.
	if !a.lastMouseEventAt.IsZero() && time.Since(a.lastMouseEventAt) < 80*time.Millisecond {
		switch m.String() {
		case "up", "down":
			return a, a.view.Update(m)
		}
	}

	// Layer 1: modal overlay — exclusive consumer.
	if top := a.focus.Top(); top != nil && top.Modal() {
		if m.String() == "ctrl+c" {
			a.focus.Pop()
			a.relayout()
			if a.runCancel != nil {
				a.runCancel()
			}
			return a, tea.Quit
		}
		switch m.String() {
		case "pgup", "pgdown", "home", "end":
			return a, a.view.Update(m)
		}
		closes, cmd := top.Update(m)
		if closes {
			a.focus.Pop()
			a.relayout()
		}
		return a, cmd
	}

	// Layer 2: global keys.
	switch m.String() {
	case "ctrl+c":
		if a.runCancel != nil {
			a.interrupted = true
			a.runCancel()
			return a, nil
		}
		return a, tea.Quit

	case "esc":
		if a.runCancel != nil {
			a.interrupted = true
			a.runCancel()
			return a, nil
		}
		if a.state.Current() == status.StateError {
			a.state.Dismiss()
			return a, nil
		}
		if a.slashVisible() {
			a.slash.Dismiss()
			return a, nil
		}
		return a, tea.Quit

	case "ctrl+o":
		a.transcript.ToggleExpand()
		a.view.MarkDirty()
		return a, nil

	case "ctrl+y":
		if a.transcript == nil || len(a.transcript.Blocks()) == 0 {
			return a, nil
		}
		y := overlays.NewYank(a.transcript)
		if y == nil {
			return a, nil
		}
		a.focus.Push(y)
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case "shift+tab":
		if a.controller != nil {
			next := a.controller.CyclePermissionMode()
			a.status.SetPermissionMode(next)
			a.state.SetHint("permission mode: " + next)
		}
		return a, nil

	case "ctrl+f":
		if a.transcript == nil || len(a.transcript.Blocks()) == 0 {
			return a, nil
		}
		s := overlays.NewSearch(a.transcript)
		if s == nil {
			return a, nil
		}
		a.focus.Push(s)
		a.view.MarkDirty()
		a.relayout()
		return a, s.BlinkCmd()

	case "pgup", "pgdown", "home", "end":
		return a, a.view.Update(m)
	}

	// Layer 3: slash panel navigation.
	if a.slashVisible() {
		catalog := slash.Catalog(a.controller)
		switch m.String() {
		case "tab":
			if name := a.slash.Complete(a.input.Value(), catalog); name != "" {
				a.input.SetValue(name)
			}
			return a, nil
		case "up":
			if a.slash.MoveSel(a.input.Value(), catalog, -1) {
				return a, nil
			}
		case "down":
			if a.slash.MoveSel(a.input.Value(), catalog, +1) {
				return a, nil
			}
		}
	}

	// Layer 4: input textarea.
	return a, a.input.Update(m)
}

func (a *App) slashVisible() bool {
	overlayOpen := a.focus.Len() > 0
	return a.slash.Visible(a.input.Value(), overlayOpen, slash.Catalog(a.controller))
}

// handleSubmit dispatches a SubmitMsg: slash commands open reused overlays;
// regular text starts (or queues) a Run.
func (a *App) handleSubmit(m SubmitMsg) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.ForAgent)

	switch text {
	case "/exit", "/quit", "exit":
		a.input.Reset()
		a.slash.Reset()
		return a, tea.Quit
	case "/clear":
		a.input.Reset()
		a.slash.Reset()
		// Start a NEW session (fresh history/usage/todos, new id) — the old
		// one stays on disk for /resume. Refused mid-run; the scrollback
		// survives a refused clear so nothing silently vanishes.
		if a.controller != nil {
			if err := a.controller.ClearSession(); err != nil {
				a.state.SetHint("clear: " + err.Error())
				a.view.MarkDirty()
				return a, nil
			}
		}
		a.transcript.Reset()
		a.state.OnRunDone(nil, false)
		if a.controller != nil {
			a.status.SetUsage(a.controller.Usage())
			a.status.SetContext(0, status.ContextLimitFor(a.controller.Model()))
			a.state.SetHint("new session started · /resume lists the old one")
		}
		a.view.MarkDirty()
		return a, nil
	case "/config":
		return a.openOverlay(overlays.NewConfig(a.controller))
	case "/model":
		return a.openOverlay(overlays.NewModel(a.controller))
	case "/profile":
		return a.openOverlay(overlays.NewProfile(a.controller))
	case "/compact":
		return a.openOverlay(overlays.NewCompact(a.controller))
	case "/effort":
		return a.openOverlay(overlays.NewEffort(a.controller))
	case "/resume":
		return a.openOverlay(overlays.NewResume(a.controller))
	case "/update":
		a.input.Reset()
		a.slash.Reset()
		u := overlays.NewUpdate()
		a.focus.Push(u)
		a.relayout()
		return a, u.StartCheck()
	}

	if a.state.Current() == status.StateIterLimit {
		a.input.Reset()
		a.startContinue()
		return a, nil
	}

	if text == "" {
		return a, nil
	}
	if a.controller == nil {
		a.state.SetHint("no controller attached")
		return a, nil
	}

	// Mid-run submit: queue the prompt rather than starting a second Run.
	if a.runCancel != nil {
		a.transcript.AppendUserPrompt(m.ForView)
		a.input.Reset()
		a.controller.EnqueueUserPrompt(m.ForAgent)
		a.state.SetHint("queued — will land at next iteration")
		a.view.MarkDirty()
		return a, nil
	}

	a.transcript.AppendUserPrompt(m.ForView)
	a.input.Reset()
	a.view.MarkDirty()
	a.startRun(m.ForAgent)
	return a, nil
}

// openOverlay pushes a Focusable overlay (or surfaces a hint when the
// controller isn't attached) and resets the input/slash state.
func (a *App) openOverlay(o Focusable) (tea.Model, tea.Cmd) {
	a.input.Reset()
	a.slash.Reset()
	if o == nil || a.controller == nil {
		a.state.SetHint("no controller attached")
		return a, nil
	}
	a.focus.Push(o)
	a.relayout()
	return a, nil
}

func (a *App) startRun(prompt string) {
	if a.controller == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runCancel = cancel
	a.state.OnSubmit()

	p := a.program
	go func() {
		_, err := a.controller.Run(ctx, prompt)
		if p != nil {
			p.Send(events.RunDoneMsg{Err: err})
		}
	}()
}

func (a *App) startContinue() {
	if a.controller == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runCancel = cancel
	a.state.OnSubmit()

	p := a.program
	go func() {
		_, err := a.controller.Continue(ctx)
		if p != nil {
			p.Send(events.RunDoneMsg{Err: err})
		}
	}()
}

// View composes the frame top→bottom (each chrome layer is fixed height; the
// transcript flexes; panels/overlay collapse when empty):
//
//	status line
//	─ rule ─
//	transcript viewport
//	todos panel        (when tracked)
//	daemon line        (when running)
//	overlay | slash    (when open / typing "/")
//	─ rule ─
//	input
//	hint
func (a *App) View() string {
	if a.width == 0 {
		return "initializing…"
	}
	rule := a.theme.StatusSep.Render(strings.Repeat("─", a.width))

	var b strings.Builder
	b.WriteString(a.status.Render(a.width, a.theme))
	b.WriteByte('\n')
	b.WriteString(rule)
	b.WriteByte('\n')
	b.WriteString(a.view.View())

	width := a.transcriptWidth()
	if a.controller != nil {
		if panel := RenderTodos(a.controller.TodoStore(), width, a.theme); panel != "" {
			b.WriteByte('\n')
			b.WriteString(panel)
		}
		if line := RenderDaemons(a.controller.DaemonState(), width, a.theme); line != "" {
			b.WriteByte('\n')
			b.WriteString(line)
		}
	}

	if top := a.focus.Top(); top != nil {
		if body := top.View(width, a.theme); body != "" {
			b.WriteByte('\n')
			b.WriteString(body)
		}
	} else if a.slashVisible() {
		b.WriteByte('\n')
		b.WriteString(a.slash.View(a.input.Value(), a.controller, width, a.theme))
	}

	b.WriteByte('\n')
	b.WriteString(rule)
	b.WriteByte('\n')
	b.WriteString(a.input.View())
	b.WriteByte('\n')

	var provider status.HintProvider
	if top := a.focus.Top(); top != nil {
		provider = hintAdapter{top.Hint()}
	}
	b.WriteString(a.theme.FooterHint.Render("  " + status.ResolveHint(a.state, provider)))
	return b.String()
}

// hintAdapter wraps a static string in status.HintProvider so a focused
// overlay's Hint() can win over the run-state default.
type hintAdapter struct{ s string }

func (h hintAdapter) Hint() string { return h.s }
