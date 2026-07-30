package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Redactions is the /redactions panel: what secret redaction masked out of
// this run's tool results, and why.
//
// It exists because a redactor you cannot inspect is a redactor you cannot
// debug. When a rule fires on something that was not a secret, the operator
// needs to see which rule and which value in order to write an allowlist
// entry — "my build output turned into [REDACTED:high-entropy:4f2a]" has to
// have an answer.
//
// Values are hidden until the operator presses `r`. That is not a security
// boundary — anyone at the keyboard can press it — it is a shoulder-surfing
// and screen-share guard, which is the realistic threat for a terminal. The
// panel renders UI-side only and never writes back to the session: this is
// the one path by which a masked value re-enters the world, and it stops at
// the screen.
type Redactions struct {
	rows     []ui.RedactionInfo
	revealed bool
	scroll   int
}

const redactionsPageSize = 12

// NewRedactions snapshots the controller's redaction ledger. ctrl may be
// nil (pre-Attach), matching the other read-only panels.
func NewRedactions(ctrl ui.Controller) *Redactions {
	if ctrl == nil {
		return nil
	}
	return &Redactions{rows: ctrl.Redactions()}
}

func (r *Redactions) Key() string { return "redactions" }
func (r *Redactions) Modal() bool { return true }

func (r *Redactions) Hint() string {
	if len(r.rows) == 0 {
		return "[Esc] close"
	}
	if r.revealed {
		return "[r] hide values · [↑/↓] scroll · [Esc] close"
	}
	return "[r] reveal values · [↑/↓] scroll · [Esc] close"
}

func (r *Redactions) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c", "q":
		return true, nil
	case "r":
		r.revealed = !r.revealed
	case "up", "k":
		if r.scroll > 0 {
			r.scroll--
		}
	case "down", "j":
		if r.scroll < len(r.rows)-redactionsPageSize {
			r.scroll++
		}
	}
	return false, nil
}

func (r *Redactions) View(width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /REDACTIONS — secrets withheld from the model"))
	b.WriteString("\n\n")

	if len(r.rows) == 0 {
		b.WriteString(th.DimText.Render("Nothing has been redacted this session."))
		b.WriteString("\n\n")
		b.WriteString(th.DimText.Render(
			"Tool results are scanned for credential shapes before they enter the\n" +
				"conversation. An empty list means none matched — not that none exist."))
		b.WriteString("\n")
		b.WriteString(th.FooterHint.Render(r.Hint()))
		return th.InputBorder.Render(b.String())
	}

	total := 0
	for _, row := range r.rows {
		total += row.Count
	}
	b.WriteString(th.DimText.Render("masked · "))
	b.WriteString(th.StatusValue.Render(fmt.Sprintf("%d secret(s), %d occurrence(s)", len(r.rows), total)))
	b.WriteString("\n\n")

	end := min(r.scroll+redactionsPageSize, len(r.rows))
	for _, row := range r.rows[r.scroll:end] {
		b.WriteString(redactionRow(th, row, r.revealed, width))
		b.WriteByte('\n')
	}
	if end < len(r.rows) {
		b.WriteString(th.DimText.Render(fmt.Sprintf("  … %d more", len(r.rows)-end)))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	if r.revealed {
		b.WriteString(th.ToolErr.Render("Values shown below are real credentials — mind who can see this screen."))
	} else {
		b.WriteString(th.DimText.Render("Values are hidden. The model never received them either way."))
	}
	b.WriteByte('\n')
	b.WriteString(th.FooterHint.Render(r.Hint()))
	return th.InputBorder.Render(b.String())
}

// redactionRow renders one finding: the placeholder the model saw, the rule
// that claimed it, and either the masked or the revealed value.
func redactionRow(th *theme.Theme, row ui.RedactionInfo, reveal bool, width int) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(th.StatusValue.Render(row.Placeholder))
	if row.Count > 1 {
		b.WriteString(th.DimText.Render(fmt.Sprintf(" ×%d", row.Count)))
	}
	b.WriteByte('\n')
	b.WriteString("    ")
	b.WriteString(th.DimText.Render(row.Why))
	b.WriteByte('\n')
	b.WriteString("    ")
	if reveal {
		b.WriteString(th.ToolErr.Render(truncateValue(row.Value, valueWidth(width))))
	} else {
		b.WriteString(th.DimText.Render(maskValue(row.Value)))
	}
	b.WriteByte('\n')
	return b.String()
}

// valueWidth leaves room for the border and the row's indent.
func valueWidth(panel int) int {
	w := panel - 10
	if w < 20 {
		return 20
	}
	return w
}

// maskValue shows a credential's length and nothing else. Length alone is
// often enough for the operator to recognise which key it was, without
// putting the characters on screen.
func maskValue(v string) string {
	return fmt.Sprintf("%s (%d chars)", strings.Repeat("•", min(len(v), 12)), len(v))
}

// truncateValue keeps a revealed value on one line. A PEM block is
// thousands of characters and would push the whole panel off screen.
func truncateValue(v string, width int) string {
	v = strings.ReplaceAll(strings.ReplaceAll(v, "\n", "⏎"), "\r", "")
	if len(v) <= width {
		return v
	}
	return v[:width-1] + "…"
}
