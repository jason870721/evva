package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
	"github.com/johnny1110/evva/internal/swarm/webapi"
)

// testService is a minimal stand-in for the swarm service's wire surface:
// the REST reads the TUI hydrates from, the auth guard, and a WS endpoint
// scripted per test. It speaks the SAME JSON the real webapi router speaks —
// the DTOs come straight from the webapi package.
func testService(t *testing.T, ws http.Handler) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			tok := r.Header.Get("Authorization")
			if tok != "Bearer secret" && r.URL.Query().Get("token") != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /api/swarms", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []webapi.SpaceInfo{
			{ID: "sp-1", Name: "tech-team", Status: "running", Members: 3},
			{ID: "sp-2", Name: "werewolf", Status: "stopped", Members: 13},
		})
	}))
	mux.HandleFunc("GET /api/swarm/sp-1", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []webapi.MemberInfo{
			{Name: "lead", AgentID: "ag-lead", Role: "leader", Run: "idle"},
			{Name: "qa", AgentID: "ag-qa", Role: "worker", Run: "busy"},
		})
	}))
	mux.HandleFunc("GET /api/tasks", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, webapi.TaskPage{Tasks: []webapi.TaskInfo{{ID: 42, Title: "build", Status: "running", Assignee: "qa"}}, Total: 7})
	}))
	mux.HandleFunc("GET /api/swarm/sp-1/chatlog", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []json.RawMessage{
			json.RawMessage(`{"Kind":"text","AgentID":"ag-lead","Text":{"Text":"hello"}}`),
			json.RawMessage(`{"Kind":"not_a_known_kind_at_all"}`),
			json.RawMessage(`{"Kind":"user_message","UserMessage":{"Sender":"user","Recipient":"qa","Body":"go"}}`),
		})
	}))
	mux.HandleFunc("GET /api/swarm/sp-1/pending", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []json.RawMessage{
			json.RawMessage(`{"Kind":"approval_needed","AgentID":"ag-qa","ApprovalNeeded":{"RequestID":"req-1","ToolName":"bash"}}`),
		})
	}))
	posts := map[string]*atomic.Int32{}
	for _, p := range []string{"suspend", "resume", "freeze", "unfreeze", "message", "halt"} {
		posts[p] = &atomic.Int32{}
	}
	mux.HandleFunc("POST /api/agents/qa/suspend", auth(func(w http.ResponseWriter, r *http.Request) { posts["suspend"].Add(1); w.WriteHeader(204) }))
	mux.HandleFunc("POST /api/agents/qa/message", auth(func(w http.ResponseWriter, r *http.Request) {
		posts["message"].Add(1)
		writeJSON(w, map[string]string{"id": "m1"})
	}))
	mux.HandleFunc("POST /api/halt", auth(func(w http.ResponseWriter, r *http.Request) { posts["halt"].Add(1); w.WriteHeader(204) }))
	if ws != nil {
		mux.Handle("GET /ws", ws)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	return srv, New(addr, "secret")
}

func TestClientHydrationReads(t *testing.T) {
	_, c := testService(t, nil)

	sp, err := c.ResolveSpace("tech-team")
	if err != nil || sp.ID != "sp-1" {
		t.Fatalf("resolve by name = (%+v, %v)", sp, err)
	}
	if sp2, err := c.ResolveSpace("sp-2"); err != nil || sp2.Name != "werewolf" {
		t.Fatalf("resolve by id = (%+v, %v)", sp2, err)
	}
	if _, err := c.ResolveSpace("ghost"); err == nil || !strings.Contains(err.Error(), "tech-team") {
		t.Fatalf("unknown ref must list available refs, got %v", err)
	}

	roster, err := c.Roster("sp-1")
	if err != nil || len(roster) != 2 || roster[1].AgentID != "ag-qa" {
		t.Fatalf("roster = (%+v, %v)", roster, err)
	}
	tasks, err := c.Tasks("sp-1")
	if err != nil || tasks.Total != 7 || tasks.Tasks[0].ID != 42 {
		t.Fatalf("tasks = (%+v, %v)", tasks, err)
	}

	// Chatlog: parseable events decode, junk is skipped — never fatal.
	evs, err := c.ChatLog("sp-1", 100)
	if err != nil || len(evs) != 3 {
		t.Fatalf("chatlog = (%d events, %v), want 3", len(evs), err)
	}
	if evs[2].UserMessage == nil || evs[2].UserMessage.Recipient != "qa" {
		t.Fatalf("user_message payload lost: %+v", evs[2])
	}
	pend, err := c.Pending("sp-1")
	if err != nil || len(pend) != 1 || pend[0].ApprovalNeeded.RequestID != "req-1" {
		t.Fatalf("pending = (%+v, %v)", pend, err)
	}
}

func TestClientWrites(t *testing.T) {
	_, c := testService(t, nil)
	if err := c.Verb("sp-1", "qa", "suspend"); err != nil {
		t.Fatal(err)
	}
	if err := c.Message("sp-1", "qa", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := c.HaltAll("sp-1"); err != nil {
		t.Fatal(err)
	}
}

func TestClientAuthRejected(t *testing.T) {
	_, c := testService(t, nil)
	c.Token = "wrong"
	if _, err := c.Spaces(); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token should 401, got %v", err)
	}
}

