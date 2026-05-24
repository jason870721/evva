// Package input owns the v2 TUI's bottom textarea: prompt
// composition, paste compaction, and history navigation.
//
// The Input is a tea.Model-ish wrapper around bubbles/textarea that
// adds three behaviors not in the underlying widget:
//
//  1. Bracketed-paste compaction. Multi-line or >200-char pastes
//     are stored in an internal buffer and replaced with a compact
//     placeholder so the input box stays readable. On submit,
//     placeholders are swapped back to raw content (agent-facing)
//     or to bracketed paste chips (view-facing).
//
//  2. Prompt history. Submitted prompts are pushed to a history
//     stack; Up/Down walks back/forward through them. The user's
//     in-progress draft is preserved when nav engages and restored
//     when nav exits past the newest entry.
//
//  3. Submission. The App routes Enter through Input.Submit, which
//     returns the two prompt forms (for-agent / for-view) plus a
//     boolean indicating "this is a real submission, not an empty
//     line". The App decides what to do next — start a Run,
//     intercept a slash command, or queue mid-run.
package input

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Placeholder text shown when the input is empty. Mirrors v1.
const placeholder = "<Enter> send · <Ctrl+J> newline · <Ctrl+O> toggle tool results"

// SubmitMsg is dispatched as a tea.Cmd return when the user presses
// Enter with non-empty content. The App handles this message: routes
// slash commands, queues mid-run prompts, or starts a fresh Run.
type SubmitMsg struct {
	// ForAgent is the prompt the agent sees: raw paste content
	// inlined, no markers. Byte-for-byte what the user composed.
	ForAgent string
	// ForView is the transcript-facing form: paste blocks wrapped
	// in visible chips so the scrollback shows where each paste
	// starts and ends.
	ForView string
}

// Input is the textarea wrapper. Construct with New; mount in the
// App's layout slot. The App calls Update to feed it tea.Msg values
// and View to render it.
type Input struct {
	ta textarea.Model
	th *theme.Theme

	pasted []string // bracketed-paste buffer; expand on Submit

	history      []string // submitted prompts, oldest first
	historyIdx   int      // active idx during nav; -1 when not navigating
	historyDraft string   // user's draft saved when nav engaged
}

// New constructs an Input with v1-matching styling. The caller is
// responsible for calling SetWidth on every WindowSizeMsg and
// Focus when the input is the active focus.
func New(th *theme.Theme) *Input {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	// PromptFunc renders "> " on row 0 and two-space indent on
	// continuation rows, so multi-line composition reads as one
	// prompt rather than several.
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "> "
		}
		return "  "
	})
	ta.Focus()
	// Cursor color from the theme palette (cyan glow).
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#05D9E8"))

	return &Input{
		ta:         ta,
		th:         th,
		historyIdx: -1,
	}
}

// SetWidth updates the textarea's visible column count. Called on
// every WindowSizeMsg by the App.
func (i *Input) SetWidth(w int) {
	// Reserve 4 cols for the rounded border + padding the App's
	// layout wraps the input in (M5 will own the border render;
	// for M4 it lives on the Input itself).
	inner := w - 4
	if inner < 10 {
		inner = 10
	}
	i.ta.SetWidth(inner)
}

// Focus / Blur — Wrap the textarea's focus methods so the App's
// focus stack can drive them.
func (i *Input) Focus() tea.Cmd { return i.ta.Focus() }
func (i *Input) Blur()          { i.ta.Blur() }

// Value returns the current raw text in the textarea, paste
// placeholders included.
func (i *Input) Value() string { return i.ta.Value() }

// SetValue replaces the textarea content. Used by history nav.
func (i *Input) SetValue(s string) { i.ta.SetValue(s) }

// Reset clears the textarea and the paste buffer. Called after a
// successful submission.
func (i *Input) Reset() {
	i.ta.SetValue("")
	i.pasted = nil
	i.historyIdx = -1
	i.historyDraft = ""
}

// Empty reports whether the (trimmed) input is blank.
func (i *Input) Empty() bool {
	return strings.TrimSpace(i.ta.Value()) == ""
}

// View renders the textarea wrapped in the input border. M5 may
// pull the border out into the layout layer; for M4 it stays here.
func (i *Input) View() string {
	return i.th.InputBorder.Render(i.ta.View())
}

