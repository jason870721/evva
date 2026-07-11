package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/internal/swarm/tui/reduce"
)

// Layout constants: a fixed-width left column (roster + tasks) beside the
// stream; one header and one status line. Deliberately border-less chrome —
// section titles instead of box math — so 80×24 and 200×60 both render.
const (
	leftWidth  = 28
	minWidth   = 40
	minHeight  = 8
	resultCap  = 3 // tool-result lines shown before truncation
	titleLines = 1
	statusLine = 1
)

// layout recomputes the derived sizes after a resize.
func (m *Model) layout() {
	rightW := max(m.width-leftWidth-1, 20)
	bodyH := max(m.height-titleLines-statusLine, 3)
	m.vp.Width = rightW
	m.vp.Height = max(bodyH-2, 1) // stream title + composer/hint line
	m.input.Width = max(rightW-4, 10)
}

// refreshStream re-renders the viewport content from the folded turns.
func (m *Model) refreshStream() {
	if m.vp.Width <= 0 {
		return
	}
	turns := m.turns
	if m.focus != "" {
		turns = reduce.ConsoleTurns(m.turns, m.agentOf(m.focus), m.focus)
	}
	var b strings.Builder
	for _, t := range turns {
		b.WriteString(m.renderTurn(t))
	}
	m.vp.SetContent(b.String())
	if m.follow {
		m.vp.GotoBottom()
	}
}

// stamp renders the [HH:MM:SS] prefix, padded when the turn is timeless.
func (m *Model) stamp(t *reduce.Turn) string {
	c := reduce.Clock(t.At)
	if c == "" {
		return m.th.DimText.Render("          ")
	}
	return m.th.DimText.Render("[" + c + "] ")
}

// name resolves the turn's display name (member name over raw agent id).
func (m *Model) name(agentID string) string {
	if n := m.memberOf(agentID); n != "" {
		return n
	}
	if agentID == "" {
		return "?"
	}
	return agentID
}

// renderTurn renders one folded turn as wrapped terminal lines.
func (m *Model) renderTurn(t *reduce.Turn) string {
	w := max(m.vp.Width-1, 20)
	wrap := lipgloss.NewStyle().Width(w)
	head := m.stamp(t)
	switch t.Type {
	case reduce.TurnUser:
		return wrap.Render(head+m.th.UserPrompt.Render("user → "+t.Target+"  ")+t.Text) + "\n"
	case reduce.TurnThinking:
		return wrap.Render(head+m.th.Thinking.Render("· "+m.name(t.AgentID)+" "+t.Text)) + "\n"
	case reduce.TurnAssistant:
		cursor := ""
		if t.Open {
			cursor = "▌"
		}
		return wrap.Render(head+m.th.PanelHeader.Render(m.name(t.AgentID))+" "+m.th.Assistant.Render(t.Text+cursor)) + "\n"
	case reduce.TurnTool:
		line := head + m.th.ToolCall.Render("⚙ "+m.name(t.AgentID)+" "+t.Tool) + " " + m.th.DimText.Render(compactJSON(t.Input, 60))
		out := wrap.Render(line) + "\n"
		switch t.Status {
		case reduce.ToolRunning:
			out += wrap.Render("        "+m.th.DimText.Render("… running")) + "\n"
		case reduce.ToolDone:
			if r := firstLines(t.Result, resultCap); r != "" {
				out += wrap.Render("        "+m.th.ToolOK.Render("✓ ")+m.th.ToolResult.Render(r)) + "\n"
			}
		case reduce.ToolErr:
			out += wrap.Render("        "+m.th.ToolErr.Render("✗ ")+m.th.ToolResult.Render(firstLines(t.Result, resultCap))) + "\n"
		}
		return out
	case reduce.TurnError:
		return wrap.Render(head+m.th.ErrorBanner.Render("✗ "+m.name(t.AgentID)+" "+t.Text)) + "\n"
	case reduce.TurnSystem:
		return wrap.Render(head+m.th.Draining.Render("⚡ "+t.Text)) + "\n"
	}
	return ""
}

// compactJSON squeezes a tool input into one dim inline snippet.
func compactJSON(raw json.RawMessage, limit int) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}

// firstLines caps a tool result at n lines / ~240 chars for the stream.
func firstLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		extra := len(lines) - n
		lines = append(lines[:n], fmt.Sprintf("… (+%d lines)", extra))
	}
	out := strings.Join(lines, "\n        ")
	if len(out) > 240 {
		out = out[:240] + "…"
	}
	return out
}

