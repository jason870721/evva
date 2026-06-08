package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
)

// fakeLLM is a no-network llm.Client so agent.New constructs and runs without
// real API calls.
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
	stubProvider = "swarm_stub"
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
	cfg, err := config.Load(config.LoadOptions{AppName: "swarmtest", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.LLMProviderConfig[stubProvider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x", Models: []constant.Model{stubModel}}
	cfg.DefaultProvider = constant.LLMProvider{Name: stubProvider, Models: []constant.Model{stubModel}}
	cfg.DefaultModel = constant.Model(stubModel)
	return cfg
}

func testManifest() agentdef.Manifest {
	return agentdef.Manifest{Name: "team", Settings: agentdef.Settings{PermissionMode: "bypass", MaxIterations: 5}}
}

func testLoaded() []agentdef.Loaded {
	mk := func(name string, role agentdef.Role) agentdef.Loaded {
		return agentdef.Loaded{
			Def:    agent.AgentDefinition{Name: name, SystemPrompt: "You are " + name + ".", Model: stubModel},
			Skills: skill.NewRegistry(),
			Role:   role,
		}
	}
	return []agentdef.Loaded{
		mk("leader", agentdef.RoleLeader),
		mk("worker-a", agentdef.RoleWorker),
		mk("worker-b", agentdef.RoleWorker),
	}
}

// AC#1 + AC#5: NewSpace constructs every member, all reachable by name, all
// active + idle, with accurate roster fields.
func TestNewSpaceConstructsRoster(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("space-1", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	snap := sp.Roster.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("roster has %d members, want 3", len(snap))
	}
	for _, mv := range snap {
		if mv.Membership != MembershipActive || mv.Run != RunIdle {
			t.Errorf("%s: %s/%s, want active/idle", mv.Name, mv.Membership, mv.Run)
		}
	}
	for _, n := range []string{"leader", "worker-a", "worker-b"} {
		if _, ok := sp.Roster.Controller(n); !ok {
			t.Errorf("member %q not reachable via roster", n)
		}
	}
	if snap[0].Name != "leader" || snap[0].Role != agentdef.RoleLeader {
		t.Errorf("entry[0] = %+v, want leader/leader", snap[0])
	}
	if snap[1].Role != agentdef.RoleWorker {
		t.Errorf("worker role = %s", snap[1].Role)
	}
}

// AC#2: an agent's events arrive on the space out channel stamped with the
// correct spaceID and AgentID.
func TestSpaceEventTagging(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("space-7", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	leaderID := sp.agents["leader"].AgentID()

	if _, err := sp.agents["leader"].Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let any trailing events land in the buffer

	var events []SpacedEvent
drain:
	for {
		select {
		case e := <-sp.Events():
			events = append(events, e)
		default:
			break drain
		}
	}

	if len(events) == 0 {
		t.Fatal("no events arrived on the space channel")
	}
	sawLeader := false
	for _, e := range events {
		if e.SpaceID != "space-7" {
			t.Errorf("event SpaceID = %q, want space-7", e.SpaceID)
		}
		if e.Event.AgentID == leaderID {
			sawLeader = true
		}
	}
	if !sawLeader {
		t.Errorf("no event carried the leader's AgentID %q", leaderID)
	}
}

// AC#3: two spaces with the SAME member names share nothing.
func TestTwoSpaceIsolation(t *testing.T) {
	sp1, err := NewSpace("s1", testManifest(), testLoaded(), nil, stubConfig(t))
	if err != nil {
		t.Fatalf("space 1: %v", err)
	}
	defer sp1.Shutdown()
	sp2, err := NewSpace("s2", testManifest(), testLoaded(), nil, stubConfig(t))
	if err != nil {
		t.Fatalf("space 2: %v", err)
	}
	defer sp2.Shutdown()

	c1, _ := sp1.Roster.Controller("leader")
	c2, _ := sp2.Roster.Controller("leader")
	if c1 == nil || c2 == nil || c1 == c2 {
		t.Error("same-named leaders should be distinct controllers across spaces")
	}
	if sp1.Store == sp2.Store {
		t.Error("spaces share a store")
	}
	if sp1.Workdir == sp2.Workdir {
		t.Error("spaces share a workdir")
	}
	if sp1.agents["leader"].AgentID() == sp2.agents["leader"].AgentID() {
		t.Error("same AgentID across spaces")
	}
}

// AC#4: duplicate member names within one space error at construction.
func TestNewSpaceDuplicateNameErrors(t *testing.T) {
	dup := []agentdef.Loaded{
		{Def: agent.AgentDefinition{Name: "x", SystemPrompt: "a", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleLeader},
		{Def: agent.AgentDefinition{Name: "x", SystemPrompt: "b", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
	}
	sp, err := NewSpace("dup", testManifest(), dup, nil, stubConfig(t))
	if err == nil {
		sp.Shutdown()
		t.Fatal("want a duplicate-name error")
	}
}
