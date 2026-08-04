package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Queue is the /queue panel: the mid-run messages the agent has been
// handed but has not yet read, in the order it will read them.
//
// A queue you cannot see is a queue you cannot trust. Once evva accepted
// typing during a run, the text left the composer and went somewhere the
// operator had no view of — and "did that send?" is answerable only by
// waiting. This panel answers it, and adds the one action the queue was
// missing: taking a message back before the model ever sees it.
//
// Revoking is genuinely best-effort. The loop drains on its own schedule,
// so a message can be delivered between the frame that rendered it and the
// keypress that tried to withdraw it. The panel says so rather than
// pretending otherwise — an "unsend" that silently failed would be worse
// than no unsend at all.
type Queue struct {
	ctrl   ui.Controller
	rows   []ui.PendingPrompt
	cursor int
	notice string
}

const queuePageSize = 10

// NewQueue snapshots the controller's pending queue. ctrl may be nil
// (pre-Attach), matching the other panels.
func NewQueue(ctrl ui.Controller) *Queue {
	if ctrl == nil {
		return nil
	}
	return &Queue{ctrl: ctrl, rows: ctrl.PendingPrompts()}
}

func (q *Queue) Key() string { return "queue" }
func (q *Queue) Modal() bool { return true }

func (q *Queue) Hint() string {
	if len(q.rows) == 0 {
		return "[Esc] close"
	}
	return "[d] revoke · [↑/↓] move · [Esc] close"
}

func (q *Queue) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c", "q":
		return true, nil
	case "up", "k":
		if q.cursor > 0 {
			q.cursor--
		}
	case "down", "j":
		if q.cursor < len(q.rows)-1 {
			q.cursor++
		}
	case "d", "delete", "backspace":
		if q.cursor >= len(q.rows) {
			return false, nil
		}
		row := q.rows[q.cursor]
		if q.ctrl.RevokePendingPrompt(row.ID) {
			q.notice = "revoked — the model never saw it"
		} else {
			q.notice = "too late — already delivered"
		}
		// Re-read rather than splicing the local slice: the loop may have
		// drained others since this panel opened, and a stale list would
		// offer to revoke messages that are already in the conversation.
		q.rows = q.ctrl.PendingPrompts()
		if q.cursor >= len(q.rows) {
			q.cursor = max(len(q.rows)-1, 0)
		}
	}
	return false, nil
}

func (q *Queue) View(width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /QUEUE — messages waiting to reach the model"))
	b.WriteString("\n\n")

	if len(q.rows) == 0 {
		if q.notice != "" {
			b.WriteString(th.DimText.Render(q.notice))
			b.WriteString("\n\n")
		}
		b.WriteString(th.DimText.Render("Nothing is queued."))
		b.WriteString("\n\n")
		b.WriteString(th.DimText.Render(
			"Typing while evva works queues your message for the next iteration\n" +
				"boundary. Ctrl+G sends it as an interject instead: whatever is running\n" +
				"is cut short so the message lands immediately."))
		b.WriteString("\n")
		b.WriteString(th.FooterHint.Render(q.Hint()))
		return th.InputBorder.Render(b.String())
	}

	b.WriteString(th.DimText.Render("pending · "))
	b.WriteString(th.StatusValue.Render(fmt.Sprintf("%d message(s), delivered top-first", len(q.rows))))
	b.WriteString("\n\n")

	end := min(queuePageSize, len(q.rows))
	for i, row := range q.rows[:end] {
		b.WriteString(queueRow(th, row, i == q.cursor, width))
		b.WriteByte('\n')
	}
	if end < len(q.rows) {
		b.WriteString(th.DimText.Render(fmt.Sprintf("  … %d more", len(q.rows)-end)))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	if q.notice != "" {
		b.WriteString(th.DimText.Render(q.notice))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render(q.Hint()))
	return th.InputBorder.Render(b.String())
}

// queueRow renders one pending message: a cursor marker, how it was sent,
// and a single-line preview of the text.
func queueRow(th *theme.Theme, row ui.PendingPrompt, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = "▸ "
	}
	tag := th.DimText.Render("[queued]   ")
	if row.Level == "interject" {
		tag = th.ToolErr.Render("[interject]")
	}
	return marker + tag + " " + th.StatusValue.Render(oneLine(row.Text, queueTextWidth(width)))
}

// queueTextWidth leaves room for the border, the marker and the level tag.
func queueTextWidth(panel int) int {
	w := panel - 18
	if w < 20 {
		return 20
	}
	return w
}

// oneLine flattens a multi-line prompt to a single row. A pasted stack
// trace would otherwise push the panel off screen.
func oneLine(s string, width int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if len(s) <= width {
		return s
	}
	return s[:width-1] + "…"
}
