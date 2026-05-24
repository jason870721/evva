// Package agents renders the horizontal subagent chip strip that
// sits just above the input. Each in-flight (or recently completed)
// subagent appears as one bracketed chip:
//
//	‹⠋ explorer› ‹▶ writer› ‹✔ reviewer›
//
// Chips animate their leading glyph for active phases (thinking,
// executing, draining, …); terminal phases (ready_report, crushed)
// show their static glyph. Async subagents get a small superscript "ᵃ"
// so the user can see fire-and-forget vs. blocking.
//
// Source of truth is the *daemon.DaemonState filtered by KindLocalAgent.
// The strip subscribes implicitly via the agent's KindStoreUpdate bridge.
package agents

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// agentChipMaxName caps the visible name length inside a chip so several
// agents can fit on one row.
const agentChipMaxName = 12

// Render returns the chip strip as a styled (possibly multi-line) string.
// width is the available column count; chips that don't fit wrap to a
// fresh row rather than truncating — losing visibility of a running
// agent is worse than spending an extra row.
//
// frame is the spinner index from the App's State; animated chips pick
// their glyph from theme.SpinnerFrame(frame).
func Render(ds *daemon.DaemonState, width int, th *theme.Theme, frame int) string {
	if ds == nil {
		return ""
	}
	rows := ds.SnapshotByKind(daemon.KindLocalAgent)
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

// renderChip formats one subagent as ‹glyph name›. Glyph and color follow
// the fine-grained Phase (LocalAgentMeta.Phase) while it's running, and
// the coarse Status (DaemonStatus) once terminal. Async subagents get a
// dim "ᵃ" superscript before the closing chevron.
func renderChip(r daemon.DaemonSnapshot, th *theme.Theme, frame int) string {
	meta, _ := r.Metadata.(daemon.LocalAgentMeta)
	statusKey := chipStatusKey(r.Status, meta.Phase)
	glyph := renderStatusGlyph(statusKey, th, frame)

	name := r.Description
	if name == "" {
		name = r.ID
	}
	if len(name) > agentChipMaxName {
		name = name[:agentChipMaxName-1] + "…"
	}

	c := chipColor(statusKey, th)
	chev := lipgloss.NewStyle().Foreground(c).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(c)
	async := ""
	if meta.Async {
		async = th.DimText.Render("ᵃ")
	}
	return chev.Render("‹") + glyph + " " + nameStyle.Render(name) + async + chev.Render("›")
}

// chipStatusKey picks the string the theme palette is keyed off. While
// the daemon is running, the fine-grained Phase wins (so the chip flips
// through thinking → executing → draining → ...). Once terminal, the
// daemon's coarse Status takes over.
func chipStatusKey(status daemon.DaemonStatus, phase string) string {
	if daemon.IsTerminal(status) {
		switch status {
		case daemon.StatusCompleted:
			return "ready_report"
		case daemon.StatusFailed:
			return "crushed"
		case daemon.StatusKilled:
			return "interrupted"
		}
		return string(status)
	}
	if phase != "" {
		return strings.ToLower(phase)
	}
	return string(status)
}

// renderStatusGlyph picks the right symbol for a status key: the rotating
// spinner frame in the spinner color when the status is active, the static
// palette glyph otherwise.
func renderStatusGlyph(status string, th *theme.Theme, frame int) string {
	if style, ok := th.SpinnerStyle(status); ok {
		return style.Render(theme.SpinnerFrame(frame))
	}
	g := th.Glyph(status)
	return lipgloss.NewStyle().Foreground(g.Color).Render(g.Symbol)
}

// chipColor maps a subagent's lifecycle status to its chip color.
// Mirrors the status pill vocabulary so agent state reads consistently
// across the bottom of the UI.
func chipColor(status string, th *theme.Theme) lipgloss.Color {
	var c lipgloss.TerminalColor
	switch status {
	case "thinking", "texting":
		c = th.Thinking.GetForeground()
	case "executing":
		c = th.ToolCall.GetForeground()
	case "draining", "saving":
		c = th.Draining.GetForeground()
	case "compacting", "max_iters":
		c = th.Compacting.GetForeground()
	case "ready_report", "idle":
		c = th.TasksDone.GetForeground()
	case "crushed", "interrupted":
		c = th.ErrorBanner.GetForeground()
	case "init":
		c = th.DimText.GetForeground()
	default:
		c = th.ContextFill.GetForeground()
	}
	if col, ok := c.(lipgloss.Color); ok {
		return col
	}
	return lipgloss.Color("#7A7E94")
}
