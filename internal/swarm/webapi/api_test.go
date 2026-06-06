package webapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

var errUnknownSpace = errors.New("unknown space")

// fakeBackend is a Backend stub that records inbound commands and returns canned
// snapshots, so the HTTP/WS layer can be exercised without a live swarm.
type fakeBackend struct {
	token  string
	spaces map[string][]MemberInfo // id -> roster

	mu       sync.Mutex
	runs     [][3]string // {space, agent, prompt}
	msgs     [][3]string // {space, to, body}
	perms    [][6]string // {space, agent, reqId, behavior, reason, ruleTool}
	suspends [][2]string
}

func (f *fakeBackend) Token() string                           { return f.token }
func (f *fakeBackend) HasSpace(id string) bool                 { _, ok := f.spaces[id]; return ok }
func (f *fakeBackend) Register(string, string) (string, error) { return "sp-new", nil }
func (f *fakeBackend) StopSpace(id string) error {
	if !f.HasSpace(id) {
		return errUnknownSpace
	}
	return nil // stop keeps the record (Docker-style)
}
func (f *fakeBackend) RunSpace(id string) (string, error) {
	if !f.HasSpace(id) {
		return "", errUnknownSpace
	}
	return id, nil
}
func (f *fakeBackend) RemoveSpace(id string) error {
	if !f.HasSpace(id) {
		return errUnknownSpace
	}
	delete(f.spaces, id)
	return nil
}
func (f *fakeBackend) ResetSpace(id string) (string, error) {
	if !f.HasSpace(id) {
		return "", errUnknownSpace
	}
	return id, nil // reset keeps the same id
}

func (f *fakeBackend) Spaces() []SpaceInfo {
	out := make([]SpaceInfo, 0, len(f.spaces))
	for id, r := range f.spaces {
		out = append(out, SpaceInfo{ID: id, Name: id, Status: "running", Members: len(r)})
	}
	return out
}
func (f *fakeBackend) Roster(id string) ([]MemberInfo, bool) { r, ok := f.spaces[id]; return r, ok }
func (f *fakeBackend) Tasks(id string) (TaskPage, bool) {
	if !f.HasSpace(id) {
		return TaskPage{}, false
	}
	return TaskPage{Tasks: []TaskInfo{{ID: 1, Title: "t", Status: "pending", Assignee: "w"}}}, true
}
func (f *fakeBackend) TasksByStatus(id, status string, limit, offset int) (TaskPage, bool) {
	if !f.HasSpace(id) {
		return TaskPage{}, false
	}
	return TaskPage{Tasks: []TaskInfo{{ID: 2, Title: "done", Status: status, Assignee: "w"}}, Total: 7}, true
}
func (f *fakeBackend) Messages(id string) ([]MessageInfo, bool) {
	if !f.HasSpace(id) {
		return nil, false
	}
	return []MessageInfo{}, true
}
func (f *fakeBackend) Transcript(id, agent string) ([]TranscriptEntry, bool) {
	if !f.HasSpace(id) {
		return nil, false
	}
	return []TranscriptEntry{{Role: "user", Text: "hi"}}, true
}
func (f *fakeBackend) PendingGates(id string) ([]any, bool) {
	if !f.HasSpace(id) {
		return nil, false
	}
	return []any{}, true
}
func (f *fakeBackend) Run(space, agent, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, [3]string{space, agent, prompt})
	return nil
}
func (f *fakeBackend) SendUserMessage(space, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, [3]string{space, to, body})
	return nil
}
func (f *fakeBackend) RespondPermission(space, agent, reqID, behavior, reason, ruleTool string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perms = append(f.perms, [6]string{space, agent, reqID, behavior, reason, ruleTool})
	return nil
}
func (f *fakeBackend) RespondQuestion(string, string, string, map[string]string) error { return nil }
func (f *fakeBackend) Suspend(space, agent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspends = append(f.suspends, [2]string{space, agent})
	return nil
}
func (f *fakeBackend) Resume(string, string) error    { return nil }
func (f *fakeBackend) Freeze(string, string) error    { return nil }
func (f *fakeBackend) Unfreeze(string, string) error  { return nil }
func (f *fakeBackend) AddMember(string, string) error { return nil }
func (f *fakeBackend) HaltAll(string) error           { return nil }

