// Package bubbletea is the reference TUI implementation of internal/ui.UI.
//
// Layout (top → bottom; all rows except the viewport collapse to zero
// height when their backing store is empty):
//
//	┌──────────────────────────────────────────────────────────────┐
//	│ banner box / transcript                                      │
//	│                                                              │
//	│  ▶ user prompt                                               │
//	│  assistant text…                                             │
//	│                                                              │
//	├──────────────────────────────────────────────────────────────┤
//	│ ▰▰ TASKS ▰▰▰▰▰▰▰▰▰▰▰▰▰…              (when non-empty)        │
//	│   ▶ wire migration                                           │
//	├──────────────────────────────────────────────────────────────┤
//	│  ‹⠹ explorer› ‹▶ writer› ‹✔ reviewer›  (when non-empty)      │  ← agents chip strip
//	├──────────────────────────────────────────────────────────────┤
//	│ > input                                                      │
//	├──────────────────────────────────────────────────────────────┤
//	│ ‹⠋ RUN› ◆ evva ◆ ▸ model ◆ in X out Y ◆ CTX ▰▰▱…▱ 12%       │  ← status bar
//	└──────────────────────────────────────────────────────────────┘
//
// Live-activity indicators (tasks, agents, status) cluster at the
// bottom of the screen as a coherent dashboard. The agents row is a
// horizontal chip strip — not a side column — so the transcript
// always spans the full terminal width.
//
// State machine: idle → running (on Enter) → idle | iter-limit |
// error. Esc cancels the in-flight run when running; idle Esc is a
// no-op. Ctrl+C quits.
//
// Newline composition: Alt+Enter (Option+Enter on macOS) and Ctrl+J
// both insert a newline. Shift+Enter portability is terminal-dependent —
// most terminals send the same byte for Shift+Enter as for Enter, but
// any terminal configured to send `\n` for Shift+Enter (common in
// iTerm2 / WezTerm / kitty profiles) routes through the Ctrl+J branch
// and gets newline behavior.
package bubbletea

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/tools/task"
	"github.com/johnny1110/evva/internal/ui"
	"github.com/johnny1110/evva/pkg/banner"
)

// defaultGreeting is the welcome line rendered inside the banner box on
// startup. Short, gestures at next action, sets the tone without being
// chatty. Callers can override per-deployment by editing this file or
// adding a future ~/.evva/greeting.txt override.
const defaultGreeting = "// neural link established — what shall we build, ʘᴥʘ?"

// pastePlaceholderRe matches the compact stand-in inserted into the
// textarea when the user pastes a multi-line or large block. The TUI
// expands these back to their stored content on submit.
var pastePlaceholderRe = regexp.MustCompile(`\[- paste total \d+ characters -\]`)

// pasteCompactThreshold is the size above which a paste gets a
// placeholder instead of being inserted verbatim. Below this users
// usually see what they pasted, which is what they want.
const pasteCompactThreshold = 200

// UI is the bubbletea implementation. Construct with New(); attach an
// agent via Attach() before calling Run().
type UI struct {
	program *tea.Program
	model   *rootModel

	mu         sync.Mutex
	controller ui.Controller
}

// New builds a UI ready to be Attached and Run. evvaHome is the user's
// config directory (typically ~/.evva); the constructor uses it to
// resolve banner.txt with an embedded fallback.
//
// Mouse reporting is intentionally NOT enabled: with it on, the terminal
// hands mouse events to the program and native text selection breaks
// (users can't drag-select to copy transcript content). The trade is
// losing mouse-wheel scroll on the viewport; PgUp/PgDown/Home/End cover
// keyboard scrolling.
func New(evvaHome string) *UI {
	u := &UI{model: newRootModel(evvaHome)}
	u.program = tea.NewProgram(u.model,
		tea.WithAltScreen(),
	)
	u.model.program = u.program
	return u
}

// Emit satisfies event.Sink. Called from the agent goroutine; forwards
// to bubbletea's main loop via Send.
func (u *UI) Emit(e event.Event) {
	if u.program == nil {
		return
	}
	u.program.Send(eventMsg{Event: e})
}

