package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

const maxTodoRows = 8

// RenderTodos renders lp's compact task panel: a gold "tasks" header and one
// row per todo with lp's own restrained glyphs (· pending, ▸ active, ✓ done).
// Returns "" when empty. lp owns this rather than reusing the bubbletea todos
// strip so its glyphs stay gold/sage instead of falling back to the theme's
// (nil) private glyph map.
func RenderTodos(store *todo.TodoStore, width int, th *theme.Theme) string {
	if store == nil {
		return ""
	}
	items := store.List()
	if len(items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("tasks"))

	extra := 0
	if len(items) > maxTodoRows {
		extra = len(items) - maxTodoRows
		items = items[:maxTodoRows]
	}
	for _, t := range items {
		b.WriteByte('\n')
		b.WriteString(todoRow(t, width, th))
	}
	if extra > 0 {
		b.WriteByte('\n')
		b.WriteString(th.DimText.Render(fmt.Sprintf("  +%d more", extra)))
	}
	return b.String()
}

func todoRow(t todo.Todo, width int, th *theme.Theme) string {
	var glyph string
	var gstyle, cstyle lipgloss.Style
	switch t.Status {
	case todo.StatusCompleted:
		glyph, gstyle, cstyle = "✓", th.TasksDone, th.DimText
	case todo.StatusInProgress:
		glyph, gstyle, cstyle = "▸", th.UserPrompt, th.PanelRow
	default:
		glyph, gstyle, cstyle = "·", th.DimText, th.PanelRow
	}
	return "  " + gstyle.Render(glyph) + " " + cstyle.Render(clipText(t.Content, width-6))
}

// RenderDaemons renders a one-line summary of the active background daemons
// (subagents, bash tasks, monitors, lsp), grouped by kind. Returns "" when
// none are running or the state is nil. Terminal daemons (EndedAt set) are
// omitted — they're about to be evicted.
func RenderDaemons(ds *daemon.DaemonState, _ int, th *theme.Theme) string {
	if ds == nil {
		return ""
	}
	counts := map[daemon.DaemonKind]int{}
	for _, sn := range ds.Snapshot() {
		if !sn.EndedAt.IsZero() {
			continue
		}
		counts[sn.Kind]++
	}

	var parts []string
	add := func(k daemon.DaemonKind, label string) {
		if n := counts[k]; n > 0 {
			parts = append(parts, th.PanelRow.Render(fmt.Sprintf("%s %d", label, n)))
		}
	}
	add(daemon.KindLocalAgent, "agents")
	add(daemon.KindLocalBash, "tasks")
	add(daemon.KindMonitor, "monitors")
	add(daemon.KindLSP, "lsp")
	if len(parts) == 0 {
		return ""
	}
	return "  " + th.StatusKey.Render("◷ ") + strings.Join(parts, th.StatusSep.Render(" · "))
}

// AllTodosCompleted reports whether the store holds at least one todo and
// every one is completed — the trigger for the auto-fold snapshot. Inlined
// here so lp doesn't import the bubbletea todos strip at all.
func AllTodosCompleted(store *todo.TodoStore) bool {
	if store == nil {
		return false
	}
	items := store.List()
	if len(items) == 0 {
		return false
	}
	for _, t := range items {
		if t.Status != todo.StatusCompleted {
			return false
		}
	}
	return true
}

// RenderTasksDoneSnapshot is the synthetic transcript block appended when
// every todo completes (just before the store is cleared): a sage header and
// the finished list, so the scrollback keeps a record of what was done.
func RenderTasksDoneSnapshot(store *todo.TodoStore, width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.TasksDone.Render("✓ tasks complete"))
	for _, t := range store.List() {
		b.WriteByte('\n')
		b.WriteString("  " + th.TasksDone.Render("✓") + " " + th.DimText.Render(clipText(t.Content, width-6)))
	}
	return b.String()
}

// clipText rune-safely truncates s to max columns, appending an ellipsis when
// cut. Rune-based so multibyte todo content isn't split mid-character.
func clipText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 2 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
