package lp

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestGoldThemePopulatesReusedFields guards the reuse contract: every theme
// style field the reused bubbletea renderers (transcript, slash, overlays)
// read must carry a foreground color, or those components would render with
// zero-value (default) styling against lp's black background.
func TestGoldThemePopulatesReusedFields(t *testing.T) {
	th := goldTheme()
	if th.Rev == 0 {
		t.Fatal("goldTheme().Rev should be non-zero (cache discriminator)")
	}

	fields := map[string]lipgloss.Style{
		"UserPrompt":  th.UserPrompt,
		"Assistant":   th.Assistant,
		"Thinking":    th.Thinking,
		"ToolCall":    th.ToolCall,
		"ToolOK":      th.ToolOK,
		"ToolErr":     th.ToolErr,
		"ToolResult":  th.ToolResult,
		"DiffAdd":     th.DiffAdd,
		"DiffRemove":  th.DiffRemove,
		"PanelHeader": th.PanelHeader,
		"PanelRow":    th.PanelRow,
		"StatusKey":   th.StatusKey,
		"StatusValue": th.StatusValue,
		"StatusSep":   th.StatusSep,
		"ErrorBanner": th.ErrorBanner,
		"DimText":     th.DimText,
		"FooterHint":  th.FooterHint,
		"TasksDone":   th.TasksDone,
		"ContextFill": th.ContextFill,
		"ContextRail": th.ContextRail,
	}
	for name, s := range fields {
		if _, ok := s.GetForeground().(lipgloss.Color); !ok {
			t.Errorf("theme field %s has no foreground color set", name)
		}
	}
}