// Attach hands the UI its agent controller. Must be called before Run.
// Once the controller is known we re-render the banner with its
// metadata (agent id, model, start time) so the welcome block shows
// real session info instead of just the greeting.
func (u *UI) Attach(c ui.Controller) {
	u.mu.Lock()
	u.controller = c
	u.model.controller = c
	u.model.refreshBannerMeta()
	u.mu.Unlock()
}

// Run starts the bubbletea program and blocks until the UI exits.
// ctx cancellation triggers a clean shutdown.
func (u *UI) Run(ctx context.Context) error {
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
	program    *tea.Program

	width  int
	height int

	transcript transcript
	viewport   viewport.Model
	input      textarea.Model

	state runState
	usage llm.Usage
	// hintText holds a transient status message that overrides the
	// computed state label in the header (e.g. "interrupted").
	hintText string

	// pastedBuf holds the full content of every multi-line / large
	// paste in the current input. The textarea shows compact
	// placeholders; on submit the placeholders are replaced
	// in-order from this slice. Cleared once the prompt is sent.
	pastedBuf []string

	// promptHistory stores raw user prompts (pre-paste-expansion) in
	// submission order. Up/Down navigate it from the input box, bash
	// style. Consecutive duplicates are deduped on append.
	promptHistory []string
	// historyIdx is the active index into promptHistory while the user
	// is navigating. -1 means "not navigating; whatever is in the input
	// is the user's live draft".
	historyIdx int
	// historyDraft preserves the in-progress input the user had typed
	// when they entered history nav, so Down past the newest entry can
	// restore it.
	historyDraft string

	// spinnerFrameIdx is the current braille-dot frame for the
	// status-bar state pill and any animated subagent rows. Advances
	// on spinnerTickMsg.
	spinnerFrameIdx int

	// startedAt is the wall-clock time the UI was constructed. Treated
	// as the session start for banner-display purposes — close enough
	// to the agent's birth that the difference is invisible to the
	// user (microseconds in the same main()).
	startedAt time.Time

	runCancel context.CancelFunc
}

func newRootModel(evvaHome string) *rootModel {
	ta := textarea.New()
	ta.Placeholder = "<Enter> send · <Ctrl+J> newline · <Ctrl+O> toggle tool results"
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	// PromptFunc shows "> " only on the first row; subsequent
	// rows (when the user enters a multi-line message) get two
	// spaces of indent so the prompt isn't repeated on every line.
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "> "
		}
		return "  "
	})
	ta.Focus()
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(paletteCursor)

	vp := viewport.New(80, 20)
	vp.YPosition = 0

	t := transcript{textInflightIdx: -1, thinkingInflightIdx: -1, bannerIdx: -1}
	t.setBanner(bannerSpec{
		Art:      banner.Load(evvaHome),
		Greeting: defaultGreeting,
	})

	return &rootModel{
		input:      ta,
		viewport:   vp,
		state:      stateIdle,
		transcript: t,
		startedAt:  time.Now(),
		historyIdx: -1,
	}
}

func (m *rootModel) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, spinnerTickCmd())
}

// spinnerTickCmd schedules the next spinner advance. Returns a tea.Cmd
// the runtime fires after spinnerInterval. The Update handler returns
// another spinnerTickCmd to keep the cycle going while the program
// runs; once the program exits the goroutine ends naturally.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.handleResize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		// Bracketed paste arrives as one KeyMsg with Paste=true and
		// every pasted rune in Runes. Intercept before the regular
		// key handler so we can show a compact placeholder for
		// multi-line / large pastes.
		if msg.Paste {
			return m.handlePaste(string(msg.Runes))
		}
		return m.handleKey(msg)

	case eventMsg:
		return m.handleAgentEvent(msg.Event)

	case runDoneMsg:
		return m.handleRunDone(msg.Err)

	case spinnerTickMsg:
		m.spinnerFrameIdx++
		return m, spinnerTickCmd()

	case quitMsg:
		m.cancelRunIfAny()
		return m, tea.Quit
	}

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

