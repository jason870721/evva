package service

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/event"
)

// This file is the durable half of the web console's conversation view.
//
// The console used to rebuild itself from each member's LIVE LLM context
// (Transcript → ctl.Messages()) on every load and WS reconnect — but that
// context is a working buffer, not a record: auto-compaction rewrites it (a
// full compact leaves ONE user-role brief), and tool/thinking/user turns never
// survive the re-seed. The leader, compacted most often and speaking mostly in
// tool calls, lost nearly everything. The durable answer is the RP-17 event
// log: coalesce streamed turn text into it (turnCoalescer) and replay it as
// chat events (ChatLog) for the FE to fold through the same reducer the live
// WS feed uses.

// turnCoalescer folds streaming text/thinking deltas into whole-turn events
// for the durable event log. Streaming providers emit chunks only and skip the
// final whole-block KindText/KindThinking (the pkg/event contract), while the
// log skips chunks (RP-17) — so without coalescing the log holds no assistant
// text for streamed members, which is exactly what the chatlog replay needs.
// Owned and driven by the single per-space pump goroutine; no locking.
type turnCoalescer struct {
	pending map[string]*openBlock // agentID -> in-flight streamed block
}

// openBlock is one agent's in-flight streamed block: deltas of one kind
// accumulating since `first` — the first chunk's stamp, which becomes the
// synthetic's Time so a replayed turn sorts where it OPENED in the live view.
type openBlock struct {
	kind     event.Kind // KindText or KindThinking
	parentID string
	first    time.Time
	buf      strings.Builder
}

func newTurnCoalescer() *turnCoalescer {
	return &turnCoalescer{pending: make(map[string]*openBlock)}
}

// chunkBoundary reports whether kind closes an agent's open streamed block —
// the same boundaries the web reducer closes open turns on (tool dispatch,
// turn/run end, error) plus the whole-block text/thinking kinds buffered
// providers emit (for which the pending map is empty anyway).
func chunkBoundary(kind event.Kind) bool {
	switch kind {
	case event.KindToolUseStart, event.KindTurnEnd, event.KindRunEnd,
		event.KindRunCancelled, event.KindError, event.KindText, event.KindThinking:
		return true
	}
	return false
}

// fold consumes one event and returns any whole-turn synthetic(s) to log
// BEFORE it, so file order keeps a block ahead of the boundary that closed it.
func (c *turnCoalescer) fold(e event.Event) []event.Event {
	switch e.Kind {
	case event.KindTextChunk:
		return c.append(e, event.KindText)
	case event.KindThinkingChunk:
		return c.append(e, event.KindThinking)
	default:
		if chunkBoundary(e.Kind) {
			return c.flush(e.AgentID)
		}
		return nil
	}
}

// append folds one delta into the agent's open block of the target kind,
// flushing first on a text<->thinking switch (the reducer's block boundary).
func (c *turnCoalescer) append(e event.Event, kind event.Kind) []event.Event {
	var delta string
	switch {
	case kind == event.KindText && e.Text != nil:
		delta = e.Text.Text
	case kind == event.KindThinking && e.Thinking != nil:
		delta = e.Thinking.Text
	}
	if delta == "" {
		return nil
	}
	var out []event.Event
	b := c.pending[e.AgentID]
	if b != nil && b.kind != kind {
		out = c.flush(e.AgentID)
		b = nil
	}
	if b == nil {
		b = &openBlock{kind: kind, parentID: e.ParentID, first: e.Time}
		c.pending[e.AgentID] = b
	}
	b.buf.WriteString(delta)
	return out
}

// flush closes one agent's open block into a synthetic whole-turn event.
func (c *turnCoalescer) flush(agentID string) []event.Event {
	b := c.pending[agentID]
	if b == nil {
		return nil
	}
	delete(c.pending, agentID)
	text := b.buf.String()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	syn := event.Event{Kind: b.kind, AgentID: agentID, ParentID: b.parentID, Time: b.first}
	p := &event.TextPayload{Text: text}
	if b.kind == event.KindThinking {
		syn.Thinking = p
	} else {
		syn.Text = p
	}
	return []event.Event{syn}
}

// flushAll drains every open block — the pump's exit path, so a member cut off
// mid-stream still lands its partial text in the log.
func (c *turnCoalescer) flushAll() []event.Event {
	var out []event.Event
	for id := range c.pending {
		out = append(out, c.flush(id)...)
	}
	return out
}

// --- replay -----------------------------------------------------------------

// defaultChatLogLimit bounds a replay when the client passes none — matched to
// the FE TurnList render cap (400), with the tail being what it shows anyway.
const defaultChatLogLimit = 400

// maxChatLineBytes caps one log line during replay. Tool-result events carry
// full result bodies, so lines can be large; anything beyond this is a
// pathological line we skip rather than fail the whole replay on.
const maxChatLineBytes = 8 << 20

// chatEvent is one replayable wire event plus the instant it sorts by.
type chatEvent struct {
	raw json.RawMessage
	at  time.Time
}

// chatKind reports whether a logged event kind is part of the conversation the
// web console renders: exactly the kinds its reduceChat folds into visible
// turns (text/thinking blocks, tool cards, errors) plus the turn/run
// boundaries that close open blocks between them.
func chatKind(kind string) bool {
	switch event.Kind(kind) {
	case event.KindText, event.KindThinking, event.KindToolUseStart,
		event.KindToolUseResult, event.KindError, event.KindTurnEnd, event.KindRunEnd:
		return true
	}
	return false
}

