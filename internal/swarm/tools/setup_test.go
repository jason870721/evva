package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/skill"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// --- stub LLM so swarm.NewSpace builds agents with no network (list_members
// needs a real, populated Roster, which only NewSpace can construct). ----------

type fakeLLM struct{ model string }

func (f *fakeLLM) Name() string             { return stubProvider }
func (f *fakeLLM) Model() string            { return f.model }
func (*fakeLLM) SupportsDeferLoading() bool { return false }
func (*fakeLLM) Complete(context.Context, []llm.Message, []pubtools.Tool) (llm.Response, error) {
	return llm.Response{Content: "ok"}, nil
}
func (f *fakeLLM) Stream(ctx context.Context, m []llm.Message, ts []pubtools.Tool, _ llm.ChunkSink) (llm.Response, error) {
	return f.Complete(ctx, m, ts)
}
func (*fakeLLM) Apply(...llm.Option) {}

const (
	stubProvider = "swarmtools_stub"
	stubModel    = "stub-model"
)

func init() {
	if !llm.DefaultRegistry().Has(stubProvider) {
		_ = llm.DefaultRegistry().Register(stubProvider, func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &fakeLLM{model: model}, nil
		})
	}
}

func stubCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{AppName: "swarmtoolstest", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.LLMProviderConfig[stubProvider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x", Models: []constant.Model{stubModel}}
	cfg.DefaultProvider = constant.LLMProvider{Name: stubProvider, Models: []constant.Model{stubModel}}
	cfg.DefaultModel = constant.Model(stubModel)
	return cfg
}

// --- test spaces -------------------------------------------------------------

type fakeMembership struct{ active []string }

func (f fakeMembership) ActiveMembers() []string { return f.active }

// liteSpace is a real store + bus with no agents — enough for the task tools and
// send_message (neither needs the Roster). `active` seeds the bus's broadcast
// fan-out.
func liteSpace(t *testing.T, active ...string) *swarm.SwarmSpace {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &swarm.SwarmSpace{Store: st, Bus: bus.New(st, fakeMembership{active: active})}
}

// realSpace builds a fully-populated space (leader + two workers) via NewSpace —
// used where a real Roster is needed (list_members).
func realSpace(t *testing.T) *swarm.SwarmSpace {
	t.Helper()
	loaded := []agentdef.Loaded{
		{Def: agent.AgentDefinition{Name: "leader", SystemPrompt: "You are leader.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleLeader},
		{Def: agent.AgentDefinition{Name: "worker-a", SystemPrompt: "You are worker-a.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
		{Def: agent.AgentDefinition{Name: "worker-b", SystemPrompt: "You are worker-b.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
	}
	m := agentdef.Manifest{Name: "team", Settings: agentdef.Settings{PermissionMode: "bypass", MaxIterations: 5}}
	sp, err := swarm.NewSpace("t", m, loaded, nil, stubCfg(t))
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	t.Cleanup(sp.Shutdown)
	return sp
}

func leaderMC(sp *swarm.SwarmSpace) swarm.MemberContext {
	return swarm.MemberContext{Name: "leader", Role: agentdef.RoleLeader, Space: sp}
}

func workerMC(sp *swarm.SwarmSpace, name string) swarm.MemberContext {
	return swarm.MemberContext{Name: name, Role: agentdef.RoleWorker, Space: sp}
}

// exec drives a tool's Execute with a background ctx + nop logger.
func exec(t *testing.T, tool pubtools.Tool, input string) pubtools.Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), pubtools.NopLogger(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("%s: unexpected Go error: %v", tool.Name(), err)
	}
	return res
}

// stubState is a minimal pkg/tools.State for factory tests.
type stubState struct{ cfg *config.Config }

func (s stubState) Config() *config.Config { return s.cfg }
func (stubState) Workdir() string          { return "" }
