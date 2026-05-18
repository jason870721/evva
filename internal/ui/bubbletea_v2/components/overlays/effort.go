package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/ui"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
)

// EffortChoice is one row in the effort picker.
type EffortChoice struct {
	Level string
	Label string
	Desc  string
}

// EffortSwitchedMsg signals a successful effort change.
type EffortSwitchedMsg struct {
	Level string
}

// Effort is the /effort picker. Lists the 4 effort levels with the
// currently-active one marked and cursored.
type Effort struct {
	ctrl    ui.Controller
	choices []EffortChoice
	sel     int
	errMsg  string
}

var effortChoiceDefs = []EffortChoice{
	{Level: "low", Label: "Low", Desc: "fastest response, minimal reasoning"},
	{Level: "medium", Label: "Medium", Desc: "moderate reasoning (default)"},
	{Level: "high", Label: "High", Desc: "substantial reasoning"},
	{Level: "ultra", Label: "Ultra", Desc: "deepest reasoning, slower responses"},
}

// NewEffort builds the picker, cursoring on the currently-active level.
func NewEffort(ctrl ui.Controller) *Effort {
	if ctrl == nil {
		return nil
	}
	choices := make([]EffortChoice, len(effortChoiceDefs))
	copy(choices, effortChoiceDefs)
	current := ctrl.Effort()
	cursor := 0
	for i, c := range choices {
		if c.Level == current {
			cursor = i
			break
		}
	}
	return &Effort{ctrl: ctrl, choices: choices, sel: cursor}
}

func (e *Effort) Key() string  { return "effort" }
func (e *Effort) Modal() bool  { return true }
func (e *Effort) Hint() string { return "[↑↓] navigate · [Enter] apply · [Esc] cancel" }

func (e *Effort) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return true, nil
	case "up", "k":
		if e.sel > 0 {
			e.sel--
			e.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if e.sel < len(e.choices)-1 {
			e.sel++
			e.errMsg = ""
		}
		return false, nil
	case "enter":
		choice := e.choices[e.sel]
		if err := e.ctrl.SetEffort(choice.Level); err != nil {
			e.errMsg = err.Error()
			return false, nil
		}
		return true, func() tea.Msg {
			return EffortSwitchedMsg{Level: choice.Level}
		}
	}
	return false, nil
}

func (e *Effort) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}
	_ = innerWidth // reserved for future longer descriptions

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /EFFORT"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"Controls how much reasoning the model spends before replying. " +
			"Each provider maps effort to its own parameters.",
	))
	b.WriteString("\n\n")

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	for i, choice := range e.choices {
		marker := "  "
		style := dim
		if i == e.sel {
			marker = "▶ "
			style = sel
		}
		line := fmt.Sprintf("%s%s  — %s", marker, choice.Label, choice.Desc)
		if choice.Level == e.ctrl.Effort() {
			line += "  (current)"
		}
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if e.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + e.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[↑↓] navigate · [Enter] apply · [Esc] cancel"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}