func newFake() *fakeBackend {
	return &fakeBackend{
		token: "secret",
		spaces: map[string][]MemberInfo{
			"sp-a": {{Name: "leader", Role: "leader", Membership: "active", Run: "idle"}},
			"sp-b": {{Name: "leader", Role: "leader", Membership: "active", Run: "idle"}},
		},
	}
}

// AC#1: the token gate. No token → 401; correct token → 200.
func TestTokenGate(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(NewRouter(fake, NewHub(), nil))
	defer srv.Close()

	// No token.
	resp, err := http.Get(srv.URL + "/api/swarms")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", resp.StatusCode)
	}

	// Bearer token.
	req, _ := http.NewRequest("GET", srv.URL+"/api/swarms", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("bearer status = %d, want 200", resp2.StatusCode)
	}

	// Healthz is unauthenticated.
	hz, _ := http.Get(srv.URL + "/healthz")
	if hz.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", hz.StatusCode)
	}
	hz.Body.Close()
}

// REST snapshots return the backend's data; query-param token works too.
func TestRESTSnapshots(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(NewRouter(fake, NewHub(), nil))
	defer srv.Close()

	var spaces []SpaceInfo
	getJSON(t, srv.URL+"/api/swarms?token=secret", &spaces)
	if len(spaces) != 2 {
		t.Fatalf("got %d spaces, want 2", len(spaces))
	}

	var roster []MemberInfo
	getJSON(t, srv.URL+"/api/swarm/sp-a?token=secret", &roster)
	if len(roster) != 1 || roster[0].Name != "leader" {
		t.Fatalf("roster = %+v", roster)
	}

	// No status → board snapshot TaskPage {tasks,total}.
	var page TaskPage
	getJSON(t, srv.URL+"/api/tasks?space=sp-a&token=secret", &page)
	if len(page.Tasks) != 1 || page.Tasks[0].Status != "pending" {
		t.Fatalf("board tasks = %+v", page)
	}
	// A status filter → on-demand paged view (TasksByStatus) carrying the full total.
	var done TaskPage
	getJSON(t, srv.URL+"/api/tasks?space=sp-a&status=completed&limit=5&offset=0&token=secret", &done)
	if len(done.Tasks) != 1 || done.Tasks[0].Status != "completed" || done.Total != 7 {
		t.Fatalf("paged completed = %+v", done)
	}

	// Unknown space → 404.
	resp, _ := http.Get(srv.URL + "/api/swarm/nope?token=secret")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown space status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// REST command endpoints translate to the backend calls.
func TestRESTCommands(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(NewRouter(fake, NewHub(), nil))
	defer srv.Close()

	body := bytes.NewBufferString(`{"prompt":"do it"}`)
	resp := post(t, srv.URL+"/api/agents/leader/run?space=sp-a&token=secret", body)
	if resp != http.StatusNoContent {
		t.Fatalf("run status = %d, want 204", resp)
	}
	if len(fake.runs) != 1 || fake.runs[0] != [3]string{"sp-a", "leader", "do it"} {
		t.Fatalf("run not recorded: %+v", fake.runs)
	}

	if s := post(t, srv.URL+"/api/agents/leader/suspend?space=sp-a&token=secret", nil); s != http.StatusNoContent {
		t.Fatalf("suspend status = %d", s)
	}
	if len(fake.suspends) != 1 {
		t.Fatalf("suspend not recorded: %+v", fake.suspends)
	}

	// Operator → member message (flat comms).
	mbody := bytes.NewBufferString(`{"body":"status?"}`)
	if s := post(t, srv.URL+"/api/agents/worker-a/message?space=sp-a&token=secret", mbody); s != http.StatusNoContent {
		t.Fatalf("message status = %d, want 204", s)
	}
	if len(fake.msgs) != 1 || fake.msgs[0] != [3]string{"sp-a", "worker-a", "status?"} {
		t.Fatalf("user message not recorded: %+v", fake.msgs)
	}
}

// AC#3: a WS client subscribed to one space receives that space's events and
// NOT the other space's.
func TestWSFanoutIsolation(t *testing.T) {
	fake := newFake()
	hub := NewHub()
	srv := httptest.NewServer(NewRouter(fake, hub, nil))
	defer srv.Close()

	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
	a := dialWS(t, wsBase+"/ws?space=sp-a&token=secret")
	defer a.Close()
	b := dialWS(t, wsBase+"/ws?space=sp-b&token=secret")
	defer b.Close()

	waitConns(t, hub, 2)

	hub.Publish("sp-a", "leader", []byte(`{"spaceId":"sp-a"}`))

	// a receives it.
	if got := recvWS(t, a, time.Second); !strings.Contains(got, "sp-a") {
		t.Fatalf("client A got %q, want sp-a event", got)
	}
	// b receives nothing for sp-a.
	if got := recvWSMaybe(b, 200*time.Millisecond); got != "" {
		t.Fatalf("client B leaked a foreign-space event: %q", got)
	}
}

// AC#5 path: an approval reply sent over the WS reaches RespondPermission with
// the right (space, agent, reqId).
func TestWSRespondPermission(t *testing.T) {
	fake := newFake()
	hub := NewHub()
	srv := httptest.NewServer(NewRouter(fake, hub, nil))
	defer srv.Close()

	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
	c := dialWS(t, wsBase+"/ws?space=sp-a&token=secret")
	defer c.Close()
	waitConns(t, hub, 1)

	cmd := `{"type":"respond_permission","agent":"leader","reqId":"r1","behavior":"allow"}`
	if err := websocket.Message.Send(c, cmd); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.perms)
		fake.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.perms) != 1 || fake.perms[0] != [6]string{"sp-a", "leader", "r1", "allow", "", ""} {
		t.Fatalf("permission not routed: %+v", fake.perms)
	}
}

