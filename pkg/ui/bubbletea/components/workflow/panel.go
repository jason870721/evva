// Package workflow renders the dynamic-workflow board panel — the solo
// counterpart of the swarm web board: one row per task with lifecycle
// glyph, dependency badges, verify-policy chip, and a live spinner for
// tasks whose worker daemon is currently running.
//
// Pure rendering — no tea.Model. The App passes the current store (nil
// when the feature is off) plus the daemon state for owner liveness on
// every frame; returns "" when there is nothing to show so the layout
// collapses the slot.
package workflow

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/tools/daemon"
	wf "github.com/johnny1110/evva/pkg/tools/workflow"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Render returns the board panel as a styled string. Empty when the
// feature is off (nil store) or the board has no tasks.
func Render(store *wf.Store, ds *daemon.DaemonState, width int, th *theme.Theme, frame int) string {
	if store == nil {
		return ""
	}
	rows := store.List()
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(renderHeader("WORKFLOW", width, th))
	b.WriteByte('\n')
	for _, t := range rows {
		b.WriteString(renderRow(t, ds, width, th, frame))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderHeader produces the HUD section header — "▰▰ LABEL ▰▰▰…" padded
// to width (the todos panel's shape, so stacked panels read as one HUD).
func renderHeader(label string, width int, th *theme.Theme) string {
	left := th.PanelHeader.Render("▰▰ " + label + " ")
	tailLen := max(width-len(label)-4, 0)
	tail := th.Timeline.Render(strings.Repeat("▰", tailLen))
	return left + tail
}

// renderRow formats one task: lifecycle glyph, #id, label, and badges.
//
//	▣ #1 scaffold API                     completed
//	⠋ #2 implementing endpoints  ᵂ        running, live worker → spinner
//	▶ #3 reviewing the diff               running self-task
//	◈ #4 write tests                      verifying (result awaits judgment)
//	✘ #5 flaky step                       verifying, worker failed
//	▢ #6 integrate            auto        pending, verify:"auto" chip
//	· #7 release              ⛓ #4 #6     blocked, dependency badges
func renderRow(t wf.Task, ds *daemon.DaemonState, width int, th *theme.Theme, frame int) string {
	glyph := statusGlyph(t, ds, th, frame)

	label := t.Subject
	if t.Status == wf.StatusRunning && t.ActiveForm != "" {
		label = t.ActiveForm
	}

	var badges []string
	if t.EngineManaged() && t.Status != wf.StatusCompleted {
		badges = append(badges, th.DimText.Render("ᵂ"))
	}
	if t.Verify == wf.VerifyAuto && t.Status != wf.StatusCompleted {
		badges = append(badges, th.DimText.Render("auto"))
	}
	if t.Status == wf.StatusBlocked && len(t.DependsOn) > 0 {
		badges = append(badges, th.DimText.Render("⛓ #"+strings.Join(t.DependsOn, " #")))
	}
	badge := ""
	if len(badges) > 0 {
		badge = "  " + strings.Join(badges, " ")
	}

	id := th.DimText.Render("#" + t.ID)
	// 10 ≈ glyph + spaces + id column; keep the row inside width.
	maxLen := width - 10 - lipgloss.Width(badge)
	if maxLen > 0 && len(label) > maxLen {
		label = truncate(label, maxLen)
	}
	if t.Status == wf.StatusBlocked {
		label = th.DimText.Render(label)
	}
	return fmt.Sprintf("  %s %s %s%s", glyph, id, label, badge)
}

// statusGlyph picks the row symbol: the rotating spinner while a live
// worker daemon carries the task, static lifecycle glyphs otherwise.
// Vocabulary reuses the theme's existing task/subagent keys so the board
// reads consistently with the todos panel and the agents strip.
func statusGlyph(t wf.Task, ds *daemon.DaemonState, th *theme.Theme, frame int) string {
	key := ""
	switch t.Status {
	case wf.StatusCompleted:
		key = "completed"
	case wf.StatusPending:
		key = "pending"
	case wf.StatusBlocked:
		key = "deleted" // the dim neutral dot — blocked rows dim their label too
	case wf.StatusVerifying:
		if t.WorkerFailed {
			key = "crushed"
		} else {
			key = "draining" // result parked, awaiting judgment
		}
	case wf.StatusRunning:
		if workerAlive(ds, t.Owner) {
			if style, ok := th.SpinnerStyle("executing"); ok {
				return style.Render(theme.SpinnerFrame(frame))
			}
		}
		key = "in_progress"
	}
	g := th.Glyph(key)
	return lipgloss.NewStyle().Foreground(g.Color).Render(g.Symbol)
}

// workerAlive reports whether the owner daemon id is registered and not
// terminal — the spinner condition.
func workerAlive(ds *daemon.DaemonState, owner string) bool {
	if ds == nil || owner == "" {
		return false
	}
	d, ok := ds.Get(owner)
	if !ok {
		return false
	}
	return !daemon.IsTerminal(d.Snapshot().Status)
}

func truncate(s string, max int) string {
	if max < 1 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