// phaseStyle picks the pill color by the web's phaseClass vocabulary.
func (m *Model) phaseStyle(mem reduce.Member) lipgloss.Style {
	if mem.Run == "suspended" || mem.Membership == "frozen" {
		return m.th.DimText
	}
	switch mem.Phase {
	case "waiting-approval", "waiting-input":
		return m.th.Compacting // yellow — operator must act
	case "error", "paused":
		return m.th.ToolErr
	case "executing":
		return m.th.ToolCall
	case "running", "thinking", "texting":
		return m.th.BannerInfo
	}
	if mem.Run == "busy" {
		return m.th.BannerInfo
	}
	return m.th.DimText
}

// rosterPane renders the ordered member list with attention badges.
func (m *Model) rosterPane(h int) string {
	rows := []string{m.th.PanelHeader.Render("ROSTER")}
	ms := m.members()
	for i, mem := range ms {
		if len(rows) >= h {
			break
		}
		cursor := "  "
		if i == m.sel {
			cursor = m.th.BannerInfo.Render("▸ ")
		}
		mark := " "
		if mem.Name == m.focus {
			mark = m.th.PanelHeader.Render("●")
		}
		badge := ""
		switch reduce.AttentionKind(mem) {
		case "act":
			badge = m.th.Compacting.Render("✋")
		case "warn":
			badge = m.th.ToolErr.Render("⚠")
		default:
			if m.gateFor(mem.Name) != nil {
				badge = m.th.Compacting.Render("✋")
			}
		}
		phase := reduce.DisplayPhase(mem)
		el := reduce.Elapsed(mem.PhaseSince, m.now)
		row := fmt.Sprintf("%s%s%-10s %s %s", cursor, mark, trunc(mem.Name, 10),
			m.phaseStyle(mem).Render(trunc(phase, 12)), m.th.DimText.Render(el))
		if badge != "" {
			row += " " + badge
		}
		rows = append(rows, trunc(row, leftWidth))
	}
	return pad(rows, h)
}

// taskGlyph maps a task status to the board's compact glyph.
func taskGlyph(status string) string {
	switch status {
	case "pending":
		return "▢"
	case "blocked":
		return "⛓"
	case "running":
		return "▶"
	case "suspended":
		return "◫"
	case "verifying":
		return "?"
	case "completed":
		return "▣"
	}
	return "·"
}

// tasksPane renders the compact read-only task list.
func (m *Model) tasksPane(h int) string {
	title := "TASKS"
	if m.tasks.Total > 0 {
		title = fmt.Sprintf("TASKS · %d done", m.tasks.Total)
	}
	rows := []string{m.th.PanelHeader.Render(title)}
	for _, t := range m.tasks.Tasks {
		if len(rows) >= h {
			break
		}
		if t.Status == "completed" {
			continue // the pane is the ACTIVE board; the count carries done
		}
		row := fmt.Sprintf("%s #%d %s %s", taskGlyph(t.Status), t.ID,
			trunc(t.Title, 12), m.th.DimText.Render(trunc(t.Assignee, 8)))
		rows = append(rows, trunc(row, leftWidth))
	}
	if len(rows) == 1 {
		rows = append(rows, m.th.DimText.Render("  (no active tasks)"))
	}
	return pad(rows, h)
}

// header renders the top line: space identity + connection state.
func (m *Model) header() string {
	conn := m.th.TasksDone.Render("● live")
	if !m.connected {
		conn = m.th.Compacting.Render(fmt.Sprintf("↻ reconnecting (%d)…", max(m.reconnectN, 1)))
	}
	name := m.spaceName
	if name == "" {
		name = m.spaceID
	}
	left := m.th.PanelHeader.Render("⚡ "+name) + m.th.DimText.Render(fmt.Sprintf(" · %d members", len(m.roster)))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(conn)-1, 1)
	return left + strings.Repeat(" ", gap) + conn
}

// statusBar renders the bottom line: toast (or key hints).
func (m *Model) statusBar() string {
	hint := "↑↓ select · enter focus · a all · m message · : run · g gates · s/r/f/u verbs · H halt · q detach"
	body := m.toast
	if body == "" {
		body = hint
	}
	return m.th.FooterHint.Render(trunc(body, max(m.width-1, 10)))
}

// composerLine renders the input (when composing) or the focus hint.
func (m *Model) composerLine() string {
	if m.compose != composeOff {
		return m.input.View()
	}
	who := m.focus
	if who == "" {
		who = "all members"
	}
	extra := ""
	if n := len(m.gates); n > 0 {
		extra = m.th.Compacting.Render(fmt.Sprintf(" · ✋ %d gate(s) — g opens", n))
	}
	return m.th.DimText.Render("stream: "+who) + extra
}

