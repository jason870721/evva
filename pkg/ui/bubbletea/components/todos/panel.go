// Package todos renders the bottom todo panel and the green
// "TODOS COMPLETE" snapshot folded into the transcript when every
// todo in the store finishes.
//
// Pure rendering — no tea.Model. The App passes the current
// *todo.TodoStore to Render on every frame; the panel reads the
// store and produces a styled multi-line string. Returns ""
// when there's nothing to show so the layout collapses the slot.
package todos

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
	"github.com/johnny1110/evva/pkg/tools/todo"
)

// Render returns the todo panel as a styled string. Empty when no todos
// are tracked.
//
// width caps row length; oversized contents are truncated with an
// ellipsis. The header is a HUD-style scanline ("▰▰ TODOS ▰▰▰…").
func Render(store *todo.TodoStore, width int, th *theme.Theme) string {
	if store == nil {
		return ""
	}
	rows := store.List()
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(renderHeader("TODOS", width, th))
	b.WriteByte('\n')
	for _, t := range rows {
		b.WriteString(renderRow(t, width, th))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// AllCompleted reports whether every todo in the store has reached
// StatusCompleted. The App watches this on every todo KindStoreUpdate
// to decide when to auto-fold the panel into a transcript snapshot.
//
// Returns false on an empty store so a fresh store doesn't trigger a
// phantom snapshot.
func AllCompleted(store *todo.TodoStore) bool {
	if store == nil {
		return false
	}
	rows := store.List()
	if len(rows) == 0 {
		return false
	}
	for _, t := range rows {
		if t.Status != todo.StatusCompleted {
			return false
		}
	}
	return true
}

// RenderCompleteSnapshot builds the green "TODOS COMPLETE" snapshot
// that gets folded into the transcript when the panel auto-clears.
// Mirrors Render's row shape but uses the green header style so the
// snapshot reads as a definite "done" event in the scrollback.
func RenderCompleteSnapshot(store *todo.TodoStore, width int, th *theme.Theme) string {
	if store == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(th.TasksDone.Render("✔ TODOS COMPLETE"))
	for _, t := range store.List() {
		b.WriteByte('\n')
		b.WriteString(renderRow(t, width, th))
	}
	return b.String()
}

// renderHeader produces a HUD section header — "▰▰ LABEL ▰▰▰…" with
// the trailing rail padded out to width.
func renderHeader(label string, width int, th *theme.Theme) string {
	left := th.PanelHeader.Render("▰▰ " + label + " ")
	// 4 = "▰▰ " (3 cols) + trailing space (1 col).
	tailLen := width - len(label) - 4
	if tailLen < 0 {
		tailLen = 0
	}
	tail := th.Timeline.Render(strings.Repeat("▰", tailLen))
	return left + tail
}

// renderRow formats one todo with its lifecycle glyph + content.
// Content longer than the row width is truncated; the glyph always
// stays visible so the user can read progress at a glance.
func renderRow(t todo.Todo, width int, th *theme.Theme) string {
	g := th.Glyph(string(t.Status))
	glyph := lipgloss.NewStyle().Foreground(g.Color).Render(g.Symbol)

	content := t.Content
	if t.Status == todo.StatusInProgress && t.ActiveForm != "" {
		content = t.ActiveForm
	}
	maxLen := width - 6
	if maxLen > 0 && len(content) > maxLen {
		content = truncate(content, maxLen)
	}
	return fmt.Sprintf("  %s  %s", glyph, content)
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
