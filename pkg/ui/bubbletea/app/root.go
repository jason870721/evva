// Package app is the v2 TUI's top-level tea.Model. It stays thin on
// purpose — focus stack, layout engine, and msg dispatch live here;
// every visual concern lives in a sibling component package.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/banner"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/agents"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/bgtasks"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/input"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/monitors"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/overlays"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/slash"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/status"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/todos"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/transcript"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/mouse"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// defaultGreeting is the welcome line rendered inside the banner box
// on startup.
const defaultGreeting = "// neural link established — what shall we build, ʘᴥʘ?"

// App is the v2 root model. M7 wires the focus stack (modal
// overlays: /config /model /compact) and the slash suggestion
// panel (non-modal, floats above the input when the user types
// "/"). Key routing precedence is now:
//
//  1. top of focus stack (if it's modal) — exclusive consumer
//  2. global keys (ctrl+c, esc, ctrl+o, scroll)
//  3. slash panel (when visible) for Tab / Up / Down
//  4. input textarea (history nav, paste, plain typing)
type App struct {
	evvaHome   string
	program    *tea.Program
	controller ui.Controller

	width  int
	height int

	theme      *theme.Theme
	transcript *transcript.Transcript
	view       *transcript.View
	input      *input.Input
	status     *status.StatusBar
	state      *status.State

	// focus is the modal overlay stack. Empty during normal
	// composition; non-empty while /config /model /compact (or M8+
	// yank / search / permission) is open.
	focus *FocusStack
	// slash is the autocomplete suggestion panel. Visible whenever
	// the input starts with "/" and the user hasn't dismissed it
	// for this typing session.
	slash *slash.Panel

	// runCancel is the cancel func for the in-flight Run, set in
	// startRun and cleared in handleRunDone. Used by the Esc /
	// Ctrl+C handlers to interrupt mid-flight.
	runCancel context.CancelFunc
	// interrupted captures the "user pressed Esc" signal so the
	// RunDoneMsg handler can pick the "interrupted" hint instead
	// of "error: ...". Cleared on next OnSubmit.
	interrupted bool

	// lastMouseEventAt is the timestamp of the most recent mouse wheel
	// event we received. Some terminals (tmux without mouse-on, several
	// SSH setups) emit a wheel event AND a synthesised arrow-key event
	// for the same scroll; without dedup the arrow key reaches the
	// input box and triggers history navigation, replacing what the
	// user is composing. See handleKey's up/down branch.
	lastMouseEventAt time.Time

	startedAt time.Time
}

// New builds a fresh App. The program reference is wired in
// afterwards.
func New(evvaHome string) *App {
	th := theme.Default()
	tr := transcript.New()
	tr.SetTheme(th)
	tr.SetBanner(transcript.BannerSpec{
		Art:      banner.Load(evvaHome),
		Greeting: defaultGreeting,
	})
	v := transcript.NewView(tr)
	in := input.New(th)
	st := status.NewState()
	bar := status.New(st)

	return &App{
		evvaHome:   evvaHome,
		theme:      th,
		transcript: tr,
		view:       v,
		input:      in,
		status:     bar,
		state:      st,
		focus:      NewFocusStack(),
		slash:      slash.New(),
		startedAt:  time.Now(),
	}
}

// SetProgram lets the package-level UI hand the model the program
// reference. Used by the run goroutine to dispatch RunDoneMsg back
// to the bubbletea main loop.
func (a *App) SetProgram(p *tea.Program) { a.program = p }

// Attach hands the model the agent controller and re-renders the
// banner. Also primes the status bar with model + agent id and the
// initial context limit.
func (a *App) Attach(c ui.Controller) {
	a.controller = c
	a.refreshBanner()
	a.status.SetModel(c.Model())
	a.status.SetEffort(c.Effort())
	a.status.SetAgentID(c.AgentID())
	a.status.SetAgentName(strings.ToUpper(c.ProfileName()))
	a.status.SetPermissionMode(c.PermissionModeName())
	a.status.SetContext(0, status.ContextLimitFor(c.Model()))
	a.view.MarkDirty()
	a.relayout()
}

