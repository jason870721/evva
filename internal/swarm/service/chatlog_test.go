package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/event"
)

func chunk(kind event.Kind, agent, text string, at time.Time) event.Event {
	e := event.Event{Kind: kind, AgentID: agent, Time: at}
	p := &event.TextPayload{Text: text}
	if kind == event.KindThinkingChunk {
		e.Thinking = p
	} else {
		e.Text = p
	}
	return e
}

// The coalescer folds an agent's chunk run into ONE whole-turn event, stamped
// with the FIRST chunk's instant, flushed by the same boundaries the web
// reducer closes open turns on. Interleaved agents don't cross-contaminate.
func TestTurnCoalescerFoldsChunks(t *testing.T) {
	co := newTurnCoalescer()
	t0 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	if syn := co.fold(chunk(event.KindTextChunk, "a1", "He", t0)); len(syn) != 0 {
		t.Fatalf("first chunk produced synthetics: %v", syn)
	}
	// Another agent's chunk interleaves without disturbing a1's open block.
	if syn := co.fold(chunk(event.KindTextChunk, "b2", "other", t0.Add(time.Second))); len(syn) != 0 {
		t.Fatalf("interleaved agent produced synthetics: %v", syn)
	}
	co.fold(chunk(event.KindTextChunk, "a1", "llo", t0.Add(2*time.Second)))

	syn := co.fold(event.Event{Kind: event.KindToolUseStart, AgentID: "a1", Time: t0.Add(3 * time.Second)})
	if len(syn) != 1 {
		t.Fatalf("boundary flushed %d synthetics, want 1", len(syn))
	}
	got := syn[0]
	if got.Kind != event.KindText || got.AgentID != "a1" || got.Text == nil || got.Text.Text != "Hello" {
		t.Fatalf("synthetic = %+v, want whole-turn text %q for a1", got, "Hello")
	}
	if !got.Time.Equal(t0) {
		t.Fatalf("synthetic Time = %v, want first-chunk instant %v", got.Time, t0)
	}

	// b2's block is still pending; flushAll drains it (the pump-exit path).
	rest := co.flushAll()
	if len(rest) != 1 || rest[0].AgentID != "b2" || rest[0].Text.Text != "other" {
		t.Fatalf("flushAll = %+v, want b2's pending block", rest)
	}
	if again := co.flushAll(); len(again) != 0 {
		t.Fatalf("second flushAll not empty: %v", again)
	}
}

// A thinking->text switch closes the thinking block first (the reducer's block
// boundary), and a whitespace-only block never becomes a synthetic.
func TestTurnCoalescerTypeSwitchAndBlankBlocks(t *testing.T) {
	co := newTurnCoalescer()
	t0 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	co.fold(chunk(event.KindThinkingChunk, "a1", "hmm…", t0))
	syn := co.fold(chunk(event.KindTextChunk, "a1", "done", t0.Add(time.Second)))
	if len(syn) != 1 || syn[0].Kind != event.KindThinking || syn[0].Thinking.Text != "hmm…" {
		t.Fatalf("type switch flushed %+v, want the thinking block", syn)
	}
	if syn := co.fold(event.Event{Kind: event.KindTurnEnd, AgentID: "a1"}); len(syn) != 1 || syn[0].Text.Text != "done" {
		t.Fatalf("turn_end flushed %+v, want the text block", syn)
	}

	co.fold(chunk(event.KindTextChunk, "a1", "  \n", t0.Add(2*time.Second)))
	if syn := co.fold(event.Event{Kind: event.KindRunEnd, AgentID: "a1"}); len(syn) != 0 {
		t.Fatalf("whitespace-only block became a synthetic: %v", syn)
	}
}

// writeDayFile frames events exactly as the eventLog writer does — one
// ts-stamped line per wireEvent — into dir/<day>.jsonl.
func writeDayFile(t *testing.T, dir, day string, evs ...event.Event) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, ev := range evs {
		inner, err := json.Marshal(wireEvent{SpaceID: "sp", Event: ev})
		if err != nil {
			t.Fatal(err)
		}
		line, err := json.Marshal(map[string]json.RawMessage{"ts": json.RawMessage(`"x"`), "event": inner})
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, day+".jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func kindsOf(t *testing.T, raws []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(raws))
	for _, r := range raws {
		var peek struct{ Kind string }
		if err := json.Unmarshal(r, &peek); err != nil {
			t.Fatalf("unmarshal replayed event: %v", err)
		}
		out = append(out, peek.Kind)
	}
	return out
}

