package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// QuestionRespondedMsg is fired after the overlay delivers answers to the
// controller. The app's event handler can use it for bookkeeping.
type QuestionRespondedMsg struct {
	RequestID string
}

// questionState is the per-question selection state.
type questionState struct {
	cursor    int      // highlighted option index
	selected  []bool   // which options are toggled (len = options + 1 for Other)
	otherMode bool     // user is typing the "Other" free-text
	otherText string   // text typed for "Other"
}

// Question is the modal overlay rendered when AskUserQuestion is invoked.
// Supports 1–4 questions with left/right navigation. Each question supports
// single-select, multi-select, and a free-text "Other" option auto-added by
// the UI. Single-select questions with preview fields switch to a side-by-side
// layout with the option list on the left and the preview on the right.
type Question struct {
	ctrl      ui.Controller
	payload   event.QuestionNeededPayload

	currentQ int             // index of the question page currently visible
	qs       []questionState // per-question state (parallel to payload.Questions)

	errMsg string
}

// NewQuestion builds the overlay for a KindQuestionNeeded event.
// Returns nil when the controller is not yet attached.
func NewQuestion(ctrl ui.Controller, payload event.QuestionNeededPayload) *Question {
	if ctrl == nil {
		return nil
	}
	qs := make([]questionState, len(payload.Questions))
	for i, q := range payload.Questions {
		// options = model options + "Other"
		qs[i] = questionState{
			selected: make([]bool, len(q.Options)+1),
		}
	}
	return &Question{ctrl: ctrl, payload: payload, qs: qs}
}

func (q *Question) Key() string { return "question:" + q.payload.RequestID }
func (q *Question) Modal() bool { return true }

func (q *Question) Hint() string {
	cur := &q.qs[q.currentQ]
	if cur.otherMode {
		return "[Enter] confirm · [Esc] cancel"
	}
	total := len(q.payload.Questions)
	if total > 1 {
		return "[↑↓] move · [Space/Enter] select · [←→] questions · [Tab] submit · [Esc] cancel"
	}
	return "[↑↓] move · [Space/Enter] select · [Tab] submit · [Esc] cancel"
}

// Update handles keyboard input for the overlay.
func (q *Question) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}

	cur := &q.qs[q.currentQ]
	cq := q.payload.Questions[q.currentQ]
	optCount := len(cq.Options) + 1 // +1 for Other

	// Other-text mini-input mode
	if cur.otherMode {
		return q.updateOtherMode(key, cur)
	}

	switch key.String() {
	case "esc":
		return q.cancel()

	case "ctrl+c":
		_, cmd := q.cancel()
		return true, cmd

	case "left", "h":
		if q.currentQ > 0 {
			q.currentQ--
		}
		return false, nil

	case "right", "l":
		if q.currentQ < len(q.payload.Questions)-1 {
			q.currentQ++
		}
		return false, nil

	case "up", "k":
		if cur.cursor > 0 {
			cur.cursor--
		}
		return false, nil

	case "down", "j":
		if cur.cursor < optCount-1 {
			cur.cursor++
		}
		return false, nil

	case "tab":
		// Jump to first unanswered; if all answered, submit.
		if first := q.firstUnanswered(); first >= 0 {
			q.currentQ = first
			return false, nil
		}
		return q.submit()

	case " ", "enter":
		isOther := cur.cursor == len(cq.Options)
		if isOther {
			// Enter Other text mode.
			cur.otherMode = true
			// Deselect everything else for single-select.
			if !cq.MultiSelect {
				for i := range cur.selected {
					cur.selected[i] = false
				}
			}
			cur.selected[cur.cursor] = true
			return false, nil
		}

		if cq.MultiSelect {
			cur.selected[cur.cursor] = !cur.selected[cur.cursor]
		} else {
			// Single-select: clear all then set the chosen index.
			for i := range cur.selected {
				cur.selected[i] = false
			}
			cur.selected[cur.cursor] = true
			// Auto-advance to next unanswered after single selection.
			if next := q.nextUnansweredAfter(q.currentQ); next >= 0 {
				q.currentQ = next
			}
		}
		return false, nil
	}
	return false, nil
}

