package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/internal/ui"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
)

// ModelChoice is one row in the picker.
type ModelChoice struct {
	Provider constant.LLMProvider
	Model    constant.Model
	Label    string
}

// ModelSwitchedMsg signals a successful swap. The App handles it
// by resetting the transcript + clearing usage + re-rendering the
// banner so the new (provider, model) shows up on the next frame.
// Failures stay inside the overlay (errMsg) instead of routing
// here.
type ModelSwitchedMsg struct {
	Provider constant.LLMProvider
	Model    constant.Model
}

// Model is the /model picker. Lists every (provider, model) the
// binary knows about, cursor pre-positioned on the active one.
type Model struct {
	ctrl    ui.Controller
	choices []ModelChoice
	sel     int
	errMsg  string
}

// NewModel opens the picker, building the choices list from
// constant.GetAllProviders and marking the currently-active model
// so the cursor opens on it (a quick Esc cancels nothing).
func NewModel(ctrl ui.Controller) *Model {
	if ctrl == nil {
		return nil
	}
	cfg := config.Get()
	choices, cursor := buildModelChoices(cfg.DefaultProvider.Name, cfg.DefaultModel)
	return &Model{ctrl: ctrl, choices: choices, sel: cursor}
}

func buildModelChoices(currentProvider string, currentModel constant.Model) ([]ModelChoice, int) {
	out := []ModelChoice{}
	cursor := 0
	for _, p := range constant.GetAllProviders() {
		for _, m := range p.Models {
			label := fmt.Sprintf("%s / %s", p.Name, string(m))
			if p.Name == currentProvider && m == currentModel {
				label += "  (current)"
				cursor = len(out)
			}
			out = append(out, ModelChoice{Provider: p, Model: m, Label: label})
		}
	}
	return out, cursor
}

func (m *Model) Key() string  { return "model" }
func (m *Model) Modal() bool  { return true }
func (m *Model) Hint() string { return "[↑↓] navigate · [Enter] switch · [Esc] cancel" }

// Update consumes keys while on top of the focus stack. Enter
// performs the swap via Controller.SwitchLLM + config.SetDefaultModel;
// on success returns close=true and emits ModelSwitchedMsg so the
// App can reset the transcript. On failure the error stays in
// errMsg and the picker remains open.
func (m *Model) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc":
		return true, nil
	case "ctrl+c":
		return true, nil
	case "up", "k":
		if m.sel > 0 {
			m.sel--
			m.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if m.sel < len(m.choices)-1 {
			m.sel++
			m.errMsg = ""
		}
		return false, nil
	case "enter":
		choice := m.choices[m.sel]
		if err := m.apply(choice); err != nil {
			m.errMsg = err.Error()
			return false, nil
		}
		return true, func() tea.Msg {
			return ModelSwitchedMsg{Provider: choice.Provider, Model: choice.Model}
		}
	}
	return false, nil
}

// apply performs the actual swap: ask the controller to rebuild
// the llm.Client (which clears the agent's session) and persist
// the new defaults. The App handles the UI-side mirror (transcript
// reset, banner refresh) via the ModelSwitchedMsg.
func (m *Model) apply(c ModelChoice) error {
	if err := m.ctrl.SwitchLLM(c.Provider, c.Model); err != nil {
		return err
	}
	if err := config.Get().SetDefaultModel(c.Provider, c.Model); err != nil {
		return err
	}
	return nil
}

func (m *Model) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /MODEL"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"Swapping clears the conversation — provider-specific state " +
			"(thinking signatures) can't carry across providers.",
	))
	b.WriteString("\n\n")

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	for i, choice := range m.choices {
		marker := "  "
		style := dim
		if i == m.sel {
			marker = "▶ "
			style = sel
		}
		b.WriteString(style.Render(marker + choice.Label))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if m.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + m.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[↑↓] navigate · [Enter] switch · [Esc] cancel"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}