// layoutSizes recomputes the viewport dimensions every time the
// terminal resizes, a panel toggles, or the input gains/loses lines.
//
// The vertical budget is:
//
//	height = transcript(N) + task-panel(K) + agent-strip(A) + input(I+2) + status(1)
//
// Each "if non-empty" row collapses to zero when its store is empty,
// returning that height to the transcript. No reserved horizontal
// column — the agents chip strip is a horizontal row, not a vertical
// panel.
func (m *rootModel) layoutSizes() {
	if m.width == 0 || m.height == 0 {
		return
	}
	bodyWidth := m.bodyWidth()

	taskHeight := lineCount(m.taskPanel(bodyWidth))
	stripHeight := lineCount(m.agentStrip(bodyWidth))
	inputHeight := m.input.Height() + 2 // +2 for border
	statusHeight := 1                   // bottom status bar (state · model · tokens · ctx)

	vpHeight := m.height - taskHeight - stripHeight - inputHeight - statusHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.viewport.Width = bodyWidth
	m.viewport.Height = vpHeight
	m.input.SetWidth(bodyWidth - 4)
	// Markdown rendering wraps to the transcript width — refresh the
	// renderer whenever the body width changes so code blocks and
	// paragraph wrap line up with the viewport.
	m.transcript.setWidth(bodyWidth - 2)
	m.refreshViewport()
}

// refreshViewport pushes the transcript content into the viewport and
// auto-scrolls to the bottom only when the user was already at the
// bottom. This is the "follow the conversation" UX: if the user has
// scrolled up to read history, new events don't yank them back.
func (m *rootModel) refreshViewport() {
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.transcript.String())
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
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
	case tea.KeyEsc:
		if m.state == stateRunning {
			m.cancelRunIfAny()
			m.hintText = "cancelling…"
			return m, nil
		}
	case tea.KeyCtrlD:
		if strings.TrimSpace(m.input.Value()) == "" {
			return m, tea.Quit
		}
	case tea.KeyCtrlO:
		// Ctrl+O toggles "expand all tool results" so long bash /
		// read_file payloads stay folded by default but are one
		// keystroke away. Folded blocks render the first 3 lines
		// plus a "+N more lines" marker; expanded blocks show in
		// full.
		m.transcript.expandTools = !m.transcript.expandTools
		m.refreshViewport()
		return m, nil
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		// Scroll keys belong to the transcript viewport, not the
		// textarea (which would treat them as cursor movement).
		// Ctrl+U / Ctrl+D stay with the textarea for line editing.
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case tea.KeyUp:
		// Up walks back through prompt history when the input is
		// empty or the user is already mid-navigation. With typed
		// text and no active nav, falls through to textarea cursor
		// movement so multi-line editing still works.
		if m.historyPrev() {
			return m, nil
		}
	case tea.KeyDown:
		// Down only consumes the key while history nav is active —
		// it walks forward and eventually restores the user's saved
		// draft. Otherwise falls through to textarea cursor.
		if m.historyNext() {
			return m, nil
		}
	case tea.KeyCtrlJ:
		// Ctrl+J is line-feed (\n). On many terminals, Shift+Enter is
		// configured (default or otherwise) to send \n while plain
		// Enter sends \r — meaning Shift+Enter arrives here. We
		// insert a newline so multi-line composition Just Works.
		m.input.InsertString("\n")
		return m, nil
	case tea.KeyEnter:
		// shift + Enter
		if msg.Alt {
			m.input.InsertString("\n")
			return m, nil
		}

		// Handle fake paste case
		if len(msg.Runes) == 1 && msg.Runes[0] == '\n' {
			m.input.InsertString("\n")
			return m, nil
		}

		return m.submit()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *rootModel) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())

	// Slash commands intercepted before the prompt reaches the agent.
	switch text {
	case "/exit", "/quit", "exit":
		return m, tea.Quit
	case "/clear":
		m.transcript = transcript{textInflightIdx: -1, thinkingInflightIdx: -1}
		m.input.SetValue("")
		m.refreshViewport()
		return m, nil
	}

	if text == "" {
		return m, nil
	}
	if m.controller == nil {
		m.hintText = "no controller attached"
		return m, nil
	}

	m.appendHistory(text)
	switch m.state {
	case stateIterLimit:
		m.input.SetValue("")
		m.pastedBuf = nil
		m.startContinue()
	default:
		// Two views of the same prompt:
		//   - forAgent: raw paste content inlined, no markers (model
		//     sees exactly what the user pasted).
		//   - forView : paste blocks wrapped in boundary markers so
		//     the user can scroll back and verify the full payload
		//     is there — head and tail clearly delimited.
		forAgent := m.expandPastes(text)
		forView := m.expandPastesForView(text)
		m.transcript.appendUserPrompt(forView)
		m.input.SetValue("")
		m.pastedBuf = nil
		m.startRun(forAgent)
	}
	m.refreshViewport()
	return m, nil
}

