package app

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// placeholder shown when the input is empty — doubles as the key hint so
// lp needs no separate help line for the common keys.
const placeholder = "enter send · ctrl+j newline · ctrl+o fold · shift+tab mode"

// SubmitMsg is dispatched when the user presses Enter on non-empty input.
// lp keeps a single text form (no paste-chip compaction in v1), so ForAgent
// and ForView are identical — the two-field shape mirrors the bubbletea
// input so the root handler reads the same.
type SubmitMsg struct {
	ForAgent string
	ForView  string
}

// Input is a thin bubbles/textarea wrapper owning lp's command-line look: a
// gold ❯ prompt, a gold cursor, a single slim row, and prompt history. The
// underline frame is applied by the theme's InputBorder at View time.
type Input struct {
	ta textarea.Model
	th *theme.Theme

	history      []string
	historyIdx   int
	historyDraft string
}

// NewInput builds the input. Gold is read off the theme (UserPrompt's
// foreground) so the palette stays in one place — lp/theme.go.
func NewInput(th *theme.Theme) *Input {
	goldFg := th.UserPrompt.GetForeground()
	prompt := lipgloss.NewStyle().Foreground(goldFg).Bold(true)

	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return prompt.Render("❯ ")
		}
		return "  "
	})
	ta.Focus()
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(goldFg)

	return &Input{ta: ta, th: th, historyIdx: -1}
}

// SetWidth updates the textarea's visible column count, reserving columns
// for the underline frame's padding.
func (i *Input) SetWidth(w int) {
	i.ta.SetWidth(max(w-4, 10))
}

// Value returns the current raw text.
func (i *Input) Value() string { return i.ta.Value() }

// SetValue replaces the textarea content (used by history nav).
func (i *Input) SetValue(s string) { i.ta.SetValue(s) }

// Reset clears the textarea and history-nav state.
func (i *Input) Reset() {
	i.ta.SetValue("")
	i.historyIdx = -1
	i.historyDraft = ""
}

// View renders the textarea wrapped in the theme's underline frame.
func (i *Input) View() string {
	return i.th.InputBorder.Render(i.ta.View())
}

// BlinkCmd re-exposes the cursor blink command for the App's initial batch.
func (i *Input) BlinkCmd() tea.Cmd { return textarea.Blink }

// Update routes a tea.Msg through the input. Enter submits, Ctrl+J inserts a
// newline, Up/Down walk prompt history; everything else falls through to the
// textarea.
func (i *Input) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(tea.KeyMsg); ok {
		switch m.String() {
		case "enter":
			return i.submit()
		case "ctrl+j", "alt+enter":
			i.ta.InsertString("\n")
			return nil
		case "up":
			if i.historyPrev() {
				return nil
			}
		case "down":
			if i.historyNext() {
				return nil
			}
		}
	}
	var cmd tea.Cmd
	i.ta, cmd = i.ta.Update(msg)
	return cmd
}

// submit produces a SubmitMsg from the current input and records history.
// Reset is the App's responsibility (it peeks Value() to detect slash
// commands before clearing).
func (i *Input) submit() tea.Cmd {
	text := i.ta.Value()
	i.appendHistory(text)
	return func() tea.Msg {
		return SubmitMsg{ForAgent: text, ForView: text}
	}
}

// appendHistory records text, skipping consecutive duplicates and resetting
// nav state.
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

// historyPrev walks one step back. Returns true when the key was consumed.
// Nav engages only when the input is empty or nav is already active, so
// Up still moves the cursor while editing a multi-line draft.
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

// historyNext walks one step forward; past the newest entry it restores the
// saved draft and exits nav.
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