func (q *Question) updateOtherMode(key tea.KeyMsg, cur *questionState) (bool, tea.Cmd) {
	switch key.String() {
	case "enter":
		// Keep selection; exit text mode.
		cur.otherMode = false
		if next := q.nextUnansweredAfter(q.currentQ); next >= 0 {
			q.currentQ = next
		}
		return false, nil
	case "esc":
		cur.otherMode = false
		cur.otherText = ""
		cur.selected[cur.cursor] = false
		return false, nil
	case "backspace":
		if len(cur.otherText) > 0 {
			// Safe Unicode-aware backspace.
			runes := []rune(cur.otherText)
			cur.otherText = string(runes[:len(runes)-1])
		}
		return false, nil
	}
	if k := key.String(); len([]rune(k)) == 1 {
		cur.otherText += k
	}
	return false, nil
}

// firstUnanswered returns the index of the first question that has no
// selection, or -1 if all are answered.
func (q *Question) firstUnanswered() int {
	for i, qs := range q.qs {
		if !q.isAnswered(i, qs) {
			return i
		}
	}
	return -1
}

// nextUnansweredAfter returns the next unanswered question index after `from`,
// searching forward (wrapping not done on purpose — user can press left/right).
// Returns -1 if none remain after `from`.
func (q *Question) nextUnansweredAfter(from int) int {
	for i := from + 1; i < len(q.qs); i++ {
		if !q.isAnswered(i, q.qs[i]) {
			return i
		}
	}
	return -1
}

func (q *Question) isAnswered(idx int, qs questionState) bool {
	opts := q.payload.Questions[idx].Options
	isOther := qs.selected[len(opts)]
	if isOther {
		return len(strings.TrimSpace(qs.otherText)) > 0
	}
	for _, s := range qs.selected {
		if s {
			return true
		}
	}
	return false
}

func (q *Question) cancel() (bool, tea.Cmd) {
	id := q.payload.RequestID
	err := q.ctrl.RespondQuestion(id, ui.QuestionResponse{
		Answers:     map[string]string{},
		Annotations: map[string]ui.QuestionAnnotation{},
	})
	if err != nil {
		q.errMsg = err.Error()
		return false, nil
	}
	return true, func() tea.Msg { return QuestionRespondedMsg{RequestID: id} }
}

func (q *Question) submit() (bool, tea.Cmd) {
	answers := make(map[string]string, len(q.payload.Questions))
	multi := make(map[string][]string, len(q.payload.Questions))
	annotations := make(map[string]ui.QuestionAnnotation)

	for i, qitem := range q.payload.Questions {
		qs := q.qs[i]
		opts := qitem.Options
		isOther := qs.selected[len(opts)]

		var answer string
		var multiVals []string
		if isOther {
			answer = strings.TrimSpace(qs.otherText)
			if answer == "" {
				answer = "Other"
			}
			annotations[qitem.Question] = ui.QuestionAnnotation{Notes: answer}
			multiVals = []string{answer}
		} else if qitem.MultiSelect {
			var labels []string
			for j, sel := range qs.selected[:len(opts)] {
				if sel {
					labels = append(labels, opts[j].Label)
					if opts[j].Preview != "" {
						annotations[qitem.Question] = ui.QuestionAnnotation{Preview: opts[j].Preview}
					}
				}
			}
			answer = strings.Join(labels, ", ")
			multiVals = labels
		} else {
			for j, sel := range qs.selected[:len(opts)] {
				if sel {
					answer = opts[j].Label
					if opts[j].Preview != "" {
						annotations[qitem.Question] = ui.QuestionAnnotation{Preview: opts[j].Preview}
					}
					break
				}
			}
			if answer != "" {
				multiVals = []string{answer}
			}
		}
		answers[qitem.Question] = answer
		multi[qitem.Question] = multiVals
	}

	id := q.payload.RequestID
	err := q.ctrl.RespondQuestion(id, ui.QuestionResponse{
		Answers:      answers,
		MultiAnswers: multi,
		Annotations:  annotations,
	})
	if err != nil {
		q.errMsg = err.Error()
		return false, nil
	}
	return true, func() tea.Msg { return QuestionRespondedMsg{RequestID: id} }
}

