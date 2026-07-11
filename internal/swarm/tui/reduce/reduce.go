// Package reduce is the Go port of the web console's event reducers
// (web2/src/lib/events.ts) — the semantics that turn the swarm's wire events
// into renderable state. The duplication is deliberate and fenced by a
// contract (the TUI-attach PRD §4): the golden fixtures in testdata/ pin this
// package to events.ts behavior, so the two clients can only drift loudly (a
// failing golden), never silently. Anything changed here must be changed
// there, and vice versa — the /chatlog replay is the shared upstream both
// fold.
package reduce

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
	"github.com/johnny1110/evva/pkg/event"
)

// TurnType tags one folded turn — the Go spelling of events.ts's Turn union.
type TurnType string

const (
	TurnAssistant TurnType = "assistant"
	TurnThinking  TurnType = "thinking"
	TurnTool      TurnType = "tool"
	TurnError     TurnType = "error"
	TurnUser      TurnType = "user"
	TurnSystem    TurnType = "system" // engine lines: dispatches, checks, alerts, blackboard
)

// ToolStatus is a tool turn's lifecycle.
type ToolStatus string

const (
	ToolRunning ToolStatus = "running"
	ToolDone    ToolStatus = "done"
	ToolErr     ToolStatus = "error"
)

// Turn is one rendered unit of the conversation. At is the turn's wall-clock
// instant — for a streaming turn the time of its FIRST chunk — and stays zero
// for turns folded from a timeless event (the UI then shows no stamp),
// mirroring events.ts's optional `at`.
type Turn struct {
	Type    TurnType
	AgentID string
	Text    string
	Open    bool // streaming turn still accumulating
	Target  string
	Tool    string
	ToolID  string
	Input   json.RawMessage
	Status  ToolStatus
	Result  string
	At      time.Time
}

// textOf / thinkingOf pull the renderable delta out of a text(-chunk) event.
func textOf(ev *wire.Event) string {
	if ev != nil && ev.Text != nil {
		return ev.Text.Text
	}
	return ""
}

func thinkingOf(ev *wire.Event) string {
	if ev != nil && ev.Thinking != nil {
		return ev.Thinking.Text
	}
	return ""
}

// closeAgentOpen closes ONE agent's open streaming turns, leaving others'
// in-flight turns open (members stream concurrently on one feed).
func closeAgentOpen(turns []*Turn, agent string) {
	for _, t := range turns {
		if (t.Type == TurnAssistant || t.Type == TurnThinking) && t.Open && t.AgentID == agent {
			t.Open = false
		}
	}
}

// agentOpenTurn returns an agent's currently-streaming open text/thinking
// turn, scanning from the end so it skips other agents' interleaved turns.
func agentOpenTurn(turns []*Turn, agent string) *Turn {
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		if t.AgentID == agent && (t.Type == TurnAssistant || t.Type == TurnThinking) && t.Open {
			return t
		}
	}
	return nil
}

// appendChunk folds a delta into the agent's open turn of `typ`, opening a
// fresh one when needed (closing the agent's other open streaming turn
// first). `at` stamps only a freshly-opened turn — a continuing turn keeps
// its first-chunk time.
func appendChunk(turns []*Turn, agent string, typ TurnType, text string, at time.Time) []*Turn {
	if text == "" {
		return turns
	}
	open := agentOpenTurn(turns, agent)
	if open != nil && open.Type == typ {
		open.Text += text
		return turns
	}
	if open != nil {
		open.Open = false
	}
	return append(turns, &Turn{Type: typ, AgentID: agent, Text: text, Open: true, At: at})
}

// Fold folds one wire event into the turn list, in place, returning it — the
// Go twin of events.ts reduceChat. Streaming deltas coalesce into the
// EMITTING agent's own open turn (members interleave); tool calls become
// turns and resolve by ToolID; unknown kinds are ignored (forward-compatible
// by construction).
func Fold(turns []*Turn, ev *wire.Event) []*Turn {
	if ev == nil || ev.Kind == "" {
		return turns
	}
	agent := ev.AgentID
	at := ev.At()

	switch ev.Kind {
	case event.KindText, event.KindTextChunk:
		return appendChunk(turns, agent, TurnAssistant, textOf(ev), at)
	case event.KindThinking, event.KindThinkingChunk:
		return appendChunk(turns, agent, TurnThinking, thinkingOf(ev), at)
	case event.KindToolUseStart:
		closeAgentOpen(turns, agent)
		t := &Turn{Type: TurnTool, AgentID: agent, Status: ToolRunning, At: at}
		if p := ev.ToolUseStart; p != nil {
			t.Tool, t.ToolID, t.Input = p.Name, p.ToolID, p.Input
		}
		return append(turns, t)
	case event.KindToolUseResult:
		if p := ev.ToolUseResult; p != nil {
			for _, t := range turns {
				if t.Type == TurnTool && t.ToolID == p.ToolID {
					t.Status = ToolDone
					if p.IsError {
						t.Status = ToolErr
					}
					t.Result = p.Content
					break
				}
			}
		}
		return turns
	case event.KindError:
		closeAgentOpen(turns, agent)
		msg := "error"
		if ev.Error != nil && ev.Error.Message != "" {
			msg = ev.Error.Message
		}
		return append(turns, &Turn{Type: TurnError, AgentID: agent, Text: msg, At: at})
	case wire.KindUserMessage:
		// Synthetic kind on the /chatlog replay wire only: an operator mail
		// folded back into the conversation (target = member name, or "all").
		p := ev.UserMessage
		if p == nil {
			return turns
		}
		text := p.Body
		if p.Subject != "" {
			text = p.Subject + " — " + p.Body
		}
		if text == "" {
			return turns
		}
		return append(turns, &Turn{Type: TurnUser, Target: p.Recipient, Text: text, At: at})
	case wire.KindTaskDispatched, wire.KindMemberSpawned, wire.KindMemberRetired,
		wire.KindTaskCheckDone, wire.KindOpsAlert, wire.KindBlackboardUpdated:
		// Engine lines: the space acting on leader-declared structure, check
		// results, ops alerts, blackboard rewrites. TextPayload carries the
		// narration.
		closeAgentOpen(turns, agent)
		text := textOf(ev)
		if text == "" {
			return turns
		}
		return append(turns, &Turn{Type: TurnSystem, AgentID: agent, Text: text, At: at})
	case event.KindTurnEnd, event.KindRunEnd:
		closeAgentOpen(turns, agent)
		return turns
	default:
		return turns
	}
}

// ConsoleTurns selects one member's turns: that member's own agent turns (by
// AgentID) plus the operator's outgoing messages addressed to it (by member
// name) — events.ts consoleTurns.
func ConsoleTurns(turns []*Turn, agentID, member string) []*Turn {
	out := make([]*Turn, 0, len(turns))
	for _, t := range turns {
		if t.Type == TurnUser {
			if t.Target == member {
				out = append(out, t)
			}
			continue
		}
		if agentID != "" && t.AgentID == agentID {
			out = append(out, t)
		}
	}
	return out
}

// Clock formats a turn instant as local HH:MM:SS — the per-line timestamp.
// Empty for a zero instant so the renderer can omit it.
func Clock(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	lt := at.Local()
	return fmt.Sprintf("%02d:%02d:%02d", lt.Hour(), lt.Minute(), lt.Second())
}
