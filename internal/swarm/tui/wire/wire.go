// Package wire holds the attach TUI's wire-protocol types: the JSON shapes
// the swarm service pushes over its WebSocket and /chatlog replay, and the
// inbound command envelope the socket accepts. These mirror what the web
// console consumes (web2/src/types/events.ts) — the TUI is deliberately a
// second client of the SAME protocol, so the shapes here are decode targets
// for payloads produced elsewhere (pkg/event.Event's default PascalCase
// marshaling, service.wireEvent's {spaceId, event} envelope, webapi's
// wsCommand), never a new dialect.
package wire

import (
	"encoding/json"
	"time"

	"github.com/johnny1110/evva/pkg/event"
)

// Event is one wire event as both the live WS and the /chatlog replay carry
// it. It embeds pkg/event.Event — the service marshals that struct verbatim —
// and adds the chatlog-only synthetic user_message payload (an operator mail
// folded back into the conversation; see service/chatlog.go userMessage).
type Event struct {
	event.Event
	UserMessage *UserMessagePayload `json:",omitempty"`
}

// UserMessagePayload mirrors service/chatlog.go's userMessagePayload: one
// operator mail on the replay wire. Recipient may be "all" for a broadcast.
type UserMessagePayload struct {
	Sender    string
	Recipient string
	Subject   string `json:",omitempty"`
	Body      string
}

// Kinds the service emits that pkg/event does not define — space-level
// engine synthetics (DWF/CHK/NTF/BB) and the chatlog's user_message. The
// reducer treats unknown kinds as ignorable, so this list is display
// vocabulary, not a gate.
const (
	KindUserMessage       = event.Kind("user_message")
	KindTaskDispatched    = event.Kind("task_dispatched")
	KindMemberSpawned     = event.Kind("member_spawned")
	KindMemberRetired     = event.Kind("member_retired")
	KindTaskCheckDone     = event.Kind("task_check_done")
	KindOpsAlert          = event.Kind("ops_alert")
	KindBlackboardUpdated = event.Kind("blackboard_updated")
)

// Frame is the live socket's outbound envelope (service.wireEvent):
// {"spaceId": "...", "event": {...}}. Event stays raw so a command_error
// frame (which has no "event") can be told apart before decoding.
type Frame struct {
	SpaceID string          `json:"spaceId"`
	Event   json.RawMessage `json:"event"`
}

// CommandError is the socket's error echo for an inbound command that failed
// to apply (webapi.commandErrorFrame): {"type":"command_error", ...}. The
// reqId names the gate whose reply was lost, so the UI can re-open it.
type CommandError struct {
	Type    string `json:"type"`
	ReqID   string `json:"reqId"`
	Message string `json:"message"`
}

// Command is the inbound envelope the socket accepts (webapi.wsCommand):
// type run | respond_permission | respond_question. Lifecycle verbs go over
// REST, exactly like the web console.
type Command struct {
	Type     string              `json:"type"`
	Agent    string              `json:"agent,omitempty"`
	Prompt   string              `json:"prompt,omitempty"`
	ReqID    string              `json:"reqId,omitempty"`
	Behavior string              `json:"behavior,omitempty"`
	Reason   string              `json:"reason,omitempty"`
	RuleTool string              `json:"ruleTool,omitempty"`
	Answers  map[string][]string `json:"answers,omitempty"`
}

// ParseFrame decodes one raw socket payload into either an Event (the usual
// case) or a CommandError. Unparseable frames return (nil, nil) — the
// forward-compatibility contract: an unknown shape is ignored, never fatal.
func ParseFrame(raw []byte) (*Event, *CommandError) {
	// command_error is the only frame with a "type" discriminator.
	var ce CommandError
	if err := json.Unmarshal(raw, &ce); err == nil && ce.Type == "command_error" {
		return nil, &ce
	}
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Event) == 0 {
		return nil, nil
	}
	return ParseEvent(f.Event)
}

// ParseEvent decodes one bare event object (the /chatlog array element shape).
func ParseEvent(raw []byte) (*Event, *CommandError) {
	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil || ev.Kind == "" {
		return nil, nil
	}
	return &ev, nil
}

// At returns the event's emit instant, zero when the wire carried none — the
// reducer then leaves the turn unstamped (events.ts eventAt keeps `at`
// undefined for timeless fixtures; zero time.Time is the Go twin).
func (e *Event) At() time.Time {
	return e.Time
}
