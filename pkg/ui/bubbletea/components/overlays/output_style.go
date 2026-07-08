package overlays

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// OutputStyleSwitchedMsg signals a successful output-style swap. The App
// handles it like ProfileSwitchedMsg minus the persona-label change: the
// controller rebuilt the profile (fresh system prompt + session), so the
// transcript resets. Failures stay inside the overlay (errMsg).
type OutputStyleSwitchedMsg struct {
	Name string
}

// OutputStyle is the /output-style picker overlay. Lists the built-in
// styles (default, Explanatory, Learning) plus any custom
// output-styles/*.md from the user and project tiers.
type OutputStyle struct {
	ctrl    ui.Controller
	choices []ui.OutputStyleChoice
	sel     int
	errMsg  string
}

// NewOutputStyle opens the picker with the cursor on the active style so a
// quick Esc cancels nothing. Mirrors NewProfile.
func NewOutputStyle(ctrl ui.Controller) *OutputStyle {
	if ctrl == nil {
		return nil
	}
	choices := ctrl.ListOutputStyles()
	cursor := 0
	current := ctrl.OutputStyleName()
	for i, c := range choices {
		if c.Name == current {
			cursor = i
			break
		}
	}
	return &OutputStyle{ctrl: ctrl, choices: choices, sel: cursor}
}

func (o *OutputStyle) Key() string  { return "output-style" }
func (o *OutputStyle) Modal() bool  { return true }
func (o *OutputStyle) Hint() string { return "[↑↓] navigate · [Enter] switch · [Esc] cancel" }

// Update consumes keys while on top of the focus stack. Enter applies the
// style via Controller.SwitchOutputStyle; on success returns close=true
// and emits OutputStyleSwitchedMsg so the App can reset the transcript.
func (o *OutputStyle) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return true, nil
	case "up", "k":
		if o.sel > 0 {
			o.sel--
			o.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if o.sel < len(o.choices)-1 {
			o.sel++
			o.errMsg = ""
		}
		return false, nil
	case "enter":
		if len(o.choices) == 0 {
			return true, nil
		}
		choice := o.choices[o.sel]
		if err := o.ctrl.SwitchOutputStyle(choice.Name); err != nil {
			o.errMsg = err.Error()
			return false, nil
		}
		return true, func() tea.Msg {
			return OutputStyleSwitchedMsg{Name: choice.Name}
		}
	}
	return false, nil
}

func (o *OutputStyle) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /OUTPUT-STYLE"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"A style overlays how the active persona talks. Switching rebuilds " +
			"the system prompt, so the conversation clears. Custom styles: " +
			"output-styles/*.md under EVVA_HOME or <workdir>/.evva.",
	))
	b.WriteString("\n\n")

	if len(o.choices) == 0 {
		b.WriteString(th.DimText.Render("  (no output styles available)"))
		b.WriteByte('\n')
	}

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	current := o.ctrl.OutputStyleName()
	for i, c := range o.choices {
		marker := "  "
		style := dim
		if i == o.sel {
			marker = "▶ "
			style = sel
		}
		label := c.Name
		if c.Source != "" && c.Source != "built-in" {
			label += " [" + c.Source + "]"
		}
		if c.Name == current {
			label += "  (current)"
		}
		if hint := strings.TrimSpace(c.Description); hint != "" {
			label += "  — " + truncateWhenToUse(hint, 50)
		}
		b.WriteString(style.Render(marker + label))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if o.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + o.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[↑↓] navigate · [Enter] switch · [Esc] cancel"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}
