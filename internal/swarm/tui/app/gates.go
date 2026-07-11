package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
)

// gateOverlay is the answerable overlay for one approval or question gate —
// the TUI twin of the web's gate modals, sending the same WS commands.
type gateOverlay struct {
	ev     *wire.Event
	reqID  string
	member string // display name (agent id when unresolved)

	// Approval state: cursor over the action list.
	cursor int

	// Question state: one cursor + chosen set per question.
	q       int // active question index
	qCursor int
	chosen  []map[int]bool // per question: option index → picked

	err  string // command_error echo — reopens with the error visible
	sent bool   // reply dispatched, awaiting the unblock (or an error echo)
}

// approvalActions is the fixed action list: approve once, always-allow the
// tool (the web's session rule), deny with an optional reason.
var approvalActions = []string{"approve", "always allow", "deny…"}

func newGateOverlay(ev *wire.Event, member string) *gateOverlay {
	if member == "" {
		member = ev.AgentID
	}
	o := &gateOverlay{ev: ev, reqID: gateReqID(ev), member: member}
	if q := ev.QuestionNeeded; q != nil {
		o.chosen = make([]map[int]bool, len(q.Questions))
		for i := range o.chosen {
			o.chosen[i] = map[int]bool{}
		}
	}
	return o
}

func (o *gateOverlay) isQuestion() bool { return o.ev.QuestionNeeded != nil }

// gateKey handles a key while the overlay is open. It returns the reply
// command to send (nil = keep editing) and whether the overlay closes.
func (m Model) gateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.overlay
	switch msg.String() {
	case "esc":
		m.overlay = nil // leaves the gate pending; the beacon keeps burning
		return m, nil
	}
	if o.sent {
		return m, nil // reply in flight — only esc applies
	}
	if o.isQuestion() {
		return m.questionKey(msg)
	}
	return m.approvalKey(msg)
}

func (m Model) approvalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.overlay
	switch msg.String() {
	case "up", "k":
		o.cursor = max(o.cursor-1, 0)
	case "down", "j":
		o.cursor = min(o.cursor+1, len(approvalActions)-1)
	case "enter":
		p := o.ev.ApprovalNeeded
		cmd := wire.Command{Type: "respond_permission", Agent: o.member, ReqID: o.reqID}
		switch o.cursor {
		case 0:
			cmd.Behavior = "allow"
		case 1:
			cmd.Behavior = "allow"
			cmd.RuleTool = p.ToolName
		case 2:
			// deny → optional reason via the composer, then send.
			m.compose = composeDenyReason
			m.input.Placeholder = "deny reason (enter = send, esc = cancel)"
			m.input.SetValue("")
			m.input.Focus()
			return m, nil
		}
		return m.sendGateReply(cmd)
	}
	return m, nil
}

func (m Model) questionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.overlay
	qs := o.ev.QuestionNeeded.Questions
	if len(qs) == 0 {
		m.overlay = nil
		return m, nil
	}
	q := qs[o.q]
	switch msg.String() {
	case "up", "k":
		o.qCursor = max(o.qCursor-1, 0)
	case "down", "j":
		o.qCursor = min(o.qCursor+1, len(q.Options)-1)
	case " ":
		if q.MultiSelect {
			o.chosen[o.q][o.qCursor] = !o.chosen[o.q][o.qCursor]
		} else {
			o.chosen[o.q] = map[int]bool{o.qCursor: true}
		}
	case "tab":
		o.q = (o.q + 1) % len(qs)
		o.qCursor = 0
	case "enter":
		// Single-select with nothing picked: enter picks the cursor row.
		if len(o.chosen[o.q]) == 0 {
			o.chosen[o.q][o.qCursor] = true
		}
		if o.q < len(qs)-1 {
			o.q++
			o.qCursor = 0
			return m, nil
		}
		answers := map[string][]string{}
		for i, q := range qs {
			var labels []string
			for j, opt := range q.Options {
				if o.chosen[i][j] {
					labels = append(labels, opt.Label)
				}
			}
			answers[q.Question] = labels
		}
		return m.sendGateReply(wire.Command{
			Type: "respond_question", Agent: o.member, ReqID: o.reqID, Answers: answers,
		})
	}
	return m, nil
}

// sendGateReply queues the WS command. The overlay stays up (sent=true) until
// the gate resolves — the member's phase leaving waiting-* prunes it — or a
// command_error reopens it with the error line.
func (m Model) sendGateReply(cmd wire.Command) (tea.Model, tea.Cmd) {
	o := m.overlay
	if err := m.stream.Send(cmd); err != nil {
		o.err = err.Error()
		return m, nil
	}
	o.sent = true
	o.err = ""
	m.dropGate(o.reqID)
	m.overlay = nil
	m.toast = "reply sent to " + o.member
	return m, nil
}

func (m *Model) dropGate(reqID string) {
	kept := m.gates[:0]
	for _, g := range m.gates {
		if gateReqID(g) != reqID {
			kept = append(kept, g)
		}
	}
	m.gates = kept
}

// openOldestGate opens the most-urgent pending gate (oldest first — they are
// appended in arrival order).
func (m *Model) openOldestGate() {
	if len(m.gates) == 0 {
		return
	}
	g := m.gates[0]
	m.overlay = newGateOverlay(g, m.memberOf(g.AgentID))
}

// gateFor reports a member's oldest pending gate, nil when none.
func (m *Model) gateFor(member string) *wire.Event {
	agentID := m.agentOf(member)
	for _, g := range m.gates {
		if g.AgentID == agentID || m.memberOf(g.AgentID) == member {
			return g
		}
	}
	return nil
}
