package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	"golang.org/x/net/websocket"
)

// --- stub LLM so agent.New constructs + runs without network -------------

type fakeLLM struct{ model string }

func (f *fakeLLM) Name() string             { return stubProvider }
func (f *fakeLLM) Model() string            { return f.model }
func (*fakeLLM) SupportsDeferLoading() bool { return false }
func (*fakeLLM) Complete(context.Context, []llm.Message, []tools.Tool) (llm.Response, error) {
	return llm.Response{Content: "ok"}, nil
}
func (f *fakeLLM) Stream(ctx context.Context, m []llm.Message, ts []tools.Tool, _ llm.ChunkSink) (llm.Response, error) {
	return f.Complete(ctx, m, ts)
}
func (*fakeLLM) Apply(...llm.Option) {}

const (
	stubProvider = "svc_stub"
	stubModel    = "stub-model"
)

func init() {
	if !llm.DefaultRegistry().Has(stubProvider) {
		_ = llm.DefaultRegistry().Register(stubProvider, func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &fakeLLM{model: model}, nil
		})
	}
}

func stubConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{AppName: "svctest", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.LLMProviderConfig[stubProvider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x", Models: []constant.Model{stubModel}}
	cfg.DefaultProvider = constant.LLMProvider{Name: stubProvider, Models: []constant.Model{stubModel}}
	cfg.DefaultModel = constant.Model(stubModel)
	return cfg
}

func stubManifest() agentdef.Manifest {
	return agentdef.Manifest{Name: "team", Settings: agentdef.Settings{PermissionMode: "bypass", MaxIterations: 3}}
}

func stubLoaded() []agentdef.Loaded {
	mk := func(name string, role agentdef.Role) agentdef.Loaded {
		return agentdef.Loaded{
			Def:    agent.AgentDefinition{Name: name, SystemPrompt: "You are " + name + ".", Model: stubModel},
			Skills: skill.NewRegistry(),
			Role:   role,
		}
	}
	return []agentdef.Loaded{mk("leader", agentdef.RoleLeader), mk("worker", agentdef.RoleWorker)}
}

// registerStub brings a stub-LLM space up through the real register() core.
func registerStub(t *testing.T, s *Service) string {
	t.Helper()
	id, err := s.register(common.GenUUID(), "stub-"+common.GenUUID()[:6], stubManifest(), stubLoaded(), stubConfig(t))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return id
}

// AC#2: two spaces are independently registered + isolated; stopping one leaves
// the other serving, and same-named leaders are distinct.
func TestTwoSpaceRegistrationAndIsolation(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()

	idA := registerStub(t, svc)
	idB := registerStub(t, svc)
	if idA == idB {
		t.Fatal("two registrations produced the same space id")
	}

	spaces := svc.ListSpaces()
	if len(spaces) != 2 {
		t.Fatalf("ListSpaces = %d, want 2", len(spaces))
	}

	// Same-named leaders, distinct controllers.
	ca, _ := svc.controller(idA, "leader")
	cb, _ := svc.controller(idB, "leader")
	if ca == nil || cb == nil || ca == cb {
		t.Fatal("same-named leaders should be distinct controllers across spaces")
	}

	// Stop A; B keeps serving.
	if err := svc.StopSpace(idA); err != nil {
		t.Fatalf("StopSpace: %v", err)
	}
	if svc.HasSpace(idA) {
		t.Fatal("space A still registered after stop")
	}
	if !svc.HasSpace(idB) {
		t.Fatal("stopping A took B down — isolation broken")
	}
	if _, ok := svc.Roster(idB); !ok {
		t.Fatal("B roster unavailable after A stopped")
	}

	// Stopping an unknown space errors.
	if err := svc.StopSpace("nope"); err == nil {
		t.Fatal("StopSpace(unknown) should error")
	}
}

// AC#4: REST roster + tasks reflect the live space, behind the token gate.
func TestRESTReflectsSpace(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	ts := httptest.NewServer(svc.srv.Handler)
	defer ts.Close()

	// Seed a task directly in the ledger and assert the REST snapshot shows it.
	ent, _ := svc.entry(id)
	if _, err := ent.space.Store.CreateTask(store.Task{Title: "build it", CreatedBy: "leader", Assignee: "worker"}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	var roster []webapi.MemberInfo
	getJSON(t, ts.URL+"/api/swarm/"+id+"?token="+svc.Token(), &roster)
	if len(roster) != 2 {
		t.Fatalf("roster = %d members, want 2", len(roster))
	}

	var page webapi.TaskPage
	getJSON(t, ts.URL+"/api/tasks?space="+id+"&token="+svc.Token(), &page)
	if len(page.Tasks) != 1 || page.Tasks[0].Title != "build it" || page.Tasks[0].Status != "pending" {
		t.Fatalf("tasks = %+v", page)
	}
}

// AC#3: a WS client subscribed to space A sees A's leader run events; a client
// on space B sees none of them.
func TestWSEventRoutingAcrossSpaces(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	idA := registerStub(t, svc)
	idB := registerStub(t, svc)

	ts := httptest.NewServer(svc.srv.Handler)
	defer ts.Close()
	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")

	a := dialWS(t, wsBase+"/ws?space="+idA+"&token="+svc.Token())
	defer a.Close()
	b := dialWS(t, wsBase+"/ws?space="+idB+"&token="+svc.Token())
	defer b.Close()
	waitConns(t, svc.hub, 2)

	// Drive A's leader. Events should fan out to A's subscriber only.
	if err := svc.Run(idA, "leader", "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := recvWS(t, a, 2*time.Second)
	if !strings.Contains(got, idA) {
		t.Fatalf("A subscriber got %q, want an event tagged %s", got, idA)
	}
	if leaked := recvWSMaybe(b, 300*time.Millisecond); leaked != "" {
		t.Fatalf("B subscriber leaked an A-space event: %q", leaked)
	}
}

// AC#5 path: RespondPermission routes to the addressed controller (a known
// agent with an unknown request id surfaces the controller's error, proving the
// call reached it) and rejects unknown spaces.
func TestRespondPermissionRouting(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	if err := svc.RespondPermission("nope", "leader", "r1", "allow", "", ""); err == nil {
		t.Fatal("RespondPermission on unknown space should error")
	}
	// Known agent, unknown request id → the controller's own error (reached it).
	if err := svc.RespondPermission(id, "leader", "no-such-req", "allow", "", ""); err == nil {
		t.Fatal("RespondPermission with unknown request id should surface the controller error")
	}
}

// RP-2 §3.1 regression: the browser echoes back the event's AgentID (a random
// UUID), not the member name, when answering an approval. Before the fix the
// service resolved the controller by name only, so the UUID never matched and
// every web-driven approval reply was silently dropped — the blocked tool hung
// forever. This proves an approval addressed by AgentID now reaches the
// controller. (The existing test above covers the name path.)
func TestRespondPermissionRoutesByAgentID(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	// Resolve the leader's AgentID the way the browser does — from the roster
	// snapshot (MemberInfo.AgentID == ctl.AgentID()).
	members, ok := svc.Roster(id)
	if !ok {
		t.Fatal("roster snapshot for a known space should exist")
	}
	var agentID string
	for _, m := range members {
		if m.Name == "leader" {
			agentID = m.AgentID
		}
	}
	if agentID == "" {
		t.Fatal("leader has no AgentID in the roster snapshot")
	}

	// Unknown request id, but addressed by AgentID: the error must be the
	// controller's ("no pending request"), proving it routed — NOT the routing
	// miss ("unknown space/agent") the bug produced.
	err := svc.RespondPermission(id, agentID, "no-such-req", "allow", "", "")
	if err == nil {
		t.Fatal("unknown request id should still surface the controller's error")
	}
	if strings.Contains(err.Error(), "unknown space/agent") {
		t.Fatalf("approval addressed by AgentID failed to route (routing miss): %v", err)
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

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, err := websocket.Dial(url, "", "http://localhost")
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	return ws
}

func waitConns(t *testing.T, h interface{ Connections() int }, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
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
