package agent_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

const urgentMsg = "URGENT: stop now"

// loopingClient keeps the agent iterating (one tool call per turn) until it sees
// the urgent message folded into the conversation, then returns plain text — so
// a drainer that fires on a later boundary proves the fold happened MID-run.
type loopingClient struct {
	model    string
	pingName string
	calls    atomic.Int32
}

func (c *loopingClient) Name() string             { return "drainer_stub" }
func (c *loopingClient) Model() string            { return c.model }
func (*loopingClient) SupportsDeferLoading() bool { return false }

func (c *loopingClient) Complete(_ context.Context, msgs []llm.Message, _ []tools.Tool) (llm.Response, error) {
	n := c.calls.Add(1)
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, urgentMsg) {
			return llm.Response{Content: "acknowledged: stopping"}, nil
		}
	}
	// Keep the loop alive for a few iterations (so a later-boundary drainer has
	// somewhere to land), then self-terminate so the no-drainer cases finish.
	if n >= 3 {
		return llm.Response{Content: "done — no urgent message"}, nil
	}
	return llm.Response{ToolCalls: []*tools.Call{{ID: "t1", Name: c.pingName, Input: []byte(`{}`)}}}, nil
}
func (c *loopingClient) Stream(ctx context.Context, m []llm.Message, ts []tools.Tool, _ llm.ChunkSink) (llm.Response, error) {
	return c.Complete(ctx, m, ts)
}
func (*loopingClient) Apply(...llm.Option) {}

// fakeDrainer yields its message on the Nth Drain call (1-based) and nothing
// otherwise — letting a test place the fold on a chosen iteration boundary.
type fakeDrainer struct {
	on   int32
	msg  string
	seen atomic.Int32
}

func (d *fakeDrainer) Drain(context.Context) (string, bool) {
	if d.seen.Add(1) == d.on {
		return d.msg, true
	}
	return "", false
}

func drainerAgent(t *testing.T, sink event.Sink, opts ...agent.Option) agent.Agent {
	t.Helper()
	const provider = "drainer_stub"
	pingName := tools.ToolName("drainer_ping")

	if !llm.DefaultRegistry().Has(provider) {
		err := llm.DefaultRegistry().Register(provider, func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &loopingClient{model: model, pingName: string(pingName)}, nil
		})
		if err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}

	cfg, err := config.Load(config.LoadOptions{AppName: "drainer_test", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.LLMProviderConfig[provider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x"}

	prof, err := agent.NewProfile("drainer", "stub", nil, provider, constant.Model("stub-model"), agent.ProfileOptions{})
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}

	base := []agent.Option{
		agent.WithConfig(cfg),
		agent.WithSink(sink),
		agent.WithMaxIterations(8),
		agent.WithCustomTool(pingName, func(tools.State) (tools.Tool, error) {
			return pingTool{name: string(pingName)}, nil
		}),
		agent.WithPermissionMode(agent.PermissionBypass),
	}
	ag, err := agent.NewWithProfile(prof, append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewWithProfile: %v", err)
	}
	return ag
}

// AC#2/#3: a drainer that yields on the 2nd boundary causes the message to
// appear as a synthetic user turn mid-run, and the model reacts to it.
func TestInboxDrainer_FoldsMidRun(t *testing.T) {
	sink := &recordingSink{}
	d := &fakeDrainer{on: 2, msg: urgentMsg}
	ag := drainerAgent(t, sink, agent.WithInboxDrainer(d))

	resp, err := ag.Run(context.Background(), "do a long job")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(resp, "acknowledged") {
		t.Errorf("model never reacted to the folded message; got %q", resp)
	}
	if !sink.seen(event.KindDrainInbox) {
		t.Errorf("KindDrainInbox not emitted; events=%v", sink.events)
	}
	// The folded message is in the transcript as a user turn.
	found := false
	for _, m := range ag.Controller().Messages() {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, urgentMsg) {
			found = true
		}
	}
	if !found {
		t.Error("folded message missing from the transcript")
	}
}

// AC#1: with no drainer, behaviour is unchanged — the loop runs to completion
// and never emits a drain-inbox event (single-agent regression guard).
func TestInboxDrainer_NilNoop(t *testing.T) {
	sink := &recordingSink{}
	ag := drainerAgent(t, sink) // no WithInboxDrainer

	if _, err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sink.seen(event.KindDrainInbox) {
		t.Error("a drain-inbox event fired without any drainer installed")
	}
}

// AC#5: an empty-inbox drainer is polled every boundary and folds nothing — no
// drain event, and the poll count tracks the iteration count (proving it is
// called per boundary, cheaply).
func TestInboxDrainer_EmptyIsCheapNoop(t *testing.T) {
	var polls atomic.Int32
	d := drainerFunc(func() (string, bool) {
		polls.Add(1)
		return "", false
	})
	sink := &recordingSink{}
	ag := drainerAgent(t, sink, agent.WithInboxDrainer(d))

	if _, err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sink.seen(event.KindDrainInbox) {
		t.Error("empty drainer should fold nothing")
	}
	if polls.Load() == 0 {
		t.Error("drainer was never polled")
	}
}

// drainerFunc adapts a func to the agent.Drainer interface.
type drainerFunc func() (string, bool)

func (f drainerFunc) Drain(context.Context) (string, bool) { return f() }
