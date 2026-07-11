package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/net/websocket"

	"github.com/johnny1110/evva/internal/swarm/tui/client"
	"github.com/johnny1110/evva/internal/swarm/tui/wire"
	"github.com/johnny1110/evva/internal/swarm/webapi"
)

// harness spins a minimal scripted service (REST + WS) and returns a model
// wired to it, plus the channel of commands the socket received.
func harness(t *testing.T) (Model, chan wire.Command) {
	t.Helper()
	saw := make(chan wire.Command, 8)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/halt", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("POST /api/agents/qa/suspend", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("POST /api/agents/qa/message", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1"})
	})
	mux.Handle("GET /ws", websocket.Handler(func(ws *websocket.Conn) {
		for {
			var raw string
			if websocket.Message.Receive(ws, &raw) != nil {
				return
			}
			var cmd wire.Command
			if json.Unmarshal([]byte(raw), &cmd) == nil && cmd.Type != "" {
				saw <- cmd
			}
		}
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")

	stream := client.Dial(addr, "secret", "sp-1")
	t.Cleanup(stream.Close)
	m := New(client.New(addr, "secret"), stream, webapi.SpaceInfo{ID: "sp-1", Name: "tech-team"})
	m.roster = []webapi.MemberInfo{
		{Name: "lead", AgentID: "ag-lead", Role: "leader", Run: "idle"},
		{Name: "qa", AgentID: "ag-qa", Role: "worker", Run: "busy"},
		{Name: "dev-a", AgentID: "ag-dev", Role: "worker", Run: "idle"},
	}
	m.connected = true
	m.now = time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	return sized(m, 80, 24), saw
}

func sized(m Model, w, h int) Model {
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return nm.(Model)
}

func key(m Model, k string) (Model, tea.Cmd) {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		// The real terminal delivers space as runes — textinput inserts only
		// rune keys, and the overlay matches on String() == " " either way.
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

func ev(t *testing.T, raw string) *wire.Event {
	t.Helper()
	e, _ := wire.ParseEvent([]byte(raw))
	if e == nil {
		t.Fatalf("bad fixture event: %s", raw)
	}
	return e
}

func feed(m Model, e *wire.Event) Model {
	nm, _ := m.onStream(client.Msg{Event: e})
	return nm.(Model)
}

// TUI-3: the frame renders the roster, tasks, and folded stream at both the
// PRD's reference sizes without panicking, and pane focus follows the keys.
func TestViewRendersAtReferenceSizes(t *testing.T) {
	m, _ := harness(t)
	m.tasks = webapi.TaskPage{Tasks: []webapi.TaskInfo{
		{ID: 42, Title: "build the thing", Status: "verifying", Assignee: "qa"},
		{ID: 43, Title: "docs", Status: "pending", Assignee: "dev-a"},
	}, Total: 9}
	m = feed(m, ev(t, `{"Kind":"text","AgentID":"ag-qa","Time":"2026-07-11T10:30:00Z","Text":{"Text":"starting the sweep"}}`))
	m = feed(m, ev(t, `{"Kind":"task_dispatched","AgentID":"qa","Text":{"Text":"task #42 auto-dispatched → qa"}}`))

	for _, size := range [][2]int{{80, 24}, {200, 60}} {
		v := sized(m, size[0], size[1]).View()
		for _, want := range []string{"tech-team", "ROSTER", "lead", "qa", "TASKS", "#42", "starting the sweep", "● live"} {
			if !strings.Contains(v, want) {
				t.Errorf("%dx%d view missing %q", size[0], size[1], want)
			}
		}
	}

	// Too-small terminals refuse gracefully.
	if v := sized(m, 20, 4).View(); !strings.Contains(v, "too small") {
		t.Errorf("tiny view should degrade with a notice, got %q", v)
	}
}

// TUI-3: enter focuses the selected member's stream; a returns to all.
func TestFocusModel(t *testing.T) {
	m, _ := harness(t)
	m = feed(m, ev(t, `{"Kind":"text","AgentID":"ag-qa","Text":{"Text":"qa-only line"}}`))
	m = feed(m, ev(t, `{"Kind":"text","AgentID":"ag-dev","Text":{"Text":"dev-only line"}}`))

	m, _ = key(m, "down") // roster order: lead, qa(busy), dev-a — cursor to qa
	m, _ = key(m, "enter")
	if m.focus != "qa" {
		t.Fatalf("focus = %q, want qa", m.focus)
	}
	v := m.View()
	if !strings.Contains(v, "qa-only line") || strings.Contains(v, "dev-only line") {
		t.Errorf("focused stream must show only qa turns:\n%s", v)
	}
	m, _ = key(m, "a")
	if m.focus != "" {
		t.Fatalf("a should clear focus, got %q", m.focus)
	}
	if v := m.View(); !strings.Contains(v, "dev-only line") {
		t.Error("all view should interleave every member")
	}
}

// TUI-4: an approval gate auto-opens, approve sends the WS command with the
// reqId, and the pending set drops it.
func TestApprovalGateFlow(t *testing.T) {
	m, saw := harness(t)
	m = feed(m, ev(t, `{"Kind":"approval_needed","AgentID":"ag-qa","ApprovalNeeded":{"RequestID":"req-1","ToolName":"bash","InputDescription":"rm -rf ./dist"}}`))
	if m.overlay == nil || m.overlay.reqID != "req-1" {
		t.Fatal("gate overlay should auto-open on arrival")
	}
	if v := m.View(); !strings.Contains(v, "approval — qa wants bash") || !strings.Contains(v, "rm -rf ./dist") {
		t.Errorf("overlay missing gate facts:\n%s", v)
	}

	m, _ = key(m, "enter") // approve (cursor 0)
	select {
	case cmd := <-saw:
		if cmd.Type != "respond_permission" || cmd.ReqID != "req-1" || cmd.Behavior != "allow" || cmd.RuleTool != "" {
			t.Fatalf("approve sent %+v", cmd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("approve never reached the socket")
	}
	if m.overlay != nil || len(m.gates) != 0 {
		t.Fatal("answered gate should close and leave the pending set")
	}
}

// TUI-4: always-allow carries the session rule tool; deny routes through the
// reason composer; a command_error reopens with the error.
func TestApprovalVariants(t *testing.T) {
	m, saw := harness(t)
	gate := `{"Kind":"approval_needed","AgentID":"ag-qa","ApprovalNeeded":{"RequestID":"req-2","ToolName":"bash"}}`

	// always allow (cursor 1).
	m = feed(m, ev(t, gate))
	m, _ = key(m, "down")
	m, _ = key(m, "enter")
	cmd := <-saw
	if cmd.RuleTool != "bash" || cmd.Behavior != "allow" {
		t.Fatalf("always-allow sent %+v", cmd)
	}

	// deny with reason (cursor 2 → composer → enter).
	m = feed(m, ev(t, gate))
	m, _ = key(m, "down")
	m, _ = key(m, "down")
	m, _ = key(m, "enter")
	if m.compose != composeDenyReason {
		t.Fatal("deny should open the reason composer")
	}
	for _, r := range "too risky" {
		m, _ = key(m, string(r))
	}
	m, _ = key(m, "enter")
	cmd = <-saw
	if cmd.Behavior != "deny" || cmd.Reason != "too risky" {
		t.Fatalf("deny sent %+v", cmd)
	}

	// command_error reopens the overlay with the error and re-hydrates.
	m = feed(m, ev(t, gate))
	m, _ = key(m, "enter")
	<-saw
	nm, _ := m.onStream(client.Msg{CmdErr: &wire.CommandError{Type: "command_error", ReqID: "req-2", Message: "no gate"}})
	m = nm.(Model)
	if !strings.Contains(m.toast, "no gate") {
		t.Errorf("command_error must surface: %q", m.toast)
	}
}

// TUI-4: question gates — multi-select toggles, enter advances questions and
// submits the labels map.
func TestQuestionGateFlow(t *testing.T) {
	m, saw := harness(t)
	m = feed(m, ev(t, `{"Kind":"question_needed","AgentID":"ag-qa","QuestionNeeded":{"RequestID":"req-3","Questions":[
		{"Question":"Which env?","Options":[{"Label":"dev"},{"Label":"prod"}]},
		{"Question":"Features?","MultiSelect":true,"Options":[{"Label":"a"},{"Label":"b"},{"Label":"c"}]}]}}`))
	if m.overlay == nil || !m.overlay.isQuestion() {
		t.Fatal("question overlay should open")
	}
	m, _ = key(m, "down")  // cursor → prod
	m, _ = key(m, "enter") // pick prod, advance to q2
	m, _ = key(m, " ")     // toggle a
	m, _ = key(m, "down")
	m, _ = key(m, "down")
	m, _ = key(m, " ")     // toggle c
	m, _ = key(m, "enter") // submit
	select {
	case cmd := <-saw:
		if cmd.Type != "respond_question" || cmd.ReqID != "req-3" {
			t.Fatalf("question sent %+v", cmd)
		}
		if got := strings.Join(cmd.Answers["Which env?"], ","); got != "prod" {
			t.Errorf("q1 answers = %q", got)
		}
		if got := strings.Join(cmd.Answers["Features?"], ","); got != "a,c" {
			t.Errorf("q2 answers = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("question reply never reached the socket")
	}
}

// A failed hydrate keeps the current panes (the v1.7.4 non-destructive
// contract); a successful one replaces them.
func TestHydrateNeverBlanksOnError(t *testing.T) {
	m, _ := harness(t)
	m = feed(m, ev(t, `{"Kind":"text","AgentID":"ag-qa","Text":{"Text":"existing turn"}}`))
	nm, _ := m.Update(hydrateMsg{err: fmt.Errorf("boom")})
	m = nm.(Model)
	if len(m.turns) != 1 || !strings.Contains(m.View(), "existing turn") {
		t.Fatal("failed hydrate must keep the current stream")
	}
	if !strings.Contains(m.toast, "boom") {
		t.Error("failed hydrate should surface in the toast")
	}
}

// Gates raised while detached appear on hydrate; one answered elsewhere
// (missing from /pending) closes a stale overlay.
func TestPendingHydration(t *testing.T) {
	m, _ := harness(t)
	nm, _ := m.Update(hydrateMsg{gates: []*wire.Event{
		ev(t, `{"Kind":"approval_needed","AgentID":"ag-qa","ApprovalNeeded":{"RequestID":"req-9","ToolName":"bash"}}`),
	}})
	m = nm.(Model)
	if len(m.gates) != 1 {
		t.Fatal("pending gates should hydrate")
	}
	m.overlay = newGateOverlay(m.gates[0], "qa")
	nm, _ = m.Update(hydrateMsg{}) // next hydrate: gate resolved elsewhere
	m = nm.(Model)
	if m.overlay != nil || len(m.gates) != 0 {
		t.Fatal("a gate answered elsewhere must close the stale overlay")
	}
}

// run-terminal pruning: a member's run ending drops its pending gates.
func TestGatePruneOnRunEnd(t *testing.T) {
	m, _ := harness(t)
	m = feed(m, ev(t, `{"Kind":"approval_needed","AgentID":"ag-qa","ApprovalNeeded":{"RequestID":"req-1","ToolName":"bash"}}`))
	m.overlay = nil
	m = feed(m, ev(t, `{"Kind":"run_end","AgentID":"ag-qa"}`))
	if len(m.gates) != 0 {
		t.Fatal("run_end must prune the member's gates")
	}
}

// TUI-5: lifecycle verbs hit their endpoint exactly once and toast the
// outcome; halt requires the confirm.
func TestVerbsAndHalt(t *testing.T) {
	m, _ := harness(t)
	m, _ = key(m, "down") // select qa
	m, cmd := key(m, "s")
	if cmd == nil {
		t.Fatal("s should fire the suspend action")
	}
	if msg, ok := cmd().(actionMsg); !ok || msg.err != nil {
		t.Fatalf("suspend action = %+v", msg)
	}

	m, cmd = key(m, "H")
	if !m.confirmHalt || cmd != nil {
		t.Fatal("H should only arm the confirm")
	}
	m, cmd = key(m, "n")
	if m.confirmHalt || cmd != nil {
		t.Fatal("any non-y key must cancel the halt")
	}
	m, _ = key(m, "H")
	m, cmd = key(m, "y")
	if cmd == nil {
		t.Fatal("y should fire halt-all")
	}
	if msg, ok := cmd().(actionMsg); !ok || msg.err != nil {
		t.Fatalf("halt action = %+v", msg)
	}
}

// TUI-5: the composer messages the selected member through the REST endpoint.
func TestComposerMessage(t *testing.T) {
	m, _ := harness(t)
	m, _ = key(m, "down") // qa
	m, _ = key(m, "m")
	if m.compose != composeMessage {
		t.Fatal("m should open the composer")
	}
	for _, r := range "hello" {
		m, _ = key(m, string(r))
	}
	m, cmd := key(m, "enter")
	if cmd == nil {
		t.Fatal("enter should send")
	}
	if msg, ok := cmd().(actionMsg); !ok || msg.err != nil || !strings.Contains(msg.label, "qa") {
		t.Fatalf("message action = %+v", msg)
	}
	if m.compose != composeOff {
		t.Error("composer should close after send")
	}
}

// TUI-5: :run starts a leader turn over the socket.
func TestComposerRun(t *testing.T) {
	m, saw := harness(t)
	m, _ = key(m, ":")
	for _, r := range "run ship the release" {
		m, _ = key(m, string(r))
	}
	m, _ = key(m, "enter")
	select {
	case cmd := <-saw:
		if cmd.Type != "run" || cmd.Agent != "lead" || cmd.Prompt != "ship the release" {
			t.Fatalf(":run sent %+v", cmd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(":run never reached the socket")
	}
	_ = m
}
