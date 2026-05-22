// Package bgtasks renders the horizontal background-task chip strip.
// Mirrors components/agents/strip.go shape — one chip per task,
// rotating spinner glyph while running, static glyph on terminal
// states. Returns "" when no tasks are tracked so the layout
// collapses the slot.
//
// Source of truth is *toolset.ToolState.BgTaskStore (Phase 16). The
// strip subscribes implicitly via the agent's KindStoreUpdate bridge —
// the bubbletea host re-renders when any store update arrives.
package bgtasks

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
	"github.com/johnny1110/evva/pkg/tools/shell"
)

// bgChipMaxLabel caps the visible label length inside a chip so
// several tasks can fit on one row.
const bgChipMaxLabel = 16

// Render returns the chip strip as a (possibly multi-line) string.
// frame is the spinner frame index used to animate running tasks.
func Render(ts *toolset.ToolState, width int, th *theme.Theme, frame int) string {
	if ts == nil || !ts.HasBgTaskStore() {
		return ""
	}
	rows := ts.BgTaskStore().Snapshot()
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

func renderChip(r shell.BgTaskSnapshot, th *theme.Theme, frame int) string {
	status := string(r.Status)
	glyph := renderStatusGlyph(status, th, frame)
	label := r.Description
	if label == "" {
		label = r.Command
	}
	if len(label) > bgChipMaxLabel {
		label = label[:bgChipMaxLabel-1] + "…"
	}

	c := chipColor(status, th)
	chev := lipgloss.NewStyle().Foreground(c).Bold(true)
	idStyle := lipgloss.NewStyle().Foreground(c)
	labelStyle := lipgloss.NewStyle().Foreground(c)
	return chev.Render("‹") + glyph + " " + idStyle.Render(r.ID) + " " + labelStyle.Render(label) + chev.Render("›")
}

func renderStatusGlyph(status string, th *theme.Theme, frame int) string {
	if status == string(shell.BgRunning) {
		if style, ok := th.SpinnerStyle("executing"); ok {
			return style.Render(theme.SpinnerFrame(frame))
		}
	}
	g := th.Glyph(status)
	return lipgloss.NewStyle().Foreground(g.Color).Render(g.Symbol)
}

func chipColor(status string, th *theme.Theme) lipgloss.Color {
	var c lipgloss.TerminalColor
	switch status {
	case string(shell.BgRunning):
		c = th.ToolCall.GetForeground()
	case string(shell.BgCompleted):
		c = th.TasksDone.GetForeground()
	case string(shell.BgFailed):
		c = th.ErrorBanner.GetForeground()
	case string(shell.BgKilled):
		c = th.DimText.GetForeground()
	default:
		c = th.ContextFill.GetForeground()
	}
	if col, ok := c.(lipgloss.Color); ok {
		return col
	}
	return lipgloss.Color("#7A7E94")
}