func (a *App) refreshBanner() {
	if a.controller == nil {
		return
	}
	id := a.controller.AgentID()
	if len(id) > 8 {
		id = id[:8]
	}
	workdir, err := os.Getwd()
	if err != nil {
		workdir = "(unknown)"
	}
	a.transcript.SetBanner(transcript.BannerSpec{
		Art:      banner.Load(a.evvaHome),
		Greeting: defaultGreeting,
		Info: []transcript.BannerInfo{
			{Label: "version", Value: bannerVersion()},
			{Label: "workdir", Value: workdir},
			{Label: "session", Value: id},
			{Label: "started", Value: a.startedAt.Format("2006-01-02 15:04:05")},
		},
	})
}

// bannerVersion returns just the version string (e.g. "v0.5.2") without the
// commit + build-date suffix that config.DisplayVersion adds for --version
// output. The banner already shows started-at and runs in a fixed-width box,
// so the extra suffix is noise.
func bannerVersion() string {
	v := config.Version
	if v == "" {
		v = config.DefaultAppVersion
	}
	return v
}

// Init returns the cursor blink + spinner tick so both animate from
// the first frame.
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
		// Advance the spinner; re-arm the tick. Cheap enough to run
		// unconditionally — the cache layer prevents per-tick block
		// re-renders unless something actually animates.
		a.state.TickSpinner()
		// If a compaction block is animating, the transcript needs
		// to know about the new frame so its CompactingBlock bumps
		// Rev and re-renders.
		if a.transcript.HasInflightCompacting() {
			a.transcript.SetSpinnerFrame(a.state.Frame())
			a.view.MarkDirty()
		}
		// If the thinking sprite is mounted, advance its walk cycle.
		if a.transcript.HasThinkingSprite() {
			a.transcript.SetThinkingSpriteFrame(a.state.Frame())
			a.view.MarkDirty()
		}
		return a, status.SpinnerTickCmd()

	case events.AgentEventMsg:
		return a.handleAgentEvent(m.Event)

	case events.RunDoneMsg:
		return a.handleRunDone(m.Err)

	case input.SubmitMsg:
		return a.handleSubmit(m)

	case tea.MouseMsg:
		// Only wheel events do anything in v2; non-wheel events
		// (clicks, motion) are dropped. Native drag-select still
		// works in modern terminals via Shift/Alt-bypass.
		if mouse.IsWheelEvent(m) {
			a.lastMouseEventAt = time.Now()
			return a, a.view.Update(m)
		}
		return a, nil

	case events.ClipboardMsg:
		if m.OK {
			// Include the backend so the user can tell whether the
			// payload landed via pbcopy/xclip (native) or via the
			// OSC52 escape — useful when the terminal claims OSC52
			// support but is silently dropping the escape.
			a.state.SetHint(fmt.Sprintf("copied %d chars (%s)", m.Size, m.Method))
		} else if m.Err != nil {
			a.state.SetHint("clipboard: " + m.Err.Error())
		}
		return a, nil

	case overlays.YankCursorChangedMsg:
		// The yank overlay already set the transcript's focused
		// block; we just need to invalidate the rendered viewport
		// so the cyan-gutter accent repaints.
		a.view.MarkDirty()
		return a, nil

	case overlays.SearchRevealMsg:
		// The search overlay moved its match cursor. Scroll the
		// viewport so the target block is visible AND re-render
		// so any new match-accent gutter colors paint.
		a.view.RevealBlock(m.BlockID)
		a.view.MarkDirty()
		return a, nil

	case overlays.CompactDoneMsg:
		if m.Err != nil {
			a.state.SetHint("compact failed: " + m.Err.Error())
		}
		return a, nil

	case overlays.ModelSwitchedMsg:
		// Mirror the controller-side swap in the UI: clear the
		// transcript (preserve banner), refresh the banner with
		// the new model id, reset cumulative usage.
		a.transcript.Reset()
		a.refreshBanner()
		a.status.SetModel(a.controller.Model())
		a.status.SetEffort(a.controller.Effort())
		a.status.SetUsage(llm.Usage{})
		a.status.SetContext(0, status.ContextLimitFor(a.controller.Model()))
		a.state.SetHint("switched to " + m.Provider.Name + " / " + string(m.Model) + " · history cleared")
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case overlays.ProfileSwitchedMsg:
		// Persona swap: agent has rebuilt active tools + session + LLM
		// client. Mirror that here — clear the transcript, refresh the
		// banner, update the dynamic agent label, reset usage. The new
		// system prompt is in effect on the next Run.
		a.transcript.Reset()
		a.refreshBanner()
		a.status.SetAgentName(strings.ToUpper(m.Name))
		a.status.SetModel(a.controller.Model())
		a.status.SetEffort(a.controller.Effort())
		a.status.SetUsage(llm.Usage{})
		a.status.SetContext(0, status.ContextLimitFor(a.controller.Model()))
		a.state.SetHint("switched to " + m.Name + " · history cleared")
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case overlays.EffortSwitchedMsg:
		a.refreshBanner()
		a.status.SetEffort(m.Level)
		a.state.SetHint("effort set to " + m.Level)
		a.view.MarkDirty()
		return a, nil

	case overlays.SessionResumedMsg:
		// Session swap: the agent has rehydrated its transcript + profile
		// + LLM under the loaded snapshot. Mirror that on the UI side —
		// rebuild the visible transcript by replaying every persisted
		// llm.Message as the equivalent set of blocks, refresh the banner
		// + status pills, and surface a "resumed" hint. The user can
		// scroll up to see the prior conversation and type a new prompt
		// to continue; the LLM already has the full history in its
		// session.Messages.
		a.transcript.LoadFromMessages(a.controller.Messages())
		a.refreshBanner()
		// ResumeSnapshot overwrote Agent.ID with the loaded session-id —
		// refresh every status pill that derives from the agent so the
		// HUD reflects the rehydrated session instead of the boot one.
		a.status.SetAgentID(a.controller.AgentID())
		a.status.SetAgentName(strings.ToUpper(a.controller.ProfileName()))
		a.status.SetModel(a.controller.Model())
		a.status.SetEffort(a.controller.Effort())
		a.status.SetUsage(a.controller.Usage())
		a.status.SetContext(a.controller.LastTurnInputTokens(), status.ContextLimitFor(a.controller.Model()))
		a.state.SetHint("resumed session " + m.ID + " · type a prompt to continue")
		a.view.MarkDirty()
		a.relayout()
		return a, nil

	case overlays.ApprovalRespondedMsg:
		// The overlay already closed itself via the close=true return.
		// Nothing to do here today; reserved for future bookkeeping
		// (audit log, queue stats).
		_ = m
		return a, nil

	case overlays.QuestionRespondedMsg:
		// Same pattern as ApprovalRespondedMsg — overlay already closed itself.
		_ = m
		return a, nil

	case tea.KeyMsg:
		return a.handleKey(m)
	}

	// Fallthrough: route anything the explicit cases didn't claim to the
	// focused modal overlay. Overlays that fire their own async messages
	// (e.g. /update's updateCheckResult, updateApplyResult) depend on
	// this path to receive their own results; without it the overlay
	// stays in its initial phase forever.
	if top := a.focus.Top(); top != nil && top.Modal() {
		close, cmd := top.Update(msg)
		if close {
			a.focus.Pop()
			a.relayout()
		}
		return a, cmd
	}
	return a, nil
}

