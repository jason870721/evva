// Package app is the attach TUI (`evva swarm attach`): a Bubble Tea cockpit
// over one running space. It hydrates from the durable /chatlog + /pending,
// folds the live WebSocket feed through the reduce package (the Go port of
// the web console's reducers), and gives the terminal operator the four
// things that matter mid-run: the roster's attention state, any member's
// stream, answerable gates, and a composer. The web remains the rich
// workstation (membership, schedules, skills, memory); this is the cockpit —
// scope-fenced by the TUI-attach PRD §2.
package app

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/swarm/tui/client"
	"github.com/johnny1110/evva/internal/swarm/tui/reduce"
	"github.com/johnny1110/evva/internal/swarm/tui/wire"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Poll cadences: the roster carries phase/attention so it polls like the
// web's reconciliation loop; tasks are read-only context (PRD: 5 s).
const (
	rosterPollEvery = 2 * time.Second
	tasksPollEvery  = 5 * time.Second
	chatlogLimit    = 400 // matches the web's TurnList render cap
)

// composerMode says what the input line is composing.
type composerMode int

const (
	composeOff        composerMode = iota
	composeMessage                 // message the focused/selected member
	composeCommand                 // ":" commands — :run <prompt>, :all <body>
	composeDenyReason              // gate deny → optional reason
)

// Model is the attach program's state. All mutation happens on the Bubble
// Tea update goroutine; the stream goroutine only feeds messages in.
type Model struct {
	cli    *client.Client
	stream *client.Stream
	th     *theme.Theme

	spaceID   string
	spaceName string

	// Wire-derived state.
	turns  []*reduce.Turn  // global folded conversation
	phases reduce.PhaseMap // agentID → live fine phase
	roster []webapi.MemberInfo
	tasks  webapi.TaskPage
	gates  []*wire.Event // pending gates, oldest first (reqID-deduped)

	// UI state.
	width, height int
	now           time.Time
	sel           int    // roster cursor
	focus         string // member whose stream fills the pane; "" = all
	follow        bool   // stream viewport pinned to tail
	vp            viewport.Model
	input         textinput.Model
	compose       composerMode
	overlay       *gateOverlay
	confirmHalt   bool
	connected     bool
	reconnectN    int
	toast         string // one-line transient notice (errors, receipts)

	quitting bool
}

// New assembles the model. The stream must already be dialing; the first
// hydrate rides Init.
func New(cli *client.Client, stream *client.Stream, space webapi.SpaceInfo) Model {
	ti := textinput.New()
	ti.CharLimit = 4000
	ti.Prompt = "> "
	return Model{
		cli: cli, stream: stream, th: theme.Default(),
		spaceID: space.ID, spaceName: space.Name,
		phases: reduce.PhaseMap{}, follow: true,
		now: time.Now(), vp: viewport.New(0, 0), input: ti,
	}
}

// --- messages -----------------------------------------------------------

type streamMsg client.Msg

type hydrateMsg struct {
	turns []*reduce.Turn
	gates []*wire.Event
	err   error
}

type rosterMsg struct {
	roster []webapi.MemberInfo
	err    error
}

type tasksMsg struct {
	page webapi.TaskPage
	err  error
}

type tickMsg time.Time

// actionMsg reports one fire-and-forget REST action (verb, message, halt).
type actionMsg struct {
	label string
	err   error
}

// --- commands -----------------------------------------------------------

func (m Model) waitStream() tea.Cmd {
	ch := m.stream.Messages()
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return tea.Quit()
		}
		return streamMsg(msg)
	}
}

