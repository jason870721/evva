package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// MCP is the /mcp panel: a read-only list of every configured MCP server
// and its live connection status. It has no actions — Esc/Enter close it,
// up/down move a cosmetic highlight. Mirrors the read-only shape of the
// effort picker; the data is a one-shot snapshot taken at construction.
type MCP struct {
	servers []ui.MCPServerInfo
	sel     int
}

// NewMCP snapshots the controller's MCP server list. ctrl may be nil
// (pre-Attach) in which case it returns nil so the App can hint "no
// controller attached" instead of opening an empty overlay.
func NewMCP(ctrl ui.Controller) *MCP {
	if ctrl == nil {
		return nil
	}
	return &MCP{servers: ctrl.MCPServers()}
}

func (m *MCP) Key() string  { return "mcp" }
func (m *MCP) Modal() bool  { return true }
func (m *MCP) Hint() string { return "[↑↓] scroll · [Esc] close" }

func (m *MCP) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c", "enter", "q":
		return true, nil
	case "up", "k":
		if m.sel > 0 {
			m.sel--
		}
		return false, nil
	case "down", "j":
		if m.sel < len(m.servers)-1 {
			m.sel++
		}
		return false, nil
	}
	return false, nil
}

func (m *MCP) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 40 {
		innerWidth = 40
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /MCP — configured servers"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"MCP servers load when evva starts; add one with /setup-mcp, then restart to connect it.",
	))
	b.WriteString("\n\n")

	if len(m.servers) == 0 {
		b.WriteString(th.DimText.Render(
			"No MCP servers configured. Ask evva to set one up, or run /setup-mcp.",
		))
		b.WriteString("\n\n")
		b.WriteString(th.FooterHint.Render("[Esc] close"))
		return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
	}

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	for i, s := range m.servers {
		marker := "  "
		nameStyle := th.PanelRow
		if i == m.sel {
			marker = "▶ "
			nameStyle = sel
		}

		// Pad the plain name BEFORE styling — ANSI escapes would otherwise
		// break %-width alignment across rows.
		name := nameStyle.Render(fmt.Sprintf("%-16s", truncate(s.Name, 16)))
		transport := th.DimText.Render(fmt.Sprintf("%-5s", s.Transport))
		status := mcpStatusStyle(th, s.Status).Render(s.Status)

		meta := fmt.Sprintf("  %d tools", s.ToolCount)
		if s.ResourceCount > 0 {
			meta += " · resources"
		}
		if s.Scope != "" {
			meta += " · " + s.Scope
		}

		b.WriteString(marker + name + " " + transport + th.DimText.Render(" · ") + status)
		b.WriteString(th.DimText.Render(meta))
		b.WriteByte('\n')

		if s.Detail != "" {
			b.WriteString(th.DimText.Render("     " + truncate(s.Detail, innerWidth-6)))
			b.WriteByte('\n')
		}
		if s.Error != "" {
			b.WriteString(th.ToolErr.Render("     └ " + truncate(s.Error, innerWidth-8)))
			b.WriteByte('\n')
		}
	}

	b.WriteByte('\n')
	b.WriteString(th.FooterHint.Render("[↑↓] scroll · [Esc] close"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// mcpStatusStyle maps a server status to a themed style: green connected,
// red failed, yellow needs-auth, muted pending/disabled.
func mcpStatusStyle(th *theme.Theme, status string) lipgloss.Style {
	switch status {
	case "connected":
		return th.ToolOK
	case "failed":
		return th.ToolErr
	case "needs-auth":
		return th.TimelineMatch
	case "disabled":
		return th.DimText
	default: // pending
		return th.FooterHint
	}
}