// handleAgentEvent fans one agent event through the state machine,
// the status bar, the transcript, and (on task store updates) the
// auto-fold "TASKS COMPLETE" snapshot path.
//
// The Clear that drives the auto-fold MUST run off-goroutine: each
// task deletion emits one observable.Change, which routes through
// the agent's Sink and lands as another tea.Msg. Calling Clear
// inline from Update would deadlock bubbletea v1.3.x's unbuffered
// msgs channel — the same bug v1 documented at its app.go:813-826.
// We return the Clear as a tea.Cmd so the cascade flows through
// the normal msg→Update path.
func (a *App) handleAgentEvent(e event.Event) (tea.Model, tea.Cmd) {
	prevState := a.state.Current()
	a.state.Apply(e)
	newState := a.state.Current()

	// DEBUG: trace every event → state transition so we can pinpoint
	// which event flips state away from Idle after signal-wake's
	// KindRunEnd. Remove once the signal-wake stuck-state bug is fixed.
	if a.controller != nil {
		a.controller.Logger().Debug("tui.state.apply",
			"kind", string(e.Kind),
			"agent_id", e.AgentID,
			"parent_id", e.ParentID,
			"prev", prevState.String(),
			"new", newState.String(),
			"changed", prevState != newState,
		)
	}

	// Show the thinking sprite when the agent enters the thinking
	// sub-phase; hide it when it leaves. No-op on no transition.
	if newState.IsActive() {
		a.transcript.ShowThinkingSprite()
		a.view.MarkDirty()
	} else if prevState.IsActive() {
		a.transcript.HideThinkingSprite()
		a.view.MarkDirty()
	}

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
	// Plan-mode side effects (enter / exit) and Shift+Tab cycles both
	// route through Agent.SetPermissionMode, which emits this event.
	// Sync the status bar here so tool-driven changes show up
	// immediately instead of waiting for the next user keystroke.
	if e.Kind == event.KindModeChanged && e.ModeChanged != nil {
		a.status.SetPermissionMode(e.ModeChanged.Mode)
		a.view.MarkDirty()
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
		if todos.AllCompleted(a.controller.TodoStore()) {
			width := a.transcriptWidth()
			snap := todos.RenderCompleteSnapshot(a.controller.TodoStore(), width, a.theme)
			a.transcript.AppendSynthetic(snap)
			a.view.MarkDirty()
			store := a.controller.TodoStore()
			cmd = func() tea.Msg {
				store.Clear()
				return nil
			}
		}
	}

	// Panel content may have changed (todos added/removed/completed,
	// subagents spawned/finished). Re-derive the viewport height so
	// new panels push the input/status up instead of off-screen.
	if e.Kind == event.KindStoreUpdate {
		a.relayout()
	}
	return a, cmd
}