// appendHistory records a submitted prompt and resets nav state so the
// next Up arrow starts from the newest entry. Consecutive duplicates
// are skipped — repeatedly running the same prompt shouldn't pad the
// history with copies.
func (m *rootModel) appendHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.promptHistory); n == 0 || m.promptHistory[n-1] != text {
		m.promptHistory = append(m.promptHistory, text)
	}
	m.historyIdx = -1
	m.historyDraft = ""
}

// historyPrev walks one step back through promptHistory. Returns true
// when the key was consumed (handler should stop propagation); false
// when the caller should fall through to the textarea so multi-line
// editing keeps working.
//
// Entry rules: nav engages when the input is empty or nav is already
// active. With unrelated typed text and no active nav, Up belongs to
// the textarea.
func (m *rootModel) historyPrev() bool {
	if len(m.promptHistory) == 0 {
		return false
	}
	inNav := m.historyIdx != -1
	if !inNav && strings.TrimSpace(m.input.Value()) != "" {
		return false
	}
	if !inNav {
		m.historyDraft = m.input.Value()
		m.historyIdx = len(m.promptHistory) - 1
	} else if m.historyIdx > 0 {
		m.historyIdx--
	}
	m.input.SetValue(m.promptHistory[m.historyIdx])
	return true
}

// historyNext walks one step forward through promptHistory. Past the
// newest entry it restores the saved draft and exits nav. Returns true
// only while nav is active; outside of nav Down stays with the textarea.
func (m *rootModel) historyNext() bool {
	if m.historyIdx == -1 {
		return false
	}
	m.historyIdx++
	if m.historyIdx >= len(m.promptHistory) {
		m.historyIdx = -1
		draft := m.historyDraft
		m.historyDraft = ""
		m.input.SetValue(draft)
		return true
	}
	m.input.SetValue(m.promptHistory[m.historyIdx])
	return true
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

// handlePaste intercepts bracketed-paste input. Single-line and small
// pastes are inserted verbatim; multi-line or large pastes are stored
// in pastedBuf and replaced with a compact placeholder so the input
// box stays readable. On submit, expandPastes swaps placeholders back
// to the stored content before the prompt reaches the agent.
func (m *rootModel) handlePaste(content string) (tea.Model, tea.Cmd) {
	if !shouldCompactPaste(content) {
		m.input.InsertString(content)
		return m, nil
	}
	m.pastedBuf = append(m.pastedBuf, content)
	placeholder := fmt.Sprintf("[- paste total %d characters -]", len(content))
	m.input.InsertString(placeholder)
	return m, nil
}

// shouldCompactPaste reports whether a paste should be shown as a
// compact placeholder in the input box. Multi-line content always
// compacts; short single-line pastes pass through as plain text.
func shouldCompactPaste(s string) bool {
	if strings.ContainsRune(s, '\n') {
		return true
	}
	return len(s) > pasteCompactThreshold
}

// expandPastes walks the input text in order and replaces each compact
// placeholder with the corresponding stored paste content. Extra
// placeholders past the buffer length stay literal (defensive); extra
// stored pastes past the placeholder count are dropped (the user
// deleted them from the input).
//
// This is the agent-facing expansion: raw content only, no boundary
// markers, no chrome. The model should see exactly what the user
// pasted, byte-for-byte.
func (m *rootModel) expandPastes(text string) string {
	if len(m.pastedBuf) == 0 {
		return text
	}
	i := 0
	return pastePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		if i >= len(m.pastedBuf) {
			return match
		}
		s := m.pastedBuf[i]
		i++
		return s
	})
}