// readChatEvents keeps only chat-relevant kinds, walks day files oldest-first
// on return, and stops collecting once the newest days satisfy the limit.
func TestReadChatEventsFilterOrderLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "events")
	t0 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	txt := func(text string, at time.Time) event.Event {
		return event.Event{Kind: event.KindText, AgentID: "a1", Time: at, Text: &event.TextPayload{Text: text}}
	}
	writeDayFile(t, dir, "2026-06-01",
		txt("day1", t0),
		event.Event{Kind: event.KindUsage, AgentID: "a1", Time: t0.Add(time.Second)}, // not a chat kind — filtered
	)
	writeDayFile(t, dir, "2026-06-02",
		txt("day2-a", t0.Add(24*time.Hour)),
		event.Event{Kind: event.KindTurnEnd, AgentID: "a1", Time: t0.Add(24*time.Hour + time.Second)},
		txt("day2-b", t0.Add(24*time.Hour+2*time.Second)),
	)

	all := readChatEvents(dir, 10)
	if len(all) != 4 {
		t.Fatalf("collected %d events, want 4 (usage filtered)", len(all))
	}
	var first struct{ Text struct{ Text string } }
	if err := json.Unmarshal(all[0].raw, &first); err != nil || first.Text.Text != "day1" {
		t.Fatalf("oldest-first order broken: first = %s (err %v)", all[0].raw, err)
	}

	// A limit satisfied by the newest day never opens the older file.
	newest := readChatEvents(dir, 2)
	if len(newest) != 3 { // whole newest day is taken; the caller trims exactly
		t.Fatalf("limited collect = %d events, want the newest day's 3", len(newest))
	}
	var got struct{ Text struct{ Text string } }
	_ = json.Unmarshal(newest[0].raw, &got)
	if got.Text.Text != "day2-a" {
		t.Fatalf("limited collect starts at %q, want day2-a", got.Text.Text)
	}
}

// ChatLog end-to-end over a real registered space: replays the day file,
// filters non-chat kinds, merges operator mail as user_message synthetics
// (inter-agent mail excluded, broadcast rows collapsed to one "all" event),
// sorts by time, and applies the limit as a tail.
func TestChatLogMergesUserMailAndSorts(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)
	ent, ok := svc.entry(id)
	if !ok {
		t.Fatal("registered space not live")
	}

	t0 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	dir := filepath.Join(ent.workdir, ".vero", "events")
	writeDayFile(t, dir, "2026-06-01",
		event.Event{Kind: event.KindText, AgentID: "L", Time: t0.Add(10 * time.Second), Text: &event.TextPayload{Text: "on it"}},
		event.Event{Kind: event.KindToolUseStart, AgentID: "L", Time: t0.Add(11 * time.Second), ToolUseStart: &event.ToolUseStartPayload{Name: "bash", ToolID: "t1"}},
		event.Event{Kind: event.KindUsage, AgentID: "L", Time: t0.Add(12 * time.Second)}, // filtered
	)

	put := func(sender, recipient, body string, at time.Time) {
		t.Helper()
		if err := ent.space.Store.PutMessage(store.Message{
			ID: "m-" + sender + "-" + recipient + "-" + body, Sender: sender, Recipient: recipient,
			Body: body, CreatedAt: at.UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("user", "leader", "please start", t0)               // before the log events → sorts first
	put("worker", "leader", "status update", t0.Add(5*time.Second)) // inter-agent — excluded
	put("user", "leader", "ping all", t0.Add(20*time.Second))       // broadcast row 1
	put("user", "worker", "ping all", t0.Add(20*time.Second))       // broadcast row 2 — collapses

	evs, ok := svc.ChatLog(id, 0)
	if !ok {
		t.Fatal("ChatLog reported unknown space")
	}
	want := []string{"user_message", "text", "tool_use_start", "user_message"}
	if got := kindsOf(t, evs); len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("kinds = %v, want %v", got, want)
			}
		}
	}
	var last struct {
		UserMessage struct{ Recipient, Body string }
	}
	if err := json.Unmarshal(evs[len(evs)-1], &last); err != nil {
		t.Fatal(err)
	}
	if last.UserMessage.Recipient != store.RecipientAll || last.UserMessage.Body != "ping all" {
		t.Fatalf("broadcast rows did not collapse to one 'all' event: %+v", last.UserMessage)
	}

	// The limit keeps the TAIL — the newest turns are what the console shows.
	tail, _ := svc.ChatLog(id, 2)
	if got := kindsOf(t, tail); len(got) != 2 || got[0] != "tool_use_start" || got[1] != "user_message" {
		t.Fatalf("limited kinds = %v, want the last two", got)
	}

	// Unknown space stays a miss.
	if _, ok := svc.ChatLog("nope", 0); ok {
		t.Fatal("ChatLog(nope) reported ok")
	}
}
