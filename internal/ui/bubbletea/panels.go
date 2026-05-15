package bubbletea

import (
	"fmt"
	"github.com/johnny1110/evva/internal/constant"
	"strings"

	"github.com/johnny1110/evva/internal/tools/task"
	"github.com/johnny1110/evva/internal/toolset"
)

// renderTaskPanel returns the task panel string, or "" when there are no
// non-deleted tasks. Empty return collapses the panel entirely so it
// takes zero screen rows.
func renderTaskPanel(ts *toolset.ToolState) string {
	store := ts.TaskStore()
	tasks := store.List()
	// Filter out deleted tasks — they stay in the store for audit but
	// shouldn't clutter the live panel.
	rows := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Status == task.StatusDeleted {
			continue
		}
		rows = append(rows, t)
	}
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(styles.PanelHeader.Render("Tasks"))
	b.WriteByte('\n')
	for _, t := range rows {
		b.WriteString(styles.PanelRow.Render(fmt.Sprintf("  [%s] %s  %s", t.Status, t.ID, t.Subject)))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSubagentPanel returns the subagent panel string, or "" when no
// subagents are tracked. Mirrors the task panel's "collapse when empty"
// behavior.
//
// HasAgentGroupPanel avoids forcing allocation of the SpawnGroup for
// agents that never spawned anything — those stay panel-free.
func renderSubagentPanel(ts *toolset.ToolState) string {
	if !ts.HasAgentGroupPanel() {
		return ""
	}
	rows := ts.AgentGroup().Snapshot()
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(styles.PanelHeader.Render("Subagents"))
	b.WriteByte('\n')
	for _, r := range rows {
		marker := ""
		if r.Async {
			marker = " (async)"
		}
		summary := r.JobDesc
		if r.Status == constant.READY_REPORT.String() && r.Summary != "" {
			summary = "→ " + truncate(r.Summary, 80)
		} else if r.Status == constant.CRUSHED.String() && r.Err != "" {
			summary = "✗ " + truncate(r.Err, 80)
		}
		b.WriteString(styles.PanelRow.Render(fmt.Sprintf("  [%s] %s%s  %s", r.Status, r.ID, marker, summary)))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