// relayout recomputes the viewport height based on the current
// panel content. Called whenever a panel might have grown or
// shrunk: WindowSize, agent store updates. The transcript width
// itself doesn't depend on panel state, so we only adjust the
// vertical split.
//
// Layout vertical reservations:
//   - 5 rows: input box (3 textarea + 2 border)
//   - 2 rows: hint line + status bar
//   - ≥0 rows: todos panel (header + N todo rows)
//   - ≥0 rows: agents chip strip (one row per wrapped line)
func (a *App) relayout() {
	if a.width == 0 || a.height == 0 {
		return
	}
	used := 5 + 2 // input + hint+status
	if a.controller != nil {
		if panel := todos.Render(a.controller.TodoStore(), a.transcriptWidth(), a.theme); panel != "" {
			used += strings.Count(panel, "\n") + 1
		}
		if strip := agents.Render(a.controller.DaemonState(), a.transcriptWidth(), a.theme, a.state.Frame()); strip != "" {
			used += strings.Count(strip, "\n") + 1
		}
		if strip := bgtasks.Render(a.controller.DaemonState(), a.transcriptWidth(), a.theme, a.state.Frame()); strip != "" {
			used += strings.Count(strip, "\n") + 1
		}
		if strip := monitors.Render(a.controller.DaemonState(), a.transcriptWidth(), a.theme, a.state.Frame()); strip != "" {
			used += strings.Count(strip, "\n") + 1
		}
	}
	// Overlay (modal panel) and slash suggestion are mutually
	// exclusive in the layout — overlay wins. Both can shift the
	// input down by several rows, so they need to be deducted from
	// the viewport budget.
	if top := a.focus.Top(); top != nil {
		if body := top.View(a.transcriptWidth(), a.theme); body != "" {
			used += strings.Count(body, "\n") + 1
		}
	} else if a.slashVisible() {
		if body := a.slash.View(a.input.Value(), a.controller, a.transcriptWidth(), a.theme); body != "" {
			used += strings.Count(body, "\n") + 1
		}
	}
	viewportH := a.height - used
	if viewportH < 1 {
		viewportH = 1
	}
	a.view.SetSize(a.width, viewportH)
}

// transcriptWidth returns the column count panels and snapshots
// should size to. Currently identical to terminal width; future
// layout work may reserve gutter columns.
func (a *App) transcriptWidth() int {
	if a.width < 20 {
		return 20
	}
	return a.width
}

// handleRunDone fans the goroutine's exit error into the state
// machine and resets the cancel handle.
func (a *App) handleRunDone(err error) (tea.Model, tea.Cmd) {
	a.runCancel = nil
	interrupted := a.interrupted
	a.interrupted = false

	// Map the agent's interrupted error too — some providers
	// surface llm.ErrInterrupted instead of pure ctx.Cancelled.
	if errors.Is(err, llm.ErrInterrupted) {
		interrupted = true
	}
	a.state.OnRunDone(err, interrupted)
	a.transcript.HideThinkingSprite()
	return a, nil
}

