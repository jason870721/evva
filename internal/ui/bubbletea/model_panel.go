package bubbletea

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// pendingModel is the /model picker's in-flight state. Lists every
// (provider, model) pair the binary knows about and lets the user pick
// one. On Enter: rebuilds the agent's llm.Client, clears history, and
// persists the choice as the new default for next launch.
type pendingModel struct {
	choices  []modelChoice
	selected int
	// errMsg surfaces a swap failure (e.g. unknown provider, build error,
	// run-in-progress). Cleared on next navigation.
	errMsg string
}

// modelChoice is one row in the picker. provider + model are the typed
// constants the swap handler hands to Controller.SwitchLLM. The string
// fields are precomputed for the renderer.
type modelChoice struct {
	provider constant.LLMProvider
	model    constant.Model
	label    string
}

// buildModelChoices flattens every provider's Models list into a single
// linear menu and marks the current selection so the cursor opens on
// it. Order: providers as listed by constant.GetAllProviders, models in
// the order the provider declares them (ascending cost).
func buildModelChoices(currentProvider string, currentModel constant.Model) ([]modelChoice, int) {
	out := []modelChoice{}
	cursor := 0
	for _, p := range constant.GetAllProviders() {
		for _, m := range p.Models {
			label := fmt.Sprintf("%s / %s", p.Name, string(m))
			if p.Name == currentProvider && m == currentModel {
				label += "  (current)"
				cursor = len(out)
			}
			out = append(out, modelChoice{provider: p, model: m, label: label})
		}
	}
	return out, cursor
}

// openModelPicker pushes the picker into the pendingModel slot. The
// cursor starts on the currently-active model so a quick Esc cancels
// without changing anything.
func (m *rootModel) openModelPicker() {
	if m.controller == nil {
		m.hintText = "no controller attached"
		return
	}
	cfg := config.Get()
	choices, cursor := buildModelChoices(cfg.DefaultProvider.Name, cfg.DefaultModel)
	m.pendingModel = &pendingModel{choices: choices, selected: cursor}
}

func (m *rootModel) closeModelPicker() {
	m.pendingModel = nil
	m.layoutSizes()
}

// applyModelChoice performs the swap: rebuild llm.Client via
// Controller.SwitchLLM (which also clears session), reset the
// transcript and usage to match, persist the new defaults to YAML, and
// announce the change to the user.
func (m *rootModel) applyModelChoice(c modelChoice) error {
	if m.state.isActive() {
		return fmt.Errorf("can't switch model while a run is in flight — press Esc to cancel first")
	}
	if err := m.controller.SwitchLLM(c.provider, c.model); err != nil {
		return err
	}
	if err := config.Get().SetDefaultModel(c.provider, c.model); err != nil {
		return err
	}

	// Session was cleared on the agent side; mirror that in the UI so
	// the user doesn't see history that no longer matches the agent.
	// transcript.reset preserves the banner — refreshBannerMeta below
	// re-renders the "model" row so the status block reflects the swap
	// immediately, not only after the next message.
	m.transcript.reset()
	m.usage = llm.Usage{}
	m.refreshBannerMeta()
	m.hintText = fmt.Sprintf("switched to %s / %s · history cleared", c.provider.Name, string(c.model))
	m.refreshViewport()
	return nil
}

func (m *rootModel) handleModelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pm := m.pendingModel
	if msg.Type == tea.KeyCtrlC {
		m.closeModelPicker()
		m.cancelRunIfAny()
		return m, tea.Quit
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.closeModelPicker()
		return m, nil
	case tea.KeyUp:
		if pm.selected > 0 {
			pm.selected--
			pm.errMsg = ""
		}
		return m, nil
	case tea.KeyDown:
		if pm.selected < len(pm.choices)-1 {
			pm.selected++
			pm.errMsg = ""
		}
		return m, nil
	case tea.KeyEnter:
		if err := m.applyModelChoice(pm.choices[pm.selected]); err != nil {
			pm.errMsg = err.Error()
			return m, nil
		}
		m.closeModelPicker()
		return m, nil
	}
	return m, nil
}

func (m *rootModel) modelPanel(width int) string {
	if m.pendingModel == nil {
		return ""
	}
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}
	return styles.InputBorder.Render(m.renderModelList(innerWidth))
}

func (m *rootModel) renderModelList(innerWidth int) string {
	pm := m.pendingModel

	var b strings.Builder
	b.WriteString(styles.PanelHeader.Render("▰ /MODEL"))
	b.WriteByte('\n')
	b.WriteString(styles.DimText.Render(
		"Swapping clears the conversation — provider-specific state (thinking signatures) can't carry across providers.",
	))
	b.WriteString("\n\n")

	sel := lipgloss.NewStyle().Foreground(paletteCyan).Bold(true)
	dim := styles.DimText
	for i, c := range pm.choices {
		marker := "  "
		style := dim
		if i == pm.selected {
			marker = "▶ "
			style = sel
		}
		b.WriteString(style.Render(marker + c.label))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if pm.errMsg != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(paletteMagenta).Render("✗ " + pm.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(styles.FooterHint.Render("[↑↓] navigate · [Enter] switch · [Esc] cancel"))
	return strings.TrimRight(b.String(), "\n")
}
