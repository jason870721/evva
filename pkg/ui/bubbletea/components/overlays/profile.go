package overlays

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// ProfileSwitchedMsg signals a successful persona swap. The App handles
// it by resetting the transcript, refreshing the banner, and pushing
// the new persona's name into the status bar. Failures stay inside the
// overlay (errMsg) rather than routing here.
type ProfileSwitchedMsg struct {
	Name string
}

// Profile is the /profile picker overlay. Lists every persona in the
// agent registry whose `as:` includes "main" (built-in evva, plus any
// disk-loaded persona under <EVVA_HOME>/agents/{name}/).
type Profile struct {
	ctrl    ui.Controller
	choices []ui.ProfileChoice
	sel     int
	errMsg  string
}

// NewProfile opens the picker. Builds the choices list from
// Controller.ListMainProfiles and pre-positions the cursor on the
// currently-active persona so a quick Esc cancels nothing.
func NewProfile(ctrl ui.Controller) *Profile {
	if ctrl == nil {
		return nil
	}
	choices := ctrl.ListMainProfiles()
	cursor := 0
	current := ctrl.ProfileName()
	for i, c := range choices {
		if c.Name == current {
			cursor = i
			break
		}
	}
	return &Profile{ctrl: ctrl, choices: choices, sel: cursor}
}

func (p *Profile) Key() string  { return "profile" }
func (p *Profile) Modal() bool  { return true }
func (p *Profile) Hint() string { return "[↑↓] navigate · [Enter] switch · [Esc] cancel" }

// Update consumes keys while on top of the focus stack. Enter swaps
// the persona via Controller.SwitchProfile; on success returns
// close=true and emits ProfileSwitchedMsg so the App can reset the
// transcript and refresh the status bar. On failure the error stays in
// errMsg and the picker remains open.
func (p *Profile) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return true, nil
	case "up", "k":
		if p.sel > 0 {
			p.sel--
			p.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if p.sel < len(p.choices)-1 {
			p.sel++
			p.errMsg = ""
		}
		return false, nil
	case "enter":
		if len(p.choices) == 0 {
			return true, nil
		}
		choice := p.choices[p.sel]
		if err := p.ctrl.SwitchProfile(choice.Name); err != nil {
			p.errMsg = err.Error()
			return false, nil
		}
		return true, func() tea.Msg {
			return ProfileSwitchedMsg{Name: choice.Name}
		}
	}
	return false, nil
}

func (p *Profile) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /PROFILE"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"Switching clears the conversation — each persona has its own " +
			"system prompt and tool surface, so history can't carry across.",
	))
	b.WriteString("\n\n")

	if len(p.choices) == 0 {
		b.WriteString(th.DimText.Render("  (no main personas registered)"))
		b.WriteByte('\n')
	}

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	current := p.ctrl.ProfileName()
	for i, c := range p.choices {
		marker := "  "
		style := dim
		if i == p.sel {
			marker = "▶ "
			style = sel
		}
		label := c.Name
		if c.Name == current {
			label += "  (current)"
		}
		if hint := strings.TrimSpace(c.WhenToUse); hint != "" {
			label += "  — " + truncateWhenToUse(hint, 50)
		}
		b.WriteString(style.Render(marker + label))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if p.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + p.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[↑↓] navigate · [Enter] switch · [Esc] cancel"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// truncateWhenToUse caps a persona's "when to use" blurb so the /profile
// picker stays single-line per row. Rune-aware so multibyte input doesn't
// chop mid-character.
func truncateWhenToUse(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