// handleKey routes a key event. Precedence (each layer can return
// early; lower layers only see what the higher layers ignored):
//
//  1. top of focus stack — exclusive when modal
//  2. ctrl+c / esc — quit / cancel-run / dismiss-error / pop-overlay
//  3. ctrl+o — toggle tool fold
//  4. PgUp / PgDn / Home / End — viewport scroll
//  5. slash panel — Tab completes, Up/Down move selection, Esc dismisses
//  6. input textarea — everything else (history nav, paste, plain typing)
func (a *App) handleKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Wheel-derived arrow-key dedup. Some terminals (tmux without
	// mouse-on, certain SSH chains) emit a synthesised "up"/"down"
	// KeyMsg alongside every MouseMsg wheel event. Routing that
	// synthesised KeyMsg into the input box clobbers the user's
	// composition with a history entry. Within a short window after
	// a real wheel event, treat bare up/down as scroll instead.
	if !a.lastMouseEventAt.IsZero() && time.Since(a.lastMouseEventAt) < 80*time.Millisecond {
		switch m.String() {
		case "up", "down":
			return a, a.view.Update(m)
		}
	}

	// Layer 1: modal overlay — exclusive consumer.
	if top := a.focus.Top(); top != nil && top.Modal() {
		// Ctrl+C while an overlay is open: pop the overlay AND
		// quit (matches v1 — Ctrl+C is the universal panic
		// button).
		if m.String() == "ctrl+c" {
			a.focus.Pop()
			a.relayout()
			if a.runCancel != nil {
				a.runCancel()
			}
			return a, tea.Quit
		}
		// Scroll keys bypass the modal so the user can review the
		// transcript / panels above before deciding on the prompt.
		// The modal stays open; only the viewport behind it moves.
		switch m.String() {
		case "pgup", "pgdown", "home", "end":
			return a, a.view.Update(m)
		}
		close, cmd := top.Update(m)
		if close {
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
		// Slash panel visible? Dismiss it for this typing session
		// instead of quitting.
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
		// Open block-yank mode. Empty transcripts (pre-controller,
		// pre-banner) skip silently.
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
		// Cycle the permission stance. Takes effect immediately for the
		// next tool call; in-flight calls captured the prior mode at
		// dispatch.
		if a.controller != nil {
			next := a.controller.CyclePermissionMode()
			a.status.SetPermissionMode(next)
			a.state.SetHint("permission mode: " + next)
			a.view.MarkDirty()
		}
		return a, nil

	case "ctrl+f":
		// Open transcript search. Empty transcripts skip silently.
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

	// Layer 3: slash panel navigation. Only intercept the keys the
	// panel actually consumes — everything else flows to the input.
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
			// No movement → fall through (input handles history).
		case "down":
			if a.slash.MoveSel(a.input.Value(), catalog, +1) {
				return a, nil
			}
		}
	}

	// Layer 4: input textarea.
	cmd := a.input.Update(m)
	return a, cmd
}

// slashVisible is a thin convenience over slash.Panel.Visible that
// pre-computes the catalog and overlay state.
func (a *App) slashVisible() bool {
	overlayOpen := a.focus.Len() > 0
	return a.slash.Visible(a.input.Value(), overlayOpen, slash.Catalog(a.controller))
}