// ChatLog satisfies webapi.Backend: replay the space's durable event log as an
// oldest-first list of chat-relevant wire events, with operator mail merged in
// as synthetic user_message events. The FE folds these through the same
// reducer as the live WS feed, so a rebuilt console matches what the live one
// showed — compaction can no longer erase it. Empty (not an error) when the
// space logs no events (event_log: false); false when the space is unknown or
// stopped. limit <= 0 uses the default.
func (s *Service) ChatLog(id string, limit int) ([]json.RawMessage, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	if limit <= 0 {
		limit = defaultChatLogLimit
	}
	evs := readChatEvents(filepath.Join(ent.workdir, ".vero", "events"), limit)
	if msgs, err := ent.space.Store.ListMessages(0); err == nil {
		evs = append(evs, userMessageEvents(msgs)...)
	} else {
		s.log.Warn("swarm: chatlog: list messages", "space", id, "err", err)
	}
	// Stable by time: a coalesced block carries its FIRST chunk's stamp but sits
	// at its flush point in the file, so sorting restores where the turn opened
	// on the live view; ties keep log order (tool start before its result).
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].at.Before(evs[j].at) })
	if len(evs) > limit {
		evs = evs[len(evs)-limit:]
	}
	out := make([]json.RawMessage, len(evs))
	for i, e := range evs {
		out[i] = e.raw
	}
	return out, true
}

// readChatEvents walks the day files newest-first, collecting chat-relevant
// events until `limit` are in hand, then returns them oldest-first. Whole days
// are taken (a day file's lines are chronological); the caller trims to the
// exact limit after merging mail.
func readChatEvents(dir string, limit int) []chatEvent {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no log yet (event_log: false, or nothing emitted) — not an error
	}
	var days []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".jsonl") && len(n) == len("2006-01-02.jsonl") {
			days = append(days, n)
		}
	}
	sort.Strings(days) // zero-padded ISO dates — string order is date order
	var out []chatEvent
	for i := len(days) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(readChatDay(filepath.Join(dir, days[i])), out...)
	}
	return out
}

// readChatDay parses one day file into its chat-relevant events, in file
// order. A torn or oversized line is skipped — the replay is best-effort
// observability, never load-bearing (the eventLog writer's own contract).
func readChatDay(path string) []chatEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []chatEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxChatLineBytes)
	var last time.Time
	for sc.Scan() {
		// Line shape: {"ts":"…","event":{"spaceId":"…","event":{<event.Event>}}}.
		var ln struct {
			Event struct {
				Event json.RawMessage `json:"event"`
			} `json:"event"`
		}
		if json.Unmarshal(sc.Bytes(), &ln) != nil || len(ln.Event.Event) == 0 {
			continue
		}
		var peek struct {
			Kind string    `json:"Kind"`
			Time time.Time `json:"Time"`
		}
		if json.Unmarshal(ln.Event.Event, &peek) != nil || !chatKind(peek.Kind) {
			continue
		}
		at := peek.Time
		if at.IsZero() {
			at = last // a timeless line inherits its predecessor's instant
		}
		last = at
		// Scanner reuses its buffer — the raw payload must be copied out.
		out = append(out, chatEvent{raw: append(json.RawMessage(nil), ln.Event.Event...), at: at})
	}
	return out
}

// userMessage is the synthetic wire event carrying one operator mail into the
// replay — the console's UserTurn twin. It exists only on the chatlog wire
// (PascalCase like event.Event, so the FE reads it with the same conventions);
// pkg/event stays pure agent events.
type userMessage struct {
	Kind        string             `json:"Kind"`
	Time        time.Time          `json:"Time"`
	UserMessage userMessagePayload `json:"UserMessage"`
}

type userMessagePayload struct {
	Sender    string `json:"Sender"`
	Recipient string `json:"Recipient"`
	Subject   string `json:"Subject,omitempty"`
	Body      string `json:"Body"`
}

// userMessageEvents converts operator mail (sender "user") into synthetic
// user_message events so the replayed stream carries what the operator said
// between the members' turns. Inter-agent mail is deliberately excluded — the
// live console never showed it (it lives in the Mail view). A broadcast is
// stored as one row per recipient (bus fan-out); rows with identical
// subject+body inside a 2s window collapse back into a single "all"-addressed
// event, matching the one turn the live composer pushed.
func userMessageEvents(msgs []store.Message) []chatEvent {
	var out []chatEvent
	var lastKey string
	var lastAt int64
	for _, m := range msgs { // ListMessages(0) is created_at-ordered
		if m.Sender != "user" {
			continue
		}
		key := m.Subject + "\x00" + m.Body
		if key == lastKey && m.CreatedAt-lastAt < 2000 {
			// Same broadcast, next recipient row: re-address the emitted event to
			// "all" instead of duplicating the turn once per member.
			if n := len(out) - 1; n >= 0 {
				out[n] = broadcastChatEvent(m, out[n])
			}
			continue
		}
		lastKey, lastAt = key, m.CreatedAt
		at := time.UnixMilli(m.CreatedAt)
		syn := userMessage{Kind: "user_message", Time: at, UserMessage: userMessagePayload{
			Sender: m.Sender, Recipient: m.Recipient, Subject: m.Subject, Body: m.Body,
		}}
		if b, err := json.Marshal(syn); err == nil {
			out = append(out, chatEvent{raw: b, at: at})
		}
	}
	return out
}

// broadcastChatEvent rewrites an already-emitted user_message as addressed to
// RecipientAll — the collapse step when a second per-recipient broadcast row
// shows up. Marshal failures keep the original event.
func broadcastChatEvent(m store.Message, prev chatEvent) chatEvent {
	at := prev.at
	syn := userMessage{Kind: "user_message", Time: at, UserMessage: userMessagePayload{
		Sender: m.Sender, Recipient: store.RecipientAll, Subject: m.Subject, Body: m.Body,
	}}
	b, err := json.Marshal(syn)
	if err != nil {
		return prev
	}
	return chatEvent{raw: b, at: at}
}
