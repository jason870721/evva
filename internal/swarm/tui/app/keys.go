package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
)

// onKey routes one key press by mode: composer > halt confirm > gate overlay
// > global keys. q detaches (never stops the space).
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.compose != composeOff {
		return m.composerKey(msg)
	}
	if m.confirmHalt {
		switch msg.String() {
		case "y", "Y":
			m.confirmHalt = false
			return m, m.action("halt-all", func() error { return m.cli.HaltAll(m.spaceID) })
		default:
			m.confirmHalt = false
			return m, nil
		}
	}
	if m.overlay != nil {
		return m.gateKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		m.stream.Close()
		return m, tea.Quit

	case "up", "k":
		m.sel = max(m.sel-1, 0)
		return m, nil
	case "down", "j":
		m.sel = min(m.sel+1, max(len(m.roster)-1, 0))
		return m, nil

	case "enter":
		// Focus the selected member's stream (and auto-open its gate).
		if name := m.selectedMember(); name != "" {
			m.focus = name
			m.follow = true
			m.refreshStream()
			if g := m.gateFor(name); g != nil {
				m.overlay = newGateOverlay(g, name)
			}
		}
		return m, nil

	case "a":
		m.focus = "" // all-members interleaved view
		m.follow = true
		m.refreshStream()
		return m, nil

	case "g":
		m.openOldestGate()
		return m, nil

	case "m":
		target := m.composeTarget()
		if target == "" {
			m.toast = "no member selected"
			return m, nil
		}
		m.compose = composeMessage
		m.input.Placeholder = "message " + target + " (enter = send, esc = cancel)"
		m.input.SetValue("")
		m.input.Focus()
		return m, nil

	case ":":
		m.compose = composeCommand
		m.input.Placeholder = ":run <prompt> — start a leader turn"
		m.input.SetValue("")
		m.input.Focus()
		return m, nil

	case "s", "r", "f", "u":
		verb := map[string]string{"s": "suspend", "r": "resume", "f": "freeze", "u": "unfreeze"}[msg.String()]
		name := m.composeTarget()
		if name == "" {
			m.toast = "no member selected"
			return m, nil
		}
		return m, m.action(verb+" "+name, func() error { return m.cli.Verb(m.spaceID, name, verb) })

	case "H":
		m.confirmHalt = true
		return m, nil

	case "pgup", "b":
		m.follow = false
		m.vp.HalfPageUp()
		return m, nil
	case "pgdown", " ":
		m.vp.HalfPageDown()
		if m.vp.AtBottom() {
			m.follow = true
		}
		return m, nil
	case "end", "G":
		m.follow = true
		m.vp.GotoBottom()
		return m, nil
	}
	return m, nil
}

// composeTarget is who a message/verb applies to: the focused member first,
// else the roster selection.
func (m *Model) composeTarget() string {
	if m.focus != "" {
		return m.focus
	}
	return m.selectedMember()
}

// composerKey drives the input line for all composer modes.
func (m Model) composerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		mode := m.compose
		m.compose = composeOff
		m.input.Blur()
		if mode == composeDenyReason && m.overlay != nil {
			return m, nil // back to the overlay, gate still pending
		}
		return m, nil

	case "enter":
		text := strings.TrimSpace(m.input.Value())
		mode := m.compose
		m.compose = composeOff
		m.input.Blur()
		m.input.SetValue("")
		switch mode {
		case composeMessage:
			target := m.composeTarget()
			if text == "" || target == "" {
				return m, nil
			}
			return m, m.action("message → "+target, func() error { return m.cli.Message(m.spaceID, target, text) })
		case composeCommand:
			return m.runCommand(text)
		case composeDenyReason:
			if m.overlay == nil {
				return m, nil
			}
			return m.sendGateReply(wire.Command{
				Type: "respond_permission", Agent: m.overlay.member,
				ReqID: m.overlay.reqID, Behavior: "deny", Reason: text,
			})
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// runCommand executes a ":" composer command. :run starts a leader turn over
// the socket (the web composer's run); :all broadcasts an operator mail.
func (m Model) runCommand(text string) (tea.Model, tea.Cmd) {
	cmd, rest, _ := strings.Cut(strings.TrimPrefix(text, ":"), " ")
	rest = strings.TrimSpace(rest)
	switch cmd {
	case "run":
		if rest == "" {
			m.toast = "usage: :run <prompt>"
			return m, nil
		}
		leader := ""
		for _, r := range m.roster {
			if r.Role == "leader" {
				leader = r.Name
				break
			}
		}
		if leader == "" {
			m.toast = "no leader in roster"
			return m, nil
		}
		if err := m.stream.Send(wire.Command{Type: "run", Agent: leader, Prompt: rest}); err != nil {
			m.toast = "run: " + err.Error()
			return m, nil
		}
		m.toast = "run → " + leader
		return m, nil
	case "all":
		if rest == "" {
			m.toast = "usage: :all <body>"
			return m, nil
		}
		return m, m.action("broadcast", func() error { return m.cli.Message(m.spaceID, "all", rest) })
	case "":
		return m, nil
	default:
		m.toast = "unknown command :" + cmd
		return m, nil
	}
}
