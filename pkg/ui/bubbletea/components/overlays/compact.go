// Package overlays implements the modal panels the App pushes onto
// its focus stack: /config (form), /model (picker), /compact
// (chooser). M9 adds a search overlay; M10 adds the permission
// overlay. They all satisfy app.Focusable structurally — no
// dependency on the app package.
package overlays

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// CompactChoice is one row in the chooser.
type CompactChoice struct {
	Kind  string // "micro" | "full" — value passed to Controller.Compact
	Label string
	Desc  string
}

// compactChoices is the canonical option list. Micro first because
// it's instant and cheap; Full sits below because the LLM call is
// more expensive.
var compactChoices = []CompactChoice{
	{Kind: "micro", Label: "Micro", Desc: "elide older tool results · instant, no LLM call"},
	{Kind: "full", Label: "Full", Desc: "ask the LLM to summarize the conversation · ~5s, replaces history with a brief"},
}

// CompactDoneMsg signals the user-facing outcome of a manual
// compact. The chooser is already popped by the time this lands;
// the only effect is the status state machine surfacing the error
// (if any) as a hint.
type CompactDoneMsg struct {
	Err error
}

// Compact is the /compact chooser. Two rows; Up/Down moves
// selection; Enter dispatches Controller.Compact in a tea.Cmd and
// closes; Esc cancels.
type Compact struct {
	ctrl    ui.Controller
	choices []CompactChoice
	sel     int
	errMsg  string
}

// NewCompact opens the chooser. Returns nil when ctrl is nil — the
// App turns that into a hint ("no controller attached") instead of
// pushing an empty overlay.
func NewCompact(ctrl ui.Controller) *Compact {
	if ctrl == nil {
		return nil
	}
	return &Compact{ctrl: ctrl, choices: compactChoices}
}

func (c *Compact) Key() string  { return "compact" }
func (c *Compact) Modal() bool  { return true }
func (c *Compact) Hint() string { return "[↑↓] navigate · [Enter] confirm · [Esc] cancel" }

// Update consumes keys while on top of the focus stack.
//
// Returns close=true when Esc is pressed or Enter dispatches the
// chosen compaction. The dispatch happens as a tea.Cmd so the
// chooser closes immediately and the transcript can paint the
// animated `<spinner> Compacting…` block driven by the agent's
// KindCompacting / KindCompactingEnd events.
func (c *Compact) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc":
		return true, nil
	case "ctrl+c":
		// Defer the quit decision to App.handleKey — let the user
		// see the chooser disappear first, then the global
		// quit-while-running path kicks in.
		return true, nil
	case "up", "k":
		if c.sel > 0 {
			c.sel--
			c.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if c.sel < len(c.choices)-1 {
			c.sel++
			c.errMsg = ""
		}
		return false, nil
	case "enter":
		choice := c.choices[c.sel]
		ctrl := c.ctrl
		return true, func() tea.Msg {
			if err := ctrl.Compact(context.Background(), choice.Kind); err != nil {
				return CompactDoneMsg{Err: err}
			}
			return CompactDoneMsg{}
		}
	}
	return false, nil
}

// View renders the chooser. width is the available column count;
// the panel uses an inset margin so the bordered box sits inside
// the layout slot.
func (c *Compact) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /COMPACT"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"Compaction reshapes the conversation to free context room. " +
			"Micro is local and instant; Full asks the LLM for a summary brief.",
	))
	b.WriteString("\n\n")

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	for i, choice := range c.choices {
		marker := "  "
		style := dim
		if i == c.sel {
			marker = "▶ "
			style = sel
		}
		row := fmt.Sprintf("%s%-6s  %s", marker, choice.Label, choice.Desc)
		b.WriteString(style.Render(row))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if c.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + c.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[↑↓] navigate · [Enter] confirm · [Esc] cancel"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// extractFg pulls a lipgloss.Color from a style for cursor / accent
// rendering. Falls back to muted grey on NoColor so the overlay
// never renders invisible.
func extractFg(s lipgloss.Style) lipgloss.Color {
	if c, ok := s.GetForeground().(lipgloss.Color); ok {
		return c
	}
	return lipgloss.Color("#7A7E94")
}