// View renders the question overlay. When the current question has any option
// with a preview field (single-select only), the layout switches to
// side-by-side: options on the left, preview on the right.
func (q *Question) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 50 {
		innerWidth = 50
	}

	cqIdx := q.currentQ
	cqItem := q.payload.Questions[cqIdx]
	qs := &q.qs[cqIdx]
	total := len(q.payload.Questions)

	hasPreview := !cqItem.MultiSelect && hasAnyPreview(cqItem.Options)

	var b strings.Builder

	// Header
	title := "▰ QUESTION"
	if total > 1 {
		title = fmt.Sprintf("▰ QUESTION (%d of %d)", cqIdx+1, total)
	}
	b.WriteString(th.PanelHeader.Render(title))
	b.WriteByte('\n')

	// Question chip + text
	chip := cqItem.Header
	if len([]rune(chip)) > 12 {
		chip = string([]rune(chip)[:12])
	}
	b.WriteString(th.StatusKey.Render("["+chip+"]"))
	b.WriteString("  ")
	b.WriteString(th.StatusValue.Render(cqItem.Question))
	if cqItem.MultiSelect {
		b.WriteString(th.DimText.Render("  (multi-select)"))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')

	if hasPreview {
		b.WriteString(q.renderSideBySide(cqItem, qs, innerWidth, th))
	} else {
		b.WriteString(q.renderOptionList(cqItem, qs, innerWidth, th))
	}

	// Error
	if q.errMsg != "" {
		b.WriteByte('\n')
		b.WriteString(th.ErrorBanner.Render("✗ " + q.errMsg))
		b.WriteByte('\n')
	}

	// Navigation dots for multi-question
	if total > 1 {
		b.WriteByte('\n')
		b.WriteString(q.renderDots(total, cqIdx, th))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(th.FooterHint.Render(q.Hint()))

	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// renderOptionList renders the vertical list of options (no preview column).
func (q *Question) renderOptionList(qitem event.QuestionItem, qs *questionState, width int, th *theme.Theme) string {
	var b strings.Builder
	opts := qitem.Options

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	normal := th.StatusValue

	for i := 0; i <= len(opts); i++ {
		isOther := i == len(opts)
		isCursor := i == qs.cursor
		isSelected := qs.selected[i]

		var label, desc string
		if isOther {
			label = "Other"
			desc = "Type your own answer"
		} else {
			label = opts[i].Label
			desc = opts[i].Description
		}

		marker := optionMarker(qitem.MultiSelect, isSelected, isCursor)
		lineStyle := dim
		if isCursor {
			lineStyle = sel
		} else if isSelected {
			lineStyle = normal
		}

		b.WriteString(lineStyle.Render(marker + label))
		b.WriteByte('\n')
		if desc != "" {
			b.WriteString(th.DimText.Render("    " + truncate(desc, width-6)))
			b.WriteByte('\n')
		}

		// When the user has typed (or is typing) a free-text answer for the
		// Other option, show it right beneath the label so the saved value
		// remains visible after they exit edit mode. Previously the text
		// only rendered while otherMode was active and went hidden after
		// Enter, leaving users to assume they hadn't typed anything.
		if isOther && (qs.otherMode || qs.otherText != "") {
			b.WriteString(q.renderOtherInline(qs, width-6, th))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderOtherInline returns the indented "→ <text>▌" line beneath the
// Other option. The cursor block is only appended while the user is
// actively typing (otherMode true).
func (q *Question) renderOtherInline(qs *questionState, width int, th *theme.Theme) string {
	text := qs.otherText
	suffix := ""
	if qs.otherMode {
		suffix = "▌"
	}
	display := truncate(text+suffix, width)
	return th.StatusKey.Render("    → ") + th.StatusValue.Render(display)
}

// renderSideBySide renders a two-column layout: option list left, preview right.
func (q *Question) renderSideBySide(qitem event.QuestionItem, qs *questionState, width int, th *theme.Theme) string {
	leftW := width * 2 / 5
	if leftW < 20 {
		leftW = 20
	}
	rightW := width - leftW - 3
	if rightW < 20 {
		rightW = 20
	}

	// Build left column (option list, labels only, no desc to save height)
	var leftLines []string
	opts := qitem.Options
	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	normal := th.StatusValue

	for i := 0; i <= len(opts); i++ {
		isOther := i == len(opts)
		isCursor := i == qs.cursor
		isSelected := qs.selected[i]

		var label string
		if isOther {
			label = "Other"
		} else {
			label = opts[i].Label
		}

		marker := optionMarker(false, isSelected, isCursor)
		lineStyle := dim
		if isCursor {
			lineStyle = sel
		} else if isSelected {
			lineStyle = normal
		}
		leftLines = append(leftLines, lineStyle.Render(truncate(marker+label, leftW)))

		// Surface the saved free-text under the Other option so the user
		// always sees what they typed, not just while otherMode is active.
		if isOther && (qs.otherMode || qs.otherText != "") {
			leftLines = append(leftLines, q.renderOtherInline(qs, leftW, th))
		}
	}

	// Build right column (preview for the highlighted option)
	var preview string
	if qs.cursor < len(opts) && opts[qs.cursor].Preview != "" {
		preview = opts[qs.cursor].Preview
	}
	rightLines := wrapPreview(preview, rightW, th)

	// Merge columns
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}

	divider := th.DimText.Render("│")
	var b strings.Builder
	for i := 0; i < maxLines; i++ {
		var left, right string
		if i < len(leftLines) {
			left = leftLines[i]
		}
		if i < len(rightLines) {
			right = rightLines[i]
		}
		leftPad := lipgloss.NewStyle().Width(leftW).Render(left)
		b.WriteString(leftPad)
		b.WriteString(" ")
		b.WriteString(divider)
		b.WriteString(" ")
		b.WriteString(right)
		b.WriteByte('\n')
	}
	return b.String()
}

// renderDots renders navigation dots like  ○ ● ○  for multi-question.
func (q *Question) renderDots(total, current int, th *theme.Theme) string {
	var parts []string
	for i := 0; i < total; i++ {
		answered := q.isAnswered(i, q.qs[i])
		var dot string
		switch {
		case i == current && answered:
			dot = th.ContextFill.Render("◉")
		case i == current:
			dot = th.ContextFill.Render("●")
		case answered:
			dot = th.StatusValue.Render("◎")
		default:
			dot = th.DimText.Render("○")
		}
		parts = append(parts, dot)
	}
	return strings.Join(parts, " ")
}

// --- helpers ---

func optionMarker(multiSelect, selected, cursor bool) string {
	if multiSelect {
		if cursor && selected {
			return "▶[x] "
		}
		if cursor {
			return "▶[ ] "
		}
		if selected {
			return " [x] "
		}
		return " [ ] "
	}
	if cursor && selected {
		return "▶● "
	}
	if cursor {
		return "▶○ "
	}
	if selected {
		return " ● "
	}
	return " ○ "
}

func hasAnyPreview(opts []event.QuestionOption) bool {
	for _, o := range opts {
		if o.Preview != "" {
			return true
		}
	}
	return false
}

func wrapPreview(preview string, width int, th *theme.Theme) []string {
	if preview == "" {
		return []string{th.DimText.Render("(no preview)")}
	}
	lines := strings.Split(preview, "\n")
	var out []string
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(width - 2).
		Foreground(lipgloss.Color("#cccccc"))
	content := strings.Join(lines, "\n")
	rendered := box.Render(truncate(content, (width-2)*20)) // rough token budget
	for _, l := range strings.Split(rendered, "\n") {
		out = append(out, l)
	}
	return out
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return "..."
	}
	return string(runes[:max-3]) + "..."
}