// expandPastesForView is the transcript-facing expansion: paste content
// is sandwiched between visible head/tail markers so the user can scroll
// the scrollback and confirm the whole payload made it in. Without the
// markers a long paste blends into surrounding typed prose and the user
// has no anchor for "where does the paste end".
func (m *rootModel) expandPastesForView(text string) string {
	if len(m.pastedBuf) == 0 {
		return text
	}
	i := 0
	return pastePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		if i >= len(m.pastedBuf) {
			return match
		}
		s := m.pastedBuf[i]
		i++
		head := styles.PasteChip.Render(fmt.Sprintf("╔═ PASTE %d chars ═╗", len(s)))
		tail := styles.PasteChip.Render("╚════════════════════╝")
		return "\n" + head + "\n" + s + "\n" + tail + "\n"
	})
}

func (m *rootModel) handleAgentEvent(e event.Event) (tea.Model, tea.Cmd) {
	if e.Kind == event.KindUsage && e.Usage != nil {
		m.usage = e.Usage.Cumulative
	}
	m.updateStateFromEvent(e)
	if m.transcript.foldEvent(e) {
		// noop — state captured above
	}
	// Task auto-fold: every time a task store update arrives, check
	// if every task is completed. If so, fold the snapshot into the
	// transcript as a green block and clear the live store so the
	// panel collapses. Agent-owned data — no user action required.
	if e.Kind == event.KindStoreUpdate && e.StoreUpdate != nil && e.StoreUpdate.Domain == task.Domain {
		if m.controller != nil && AllTasksCompleted(m.controller.ToolState()) {
			snap := renderTasksCompleteSnapshot(m.controller.ToolState(), m.bodyWidth())
			m.transcript.appendBlock(snap)
			m.controller.ToolState().TaskStore().Clear()
		}
	}
	// Layout may need to recompute (panel appearing / disappearing).
	m.layoutSizes()
	return m, nil
}

// updateStateFromEvent advances m.state in response to one agent event.
// Maps the agent's lifecycle vocabulary onto the UI's coarser runState
// enum so the status pill always reflects what the agent is doing right
// now — reasoning, emitting content, calling a tool, draining, or
// compacting. Terminal/error states are handled separately in
// handleRunDone; this function only deals with mid-run transitions.
//
// Precedence note: stateError and stateIterLimit, once set, are
// "sticky" until the next prompt or Continue — we don't let a stray
// turn-end event overwrite them back to running.
func (m *rootModel) updateStateFromEvent(e event.Event) {
	if m.state == stateError || m.state == stateIterLimit {
		return
	}
	switch e.Kind {
	case event.KindRunStart, event.KindRunResume, event.KindTurnStart:
		m.state = stateRunning
	case event.KindThinking, event.KindThinkingChunk:
		m.state = stateThinking
	case event.KindText, event.KindTextChunk:
		m.state = stateTexting
	case event.KindToolUseStart:
		m.state = stateExecuting
	case event.KindToolUseResult:
		// Result arriving means this tool's call is done. Drop back
		// to a generic running state so the pill doesn't lie about
		// continued execution; the next tool call (or text emit) will
		// move us forward again.
		m.state = stateRunning
	case event.KindDrainingInfo:
		m.state = stateDraining
	case event.KindCompacting:
		m.state = stateCompacting
	case event.KindTurnEnd:
		// Between iterations: the agent decides whether to spin
		// another loop. Show generic running until the next sub-phase
		// event lands, instead of leaving the previous sub-phase
		// label stale.
		m.state = stateRunning
	}
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
		// Match the agent's iter-limit sentinel without importing the
		// agent package (TUI is an internal/ui consumer).
		if strings.Contains(err.Error(), "iteration limit") {
			m.state = stateIterLimit
			m.hintText = "press Enter to continue, Ctrl+C to quit"
		} else {
			m.state = stateError
			m.hintText = "error: " + truncate(err.Error(), 120)
		}
	}
	m.layoutSizes()
	return m, nil
}