// "Always allow" carries the tool name through as ruleTool so the backend can
// seed a session allow rule (the agent side turns it into a tool-wide rule).
func TestWSRespondPermission_AlwaysAllow(t *testing.T) {
	fake := newFake()
	hub := NewHub()
	srv := httptest.NewServer(NewRouter(fake, hub, nil))
	defer srv.Close()

	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
	c := dialWS(t, wsBase+"/ws?space=sp-a&token=secret")
	defer c.Close()
	waitConns(t, hub, 1)

	cmd := `{"type":"respond_permission","agent":"builder","reqId":"r2","behavior":"allow","ruleTool":"write"}`
	if err := websocket.Message.Send(c, cmd); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.perms)
		fake.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.perms) != 1 || fake.perms[0] != [6]string{"sp-a", "builder", "r2", "allow", "", "write"} {
		t.Fatalf("always-allow ruleTool not routed: %+v", fake.perms)
	}
}

// --- helpers --------------------------------------------------------------

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func post(t *testing.T, url string, body *bytes.Buffer) int {
	t.Helper()
	var r http.Response
	var req *http.Request
	if body == nil {
		req, _ = http.NewRequest("POST", url, nil)
	} else {
		req, _ = http.NewRequest("POST", url, body)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r = *resp
	return r.StatusCode
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, err := websocket.Dial(url, "", "http://localhost")
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	return ws
}

func waitConns(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.Connections() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hub never reached %d connections (have %d)", want, h.Connections())
}

func recvWS(t *testing.T, ws *websocket.Conn, d time.Duration) string {
	t.Helper()
	got := recvWSMaybe(ws, d)
	if got == "" {
		t.Fatal("expected a WS message, got none")
	}
	return got
}

func recvWSMaybe(ws *websocket.Conn, d time.Duration) string {
	_ = ws.SetReadDeadline(time.Now().Add(d))
	var msg string
	if err := websocket.Message.Receive(ws, &msg); err != nil {
		return ""
	}
	return msg
}