// hydrate re-reads the durable state. It MERGES on the update side: a failed
// fetch keeps the current panes (the v1.7.4 non-destructive contract) — the
// error only lands in the toast.
func (m Model) hydrate() tea.Cmd {
	cli, space := m.cli, m.spaceID
	return func() tea.Msg {
		evs, err := cli.ChatLog(space, chatlogLimit)
		if err != nil {
			return hydrateMsg{err: err}
		}
		var turns []*reduce.Turn
		for _, ev := range evs {
			turns = reduce.Fold(turns, ev)
		}
		gates, err := cli.Pending(space)
		if err != nil {
			return hydrateMsg{err: err}
		}
		return hydrateMsg{turns: turns, gates: gates}
	}
}

func (m Model) fetchRoster() tea.Cmd {
	cli, space := m.cli, m.spaceID
	return func() tea.Msg {
		r, err := cli.Roster(space)
		return rosterMsg{roster: r, err: err}
	}
}

func (m Model) fetchTasks() tea.Cmd {
	cli, space := m.cli, m.spaceID
	return func() tea.Msg {
		p, err := cli.Tasks(space)
		return tasksMsg{page: p, err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func after(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

type rosterPollMsg struct{}
type tasksPollMsg struct{}

func (m Model) action(label string, do func() error) tea.Cmd {
	return func() tea.Msg { return actionMsg{label: label, err: do()} }
}

// Init starts the IO: hydrate, snapshots, the stream wait, and the clocks.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.hydrate(), m.fetchRoster(), m.fetchTasks(), m.waitStream(), tickCmd(),
		after(rosterPollEvery, rosterPollMsg{}), after(tasksPollEvery, tasksPollMsg{}),
	)
}

// --- update -------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.refreshStream()
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		return m, tickCmd()

	case rosterPollMsg:
		return m, tea.Batch(m.fetchRoster(), after(rosterPollEvery, rosterPollMsg{}))
	case tasksPollMsg:
		return m, tea.Batch(m.fetchTasks(), after(tasksPollEvery, tasksPollMsg{}))

	case hydrateMsg:
		if msg.err != nil {
			m.toast = "hydrate: " + msg.err.Error() // keep current panes — never blank
			return m, nil
		}
		m.turns = msg.turns
		m.setGates(msg.gates)
		m.refreshStream()
		return m, nil

	case rosterMsg:
		if msg.err == nil {
			m.roster = msg.roster
			if m.sel >= len(m.roster) {
				m.sel = max(len(m.roster)-1, 0)
			}
		}
		return m, nil

	case tasksMsg:
		if msg.err == nil {
			m.tasks = msg.page
		}
		return m, nil

	case streamMsg:
		return m.onStream(client.Msg(msg))

	case actionMsg:
		if msg.err != nil {
			m.toast = msg.label + ": " + msg.err.Error()
		} else {
			m.toast = msg.label + " ✓"
		}
		return m, m.fetchRoster()

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// onStream folds one live emission.
func (m Model) onStream(msg client.Msg) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitStream()}
	switch {
	case msg.Status != nil:
		m.connected = msg.Status.Connected
		m.reconnectN = msg.Status.Attempt
		if msg.Status.Connected {
			// Every (re)connect re-reads the durable state and MERGES — events
			// emitted while the socket was down are not replayed on the wire.
			cmds = append(cmds, m.hydrate(), m.fetchRoster(), m.fetchTasks())
		}
	case msg.CmdErr != nil:
		// A gate reply that failed to route: surface it, re-open the gate
		// list from the server, and let the overlay show the error.
		m.toast = "command failed: " + msg.CmdErr.Message
		if m.overlay != nil {
			m.overlay.err = msg.CmdErr.Message
			m.overlay.sent = false
		}
		cmds = append(cmds, m.hydrate())
	case msg.Event != nil:
		ev := msg.Event
		m.turns = reduce.Fold(m.turns, ev)
		reduce.ReducePhase(m.phases, ev, m.now)
		switch ev.Kind {
		case event.KindApprovalNeeded, event.KindQuestionNeeded:
			m.addGate(ev)
			if m.overlay == nil {
				m.overlay = newGateOverlay(ev, m.memberOf(ev.AgentID))
			}
		case event.KindStoreUpdate, event.KindToolUseResult:
			// touchesLedger: refresh the REST snapshots promptly.
			cmds = append(cmds, m.fetchTasks(), m.fetchRoster())
		}
		m.pruneGates(ev)
		m.refreshStream()
	}
	return m, tea.Batch(cmds...)
}