// scriptedWS is an in-process socket endpoint: it pushes the given frames on
// connect, then echoes a command_error for every inbound respond_* command,
// and drops the connection after `dropAfter` frames if set.
func scriptedWS(frames []string, sawCmd chan<- wire.Command) http.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		for _, f := range frames {
			if websocket.Message.Send(ws, f) != nil {
				return
			}
		}
		for {
			var raw string
			if websocket.Message.Receive(ws, &raw) != nil {
				return
			}
			var cmd wire.Command
			if json.Unmarshal([]byte(raw), &cmd) == nil && cmd.Type != "" {
				if sawCmd != nil {
					sawCmd <- cmd
				}
				if strings.HasPrefix(cmd.Type, "respond_") {
					_ = websocket.Message.Send(ws, fmt.Sprintf(`{"type":"command_error","reqId":%q,"message":"no gate"}`, cmd.ReqID))
				}
			}
		}
	})
}

// collect drains stream messages until pred says stop or the deadline hits.
func collect(t *testing.T, s *Stream, deadline time.Duration, pred func([]Msg) bool) []Msg {
	t.Helper()
	var got []Msg
	timer := time.After(deadline)
	for {
		select {
		case m, ok := <-s.Messages():
			if !ok {
				return got
			}
			got = append(got, m)
			if pred(got) {
				return got
			}
		case <-timer:
			t.Fatalf("stream deadline: have %d msgs", len(got))
		}
	}
}

func TestStreamLiveFoldAndCommandError(t *testing.T) {
	saw := make(chan wire.Command, 4)
	frames := []string{
		`{"spaceId":"sp-1","event":{"Kind":"text_chunk","AgentID":"ag-lead","Text":{"Text":"hi"}}}`,
		`{"spaceId":"sp-1","event":{"Kind":"approval_needed","AgentID":"ag-qa","ApprovalNeeded":{"RequestID":"req-9","ToolName":"bash"}}}`,
	}
	srv, c := testService(t, scriptedWS(frames, saw))
	_ = srv

	s := Dial(c.Addr, "secret", "sp-1")
	defer s.Close()

	msgs := collect(t, s, 5*time.Second, func(ms []Msg) bool {
		n := 0
		for _, m := range ms {
			if m.Event != nil {
				n++
			}
		}
		return n >= 2
	})
	var connected bool
	var kinds []string
	for _, m := range msgs {
		if m.Status != nil && m.Status.Connected {
			connected = true
		}
		if m.Event != nil {
			kinds = append(kinds, string(m.Event.Kind))
		}
	}
	if !connected {
		t.Error("stream never reported Connected")
	}
	if strings.Join(kinds, ",") != "text_chunk,approval_needed" {
		t.Fatalf("kinds = %v", kinds)
	}

	// A gate reply flows out as the wsCommand shape; the scripted error echo
	// comes back as CmdErr with the reqId intact.
	if err := s.Send(wire.Command{Type: "respond_permission", Agent: "qa", ReqID: "req-9", Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	select {
	case cmd := <-saw:
		if cmd.Type != "respond_permission" || cmd.ReqID != "req-9" || cmd.Behavior != "allow" {
			t.Fatalf("server saw %+v", cmd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the command")
	}
	collect(t, s, 5*time.Second, func(ms []Msg) bool {
		for _, m := range ms {
			if m.CmdErr != nil && m.CmdErr.ReqID == "req-9" && strings.Contains(m.CmdErr.Message, "no gate") {
				return true
			}
		}
		return false
	})
}

// A dropped socket reconnects with backoff and the stream keeps delivering —
// the TUI-2 reconnect acceptance. The scripted server closes each connection
// after one frame; the client must come back for the next.
func TestStreamReconnects(t *testing.T) {
	var conns atomic.Int32
	ws := websocket.Handler(func(c *websocket.Conn) {
		n := conns.Add(1)
		_ = websocket.Message.Send(c, fmt.Sprintf(`{"spaceId":"sp-1","event":{"Kind":"text","AgentID":"ag-lead","Text":{"Text":"conn-%d"}}}`, n))
		// Drop immediately — the client should reconnect.
	})
	srv, c := testService(t, ws)
	_ = srv

	s := Dial(c.Addr, "secret", "sp-1")
	defer s.Close()

	msgs := collect(t, s, 10*time.Second, func(ms []Msg) bool {
		n := 0
		for _, m := range ms {
			if m.Event != nil {
				n++
			}
		}
		return n >= 2
	})
	reconnStatuses := 0
	for _, m := range msgs {
		if m.Status != nil && !m.Status.Connected {
			reconnStatuses++
		}
	}
	if conns.Load() < 2 {
		t.Fatalf("server saw %d connections, want ≥2", conns.Load())
	}
	if reconnStatuses == 0 {
		t.Error("stream never surfaced a disconnected status")
	}
}

func TestBackoffCurve(t *testing.T) {
	want := []time.Duration{backoffMin, 2 * time.Second, 4 * time.Second, 8 * time.Second, backoffMax, backoffMax}
	for i, w := range want {
		if got := backoffFor(i + 1); got != w {
			t.Errorf("backoffFor(%d) = %v, want %v", i+1, got, w)
		}
	}
	if got := backoffFor(40); got != backoffMax { // shift overflow guard
		t.Errorf("backoffFor(40) = %v, want cap", got)
	}
}