// handleSubmit dispatches a SubmitMsg from the Input.
//
//   - Slash commands:
//   - /exit, /quit       → quit
//   - /clear             → reset transcript
//   - /config            → push Config overlay
//   - /model             → push Model overlay
//   - /compact           → push Compact overlay
//   - Empty submit while iter-limit-paused: Continue without
//     appending a new user message.
//   - Empty submit otherwise: no-op.
//   - Regular text: append to transcript, start (or queue) a Run.
func (a *App) handleSubmit(m input.SubmitMsg) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.ForAgent)

	switch text {
	case "/exit", "/quit", "exit":
		a.input.Reset()
		a.slash.Reset()
		return a, tea.Quit
	case "/clear":
		a.transcript.Reset()
		a.input.Reset()
		a.slash.Reset()
		a.state.SetHint("")
		a.view.MarkDirty()
		return a, nil
	case "/config":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewConfig(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/model":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewModel(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/profile":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewProfile(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/mcp":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewMCP(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/compact":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewCompact(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/effort":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewEffort(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/resume":
		a.input.Reset()
		a.slash.Reset()
		if o := overlays.NewResume(a.controller); o != nil {
			a.focus.Push(o)
			a.relayout()
		} else {
			a.state.SetHint("no controller attached")
		}
		return a, nil
	case "/update":
		a.input.Reset()
		a.slash.Reset()
		u := overlays.NewUpdate()
		a.focus.Push(u)
		a.relayout()
		return a, u.StartCheck()
	}

	// Iter-limit takes precedence over the empty-text check: the
	// hint tells the user "press Enter to continue", and a continue
	// takes no payload.
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

	// Mid-run submit: queue the prompt; starting a second Run
	// while one is in flight 400s on every provider.
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

// startRun kicks off a Run in a goroutine and transitions the state
// machine to running.
func (a *App) startRun(prompt string) {
	if a.controller == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runCancel = cancel
	a.state.OnSubmit()
	a.transcript.ShowThinkingSprite()

	p := a.program
	go func() {
		_, err := a.controller.Run(ctx, prompt)
		if p != nil {
			p.Send(events.RunDoneMsg{Err: err})
		}
	}()
}

// startContinue resumes an iter-limit-paused run via
// controller.Continue. Same goroutine + RunDoneMsg pattern as
// startRun.
func (a *App) startContinue() {
	if a.controller == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runCancel = cancel
	a.state.OnSubmit()
	a.transcript.ShowThinkingSprite()

	p := a.program
	go func() {
		_, err := a.controller.Continue(ctx)
		if p != nil {
			p.Send(events.RunDoneMsg{Err: err})
		}
	}()
}

// View composes the rendered output. Vertical order (top → bottom),
// each layer collapses to zero height when its backing data is empty:
//
//	viewport / banner / transcript          (scrollable area)
//	todos panel                             (only when todos tracked)
//	agents chip strip                       (only when subagents tracked)
//	overlay panel                           (only when focus stack non-empty)
//	slash suggestion                        (only when "/<x>" in input)
//	input box                               (rounded border)
//	hint                                    (one-liner)
//	status bar                              (HUD)
func (a *App) View() string {
	if a.width == 0 {
		return "initializing…"
	}
	var b strings.Builder
	b.WriteString(a.view.View())

	width := a.transcriptWidth()
	if a.controller != nil {
		if panel := todos.Render(a.controller.TodoStore(), width, a.theme); panel != "" {
			b.WriteByte('\n')
			b.WriteString(panel)
		}
		if strip := agents.Render(a.controller.DaemonState(), width, a.theme, a.state.Frame()); strip != "" {
			b.WriteByte('\n')
			b.WriteString(strip)
		}
		if strip := bgtasks.Render(a.controller.DaemonState(), width, a.theme, a.state.Frame()); strip != "" {
			b.WriteByte('\n')
			b.WriteString(strip)
		}
		if strip := monitors.Render(a.controller.DaemonState(), width, a.theme, a.state.Frame()); strip != "" {
			b.WriteByte('\n')
			b.WriteString(strip)
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
	b.WriteString(a.input.View())
	b.WriteByte('\n')

	// Hint line above the status bar. The focus stack's top
	// HintProvider wins; otherwise the state-default applies.
	var hintProvider status.HintProvider
	if top := a.focus.Top(); top != nil {
		hintProvider = hintAdapter{top.Hint()}
	}
	hint := status.ResolveHint(a.state, hintProvider)
	b.WriteString(a.theme.FooterHint.Render("  " + hint))
	b.WriteByte('\n')
	b.WriteString(a.status.Compose(a.width, a.theme))
	return b.String()
}

// hintAdapter wraps a static string in status.HintProvider so the
// focus stack top can supply its Hint() to ResolveHint without
// requiring app.Focusable itself to embed HintProvider. The static
// string is captured fresh each frame from top.Hint(), so a
// state-dependent overlay (e.g. /config swapping list-mode ↔
// editor-mode) reflects accurately.
type hintAdapter struct{ s string }

func (h hintAdapter) Hint() string { return h.s }
