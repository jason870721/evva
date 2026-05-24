// Package bgtasks renders the horizontal background-task chip strip.
// Mirrors components/agents/strip.go shape — one chip per task,
// rotating spinner glyph while running, static glyph on terminal
// states. Returns "" when no daemons of kind local_bash are tracked so
// the layout collapses the slot.
//
// Source of truth is the *daemon.DaemonState (the unified daemon
// catalog). The strip subscribes implicitly via the agent's
// KindStoreUpdate bridge — the bubbletea host re-renders when any
// daemon change arrives.
package bgtasks

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// bgChipMaxLabel caps the visible label length inside a chip so several
// daemons can fit on one row.
const bgChipMaxLabel = 16

// Render returns the chip strip as a (possibly multi-line) string. frame
// is the spinner frame index used to animate running daemons.
func Render(ds *daemon.DaemonState, width int, th *theme.Theme, frame int) string {
	if ds == nil {
		return ""
	}
	rows := ds.SnapshotByKind(daemon.KindLocalBash)
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

func renderChip(r daemon.DaemonSnapshot, th *theme.Theme, frame int) string {
	status := string(r.Status)
	glyph := renderStatusGlyph(status, th, frame)
	label := r.Description
	if label == "" {
		if meta, ok := r.Metadata.(daemon.LocalBashMeta); ok {
			label = meta.Command
		}
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
	if status == string(daemon.StatusRunning) {
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
	case string(daemon.StatusRunning):
		c = th.ToolCall.GetForeground()
	case string(daemon.StatusCompleted):
		c = th.TasksDone.GetForeground()
	case string(daemon.StatusFailed):
		c = th.ErrorBanner.GetForeground()
	case string(daemon.StatusKilled):
		c = th.DimText.GetForeground()
	default:
		c = th.ContextFill.GetForeground()
	}
	if col, ok := c.(lipgloss.Color); ok {
		return col
	}
	return lipgloss.Color("#7A7E94")
}
