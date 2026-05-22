// Package monitors renders the horizontal monitor-task chip strip.
// Sibling to components/bgtasks/strip.go — separate strip so the user
// can glance at "background tasks" vs "streaming monitors" without
// scanning a mixed list.
//
// Source of truth is *toolset.ToolState.MonitorTaskStore.
package monitors

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
	"github.com/johnny1110/evva/pkg/tools/monitor"
)

const monChipMaxLabel = 16

func Render(ts *toolset.ToolState, width int, th *theme.Theme, frame int) string {
	if ts == nil || !ts.HasMonitorTaskStore() {
		return ""
	}
	rows := ts.MonitorTaskStore().Snapshot()
	if len(rows) == 0 {
		return ""
	}
	if width < 1 {
		return ""
	}

	spacer := th.DimText.Render(" ")
	var lines []string
	var current strings.Builder
	currentWidth := 0
	for _, r := range rows {
		chip := renderChip(r, th, frame)
		chipWidth := lipgloss.Width(chip)
		needWidth := chipWidth
		if currentWidth > 0 {
			needWidth++
		}
		if currentWidth > 0 && currentWidth+needWidth > width {
			lines = append(lines, current.String())
			current.Reset()
			currentWidth = 0
		}
		if currentWidth > 0 {
			current.WriteString(spacer)
			currentWidth++
		}
		current.WriteString(chip)
		currentWidth += chipWidth
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return strings.Join(lines, "\n")
}

func renderChip(r monitor.MonitorTaskSnapshot, th *theme.Theme, frame int) string {
	status := string(r.Status)
	glyph := renderStatusGlyph(status, th, frame)
	label := r.Description
	if label == "" {
		label = r.Command
	}
	if len(label) > monChipMaxLabel {
		label = label[:monChipMaxLabel-1] + "…"
	}
	c := chipColor(status, th)
	chev := lipgloss.NewStyle().Foreground(c).Bold(true)
	idStyle := lipgloss.NewStyle().Foreground(c)
	labelStyle := lipgloss.NewStyle().Foreground(c)
	counter := th.DimText.Render(fmt.Sprintf("·%d", r.EventCount))
	return chev.Render("‹") + glyph + " " + idStyle.Render(r.ID) + " " + labelStyle.Render(label) + counter + chev.Render("›")
}

func renderStatusGlyph(status string, th *theme.Theme, frame int) string {
	if status == string(monitor.Monitoring) {
		if style, ok := th.SpinnerStyle("draining"); ok {
			return style.Render(theme.SpinnerFrame(frame))
		}
	}
	g := th.Glyph(status)
	return lipgloss.NewStyle().Foreground(g.Color).Render(g.Symbol)
}

func chipColor(status string, th *theme.Theme) lipgloss.Color {
	var c lipgloss.TerminalColor
	switch status {
	case string(monitor.Monitoring):
		c = th.Draining.GetForeground()
	case string(monitor.Stopped):
		c = th.TasksDone.GetForeground()
	case string(monitor.Failed):
		c = th.ErrorBanner.GetForeground()
	default:
		c = th.ContextFill.GetForeground()
	}
	if col, ok := c.(lipgloss.Color); ok {
		return col
	}
	return lipgloss.Color("#7A7E94")
}