// View assembles the frame.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return "terminal too small for attach — need at least 40×8\n"
	}
	bodyH := max(m.height-titleLines-statusLine, 3)
	rosterH := max(bodyH*3/5, 3)
	tasksH := max(bodyH-rosterH, 2)

	left := lipgloss.JoinVertical(lipgloss.Left, m.rosterPane(rosterH), m.tasksPane(tasksH))

	var right string
	if m.overlay != nil {
		right = m.overlayView(m.vp.Width, bodyH)
	} else {
		right = lipgloss.JoinVertical(lipgloss.Left, m.streamTitle(), m.vp.View(), m.composerLine())
	}
	if m.confirmHalt {
		right = lipgloss.Place(m.vp.Width, bodyH, lipgloss.Center, lipgloss.Center,
			m.th.InputBorder.Render(m.th.ErrorBanner.Render("halt ALL in-flight runs?")+"\n\npress y to confirm — any other key cancels"))
	}

	leftCol := lipgloss.NewStyle().Width(leftWidth).Height(bodyH).Render(left)
	rightCol := lipgloss.NewStyle().Width(m.vp.Width).Height(bodyH).Render(right)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, " ", rightCol)

	return lipgloss.JoinVertical(lipgloss.Left, m.header(), body, m.statusBar())
}

func (m *Model) streamTitle() string {
	who := m.focus
	if who == "" {
		who = "all"
	}
	return m.th.PanelHeader.Render("STREAM · " + who)
}

// overlayView renders the active gate as a centered answerable box.
func (m *Model) overlayView(w, h int) string {
	o := m.overlay
	inner := max(min(w-6, 70), 24)
	var b strings.Builder

	if o.isQuestion() {
		q := o.ev.QuestionNeeded
		b.WriteString(m.th.Compacting.Render("? question — "+o.member) + "\n\n")
		for qi, item := range q.Questions {
			marker := "  "
			if qi == o.q {
				marker = m.th.BannerInfo.Render("▸ ")
			}
			b.WriteString(marker + m.th.PanelRow.Render(item.Question) + "\n")
			if qi != o.q {
				continue
			}
			for oi, opt := range item.Options {
				cursor := "  "
				if oi == o.qCursor {
					cursor = m.th.BannerInfo.Render("› ")
				}
				box := "( )"
				if item.MultiSelect {
					box = "[ ]"
				}
				if o.chosen[qi][oi] {
					box = "(•)"
					if item.MultiSelect {
						box = "[x]"
					}
				}
				line := cursor + box + " " + opt.Label
				if opt.Description != "" {
					line += m.th.DimText.Render(" — " + trunc(opt.Description, 40))
				}
				b.WriteString("   " + line + "\n")
			}
		}
		b.WriteString("\n" + m.th.FooterHint.Render("space pick · tab next question · enter submit · esc close"))
	} else {
		p := o.ev.ApprovalNeeded
		b.WriteString(m.th.Compacting.Render("✋ approval — "+o.member+" wants "+p.ToolName) + "\n\n")
		if p.InputDescription != "" {
			b.WriteString(m.th.PanelRow.Render(trunc(p.InputDescription, inner)) + "\n")
		}
		if p.Reason != "" {
			b.WriteString(m.th.DimText.Render("reason: "+trunc(p.Reason, inner-8)) + "\n")
		}
		if p.RiskHint != "" {
			b.WriteString(m.th.ErrorBanner.Render("risk: "+trunc(p.RiskHint, inner-6)) + "\n")
		}
		b.WriteString("\n")
		for i, a := range approvalActions {
			cursor := "  "
			if i == o.cursor {
				cursor = m.th.BannerInfo.Render("▸ ")
			}
			label := a
			if i == 1 {
				label = a + " " + p.ToolName
			}
			b.WriteString(cursor + label + "\n")
		}
		b.WriteString("\n" + m.th.FooterHint.Render("enter answer · esc close (gate stays pending)"))
	}
	if o.err != "" {
		b.WriteString("\n" + m.th.ErrorBanner.Render("✗ "+o.err))
	}
	if o.sent {
		b.WriteString("\n" + m.th.DimText.Render("reply sent — waiting for the member to move…"))
	}
	box := m.th.InputBorder.Render(lipgloss.NewStyle().Width(inner).Render(b.String()))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

// trunc hard-caps a string's display width.
func trunc(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// pad extends rows to exactly h lines.
func pad(rows []string, h int) string {
	for len(rows) < h {
		rows = append(rows, "")
	}
	return strings.Join(rows[:h], "\n")
}