// View renders the full screen. Vertical order (top → bottom):
//
//	viewport / banner / transcript    (scrollable area)
//	tasks panel                       (only when tasks tracked)
//	agents chip strip                 (only when subagents tracked)
//	input box                         (rounded border)
//	status bar                        (HUD)
//
// All live-activity indicators (tasks, agents, status) cluster at the
// bottom of the screen as a coherent dashboard. Each row collapses to
// zero height when its underlying store is empty, so the layout stays
// quiet during single-turn chats.
func (m *rootModel) View() string {
	if m.width == 0 {
		return "initializing…"
	}

	width := m.width

	sections := []string{m.viewport.View()}
	if tp := m.taskPanel(width); tp != "" {
		sections = append(sections, tp)
	}
	if strip := m.agentStrip(width); strip != "" {
		sections = append(sections, strip)
	}
	sections = append(sections, styles.InputBorder.Render(m.input.View()))

	status := renderStatusBar(statusBarInput{
		Width:        m.width,
		Model:        m.modelName(),
		Usage:        m.usage,
		State:        m.state,
		Frame:        m.spinnerFrameIdx,
		ContextUsed:  m.contextUsed(),
		ContextLimit: contextLimitFor(m.modelName()),
	})
	sections = append(sections, status)

	return strings.Join(sections, "\n")
}

// agentStrip returns the horizontal HUD chip strip for tracked
// subagents, or "" when none are tracked. Width is the full terminal
// column count — the strip wraps to additional rows if there are too
// many agents to fit on one line.
func (m *rootModel) agentStrip(width int) string {
	if m.controller == nil {
		return ""
	}
	return renderAgentStrip(m.controller.ToolState(), width, m.spinnerFrameIdx)
}

// refreshBannerMeta repopulates the welcome banner with the controller's
// current metadata. Called from Attach (once, right after the agent is
// wired) and safe to invoke later if any of the underlying values ever
// becomes mutable.
func (m *rootModel) refreshBannerMeta() {
	if m.controller == nil {
		return
	}
	id := m.controller.AgentID()
	if len(id) > 8 {
		id = id[:8]
	}
	rows := []bannerInfoRow{
		{Label: "agent", Value: id},
		{Label: "model", Value: m.modelName()},
		{Label: "started", Value: m.startedAt.Format("2006-01-02 15:04:05")},
	}
	m.transcript.setBanner(bannerSpec{
		Art:      m.transcript.banner.Art,
		Greeting: m.transcript.banner.Greeting,
		Info:     rows,
	})
}

// contextUsed reports total tokens consumed in the session — the sum
// of cumulative input + output reported via KindUsage. Divided by the
// model's context window in renderContextBar this gives a meaningful
// "session burn" indicator that moves on every turn instead of
// silently sitting at 0% when individual prompts are small.
//
// Using cumulative (rather than the last turn's prompt size) means the
// bar can exceed 100% on long, compaction-heavy sessions — that is
// surfaced by clamping in renderContextBar; the user still sees the
// bar pinned at 100% which is the right signal ("you've spent your
// budget").
func (m *rootModel) contextUsed() int {
	return m.usage.InputTokens + m.usage.OutputTokens
}

// bodyWidth returns the column width usable by transcript / panels /
// input. Now identical to the terminal width — there's no longer a
// reserved side column, since the agents chip strip lives horizontally
// below the transcript instead of in a column to its side.
func (m *rootModel) bodyWidth() int {
	if m.width < 20 {
		return 20
	}
	return m.width
}

func (m *rootModel) taskPanel(width int) string {
	if m.controller == nil {
		return ""
	}
	return renderTaskPanel(m.controller.ToolState(), width)
}

func (m *rootModel) modelName() string {
	if m.controller != nil {
		if name := m.controller.Model(); name != "" {
			return name
		}
	}
	return "-"
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