// Update routes a tea.Msg through the input. Returns a tea.Cmd
// that the App should chain. Special keys (Enter on non-empty,
// Up/Down for history, bracketed-paste) are consumed here; every
// other key falls through to the textarea.
//
// Returning a tea.Cmd that emits a SubmitMsg is how submission
// signals up to the App — keeps the App's Update loop the single
// place that owns "start a run" semantics.
func (i *Input) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		// Bracketed paste arrives as a KeyMsg with Paste=true and the
		// pasted runes in Runes. Handle before the key-name switch so
		// a multi-line paste doesn't get interpreted as an Enter.
		if m.Paste {
			return i.handlePaste(string(m.Runes))
		}
		switch m.String() {
		case "enter":
			// Plain Enter — submit. Multi-line composition uses
			// Ctrl+J / Alt+Enter (handled below).
			return i.submit()

		case "ctrl+j", "alt+enter":
			// Forward to textarea so it inserts a literal newline.
			i.ta.InsertString("\n")
			return nil

		case "up":
			if i.historyPrev() {
				return nil
			}
			// Fall through to textarea cursor movement.

		case "down":
			if i.historyNext() {
				return nil
			}
			// Fall through.
		}
	}

	var cmd tea.Cmd
	i.ta, cmd = i.ta.Update(msg)
	return cmd
}

// submit produces a SubmitMsg from the current input, expands
// pastes, and records the prompt in history. Empty submissions
// emit a SubmitMsg with empty ForAgent — the App decides what
// "empty submit" means (iter-limit continue, or no-op).
func (i *Input) submit() tea.Cmd {
	text := i.ta.Value()
	forAgent := expandForAgent(text, i.pasted)
	forView := expandForView(text, i.pasted, i.th)
	i.appendHistory(text)
	// NB: Reset is the App's responsibility — it might want to
	// peek at Value() before clearing (e.g. to detect a slash
	// command before the submission round-trips).
	return func() tea.Msg {
		return SubmitMsg{ForAgent: forAgent, ForView: forView}
	}
}

// appendHistory records text in the prompt history, skipping
// duplicates of the most recent entry. Resets nav state so the
// next Up starts from the newest entry.
func (i *Input) appendHistory(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if n := len(i.history); n == 0 || i.history[n-1] != text {
		i.history = append(i.history, text)
	}
	i.historyIdx = -1
	i.historyDraft = ""
}

// historyPrev walks one step back through prompt history.
// Returns true when the key was consumed; false when the caller
// should fall through to the textarea so multi-line editing keeps
// working.
//
// Entry rules: nav engages when the input is empty or nav is
// already active. With unrelated typed text and no active nav,
// Up belongs to the textarea.
func (i *Input) historyPrev() bool {
	if len(i.history) == 0 {
		return false
	}
	inNav := i.historyIdx != -1
	if !inNav && strings.TrimSpace(i.ta.Value()) != "" {
		return false
	}
	if !inNav {
		i.historyDraft = i.ta.Value()
		i.historyIdx = len(i.history) - 1
	} else if i.historyIdx > 0 {
		i.historyIdx--
	}
	i.ta.SetValue(i.history[i.historyIdx])
	return true
}

// historyNext walks one step forward. Past the newest entry it
// restores the saved draft and exits nav. Returns true only while
// nav is active.
func (i *Input) historyNext() bool {
	if i.historyIdx == -1 {
		return false
	}
	i.historyIdx++
	if i.historyIdx >= len(i.history) {
		i.historyIdx = -1
		draft := i.historyDraft
		i.historyDraft = ""
		i.ta.SetValue(draft)
		return true
	}
	i.ta.SetValue(i.history[i.historyIdx])
	return true
}

// handlePaste consumes a bracketed-paste message. Short / single-
// line pastes flow through as plain text; multi-line or large
// pastes get a compact placeholder and the content is stashed in
// pasted[] for expansion at submit.
func (i *Input) handlePaste(content string) tea.Cmd {
	if !shouldCompactPaste(content) {
		i.ta.InsertString(content)
		return nil
	}
	i.pasted = append(i.pasted, content)
	i.ta.InsertString(formatPlaceholder(len(content)))
	return nil
}

// Cursor's blink command — re-exposed so the App can include it in
// its initial tea.Batch.
func (i *Input) BlinkCmd() tea.Cmd { return textarea.Blink }

// HistoryLen reports the number of recorded prompts. Test-only.
func (i *Input) historyLen() int { return len(i.history) }