// setGates replaces the pending set (hydrate truth), dropping the overlay if
// its gate is no longer pending (someone else answered it).
func (m *Model) setGates(gates []*wire.Event) {
	m.gates = nil
	for _, g := range gates {
		m.addGate(g)
	}
	if m.overlay != nil && !m.overlay.sent && m.findGate(m.overlay.reqID) == nil {
		m.overlay = nil
	}
}

func gateReqID(ev *wire.Event) string {
	if ev == nil {
		return ""
	}
	if ev.ApprovalNeeded != nil {
		return ev.ApprovalNeeded.RequestID
	}
	if ev.QuestionNeeded != nil {
		return ev.QuestionNeeded.RequestID
	}
	return ""
}

func (m *Model) addGate(ev *wire.Event) {
	id := gateReqID(ev)
	if id == "" {
		return
	}
	for _, g := range m.gates {
		if gateReqID(g) == id {
			return
		}
	}
	m.gates = append(m.gates, ev)
}

func (m *Model) findGate(reqID string) *wire.Event {
	for _, g := range m.gates {
		if gateReqID(g) == reqID {
			return g
		}
	}
	return nil
}

// pruneGates drops a member's pending gates when its run moved past them —
// the run-terminal pruning the web applies (a gate answered elsewhere, or a
// run that ended, stops beaconing).
func (m *Model) pruneGates(ev *wire.Event) {
	switch ev.Kind {
	case event.KindRunEnd, event.KindRunCancelled, event.KindError, event.KindIdle:
	default:
		return
	}
	kept := m.gates[:0]
	for _, g := range m.gates {
		if g.AgentID != ev.AgentID {
			kept = append(kept, g)
		}
	}
	m.gates = kept
	if m.overlay != nil && !m.overlay.sent && m.findGate(m.overlay.reqID) == nil {
		m.overlay = nil
	}
}

// memberOf resolves an agent id to its member name ("" when unknown).
func (m *Model) memberOf(agentID string) string {
	for _, r := range m.roster {
		if r.AgentID == agentID {
			return r.Name
		}
	}
	return ""
}

// agentOf resolves a member name to its live agent id.
func (m *Model) agentOf(name string) string {
	for _, r := range m.roster {
		if r.Name == name {
			return r.AgentID
		}
	}
	return ""
}

// members converts the REST roster + live phase overlay into the reduce
// package's Member shape, ordered for the pane.
func (m *Model) members() []reduce.Member {
	out := make([]reduce.Member, 0, len(m.roster))
	for _, r := range m.roster {
		mem := reduce.Member{
			Name: r.Name, AgentID: r.AgentID, Role: r.Role,
			Run: r.Run, Membership: r.Membership,
			Phase: r.Phase, Tool: r.Tool,
		}
		if r.PhaseSince > 0 {
			mem.PhaseSince = time.UnixMilli(r.PhaseSince)
		}
		if lp, ok := m.phases[r.AgentID]; ok {
			mem.Phase, mem.Tool, mem.PhaseSince = lp.Phase, lp.Tool, lp.Since
		}
		out = append(out, mem)
	}
	att := reduce.AttentionItems(out, m.now)
	order := make([]string, len(att))
	for i, a := range att {
		order[i] = a.Name
	}
	return reduce.OrderRoster(out, order)
}

// selectedMember returns the roster-pane cursor's member name.
func (m *Model) selectedMember() string {
	ms := m.members()
	if len(ms) == 0 || m.sel >= len(ms) {
		return ""
	}
	return ms[m.sel].Name
}
