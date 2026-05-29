package overlays

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func sampleMCPServers() []ui.MCPServerInfo {
	return []ui.MCPServerInfo{
		{Name: "filesystem", Transport: "stdio", Status: "connected", Scope: "project",
			Detail: "npx -y @modelcontextprotocol/server-filesystem ${HOME}/work", ToolCount: 12, ResourceCount: 1},
		{Name: "github", Transport: "http", Status: "needs-auth", Scope: "user",
			Detail: "https://api.example/mcp/", Error: "401 unauthorized"},
	}
}

// NewMCP returns nil for a nil ctrl so the App hints "no controller attached"
// instead of opening an empty overlay.
func TestNewMCPNilCtrl(t *testing.T) {
	if o := NewMCP(nil); o != nil {
		t.Errorf("NewMCP(nil) should return nil, got %+v", o)
	}
}

func TestMCPKeyModalHint(t *testing.T) {
	m := &MCP{}
	if m.Key() != "mcp" {
		t.Errorf("Key = %q, want mcp", m.Key())
	}
	if !m.Modal() {
		t.Error("Modal should be true")
	}
	if m.Hint() == "" {
		t.Error("Hint should be non-empty")
	}
}

// Esc / Enter / q all close the read-only panel.
func TestMCPClosingKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		m := &MCP{servers: sampleMCPServers()}
		if closed, _ := m.Update(key); !closed {
			t.Errorf("%v should close the overlay", key)
		}
	}
}

func TestMCPUpDownClamps(t *testing.T) {
	m := &MCP{servers: sampleMCPServers()} // two rows
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.sel != 1 {
		t.Errorf("Down should advance sel to 1, got %d", m.sel)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // clamp at last
	if m.sel != 1 {
		t.Errorf("Down past last should clamp at 1, got %d", m.sel)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp}) // clamp at first
	if m.sel != 0 {
		t.Errorf("Up past first should clamp at 0, got %d", m.sel)
	}
}

func TestMCPViewPopulated(t *testing.T) {
	m := &MCP{servers: sampleMCPServers()}
	out := m.View(80, theme.Default())
	for _, want := range []string{"filesystem", "stdio", "connected", "github", "http", "needs-auth", "401 unauthorized"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q in:\n%s", want, out)
		}
	}
}

func TestMCPViewEmpty(t *testing.T) {
	m := &MCP{}
	out := m.View(80, theme.Default())
	if !strings.Contains(out, "No MCP servers") {
		t.Errorf("empty View() should mention no servers, got:\n%s", out)
	}
}
