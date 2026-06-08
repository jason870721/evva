package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/tools"
)

// echoTool is the canonical downstream-style custom tool: takes a payload,
// returns it verbatim. Demonstrates that custom tools see the same
// pkg/tools.State + logger + ctx contract as built-ins. Name is
// parametrised so each test gets a unique registry entry — the global
// pkg/toolset.DefaultRegistry rejects duplicates and tests must not
// share names.
type echoTool struct{ name string }

func (e echoTool) Name() string            { return e.name }
func (echoTool) Description() string       { return "echoes its input back" }
func (echoTool) Schema() json.RawMessage   { return json.RawMessage(`{"type":"object"}`) }
func (echoTool) Execute(_ context.Context, _ *slog.Logger, in json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: "echo: " + string(in)}, nil
}

// TestWithCustomTool_RegistersAndExposes builds an agent against a tiny
// profile and threads a custom tool through WithCustomTool. The tool is
// expected to land in the agent's active set so the LLM can invoke it,
// proving the downstream extension path end-to-end.
func TestWithCustomTool_RegistersAndExposes(t *testing.T) {
	// Use a unique name so re-running the suite doesn't trip the
	// "already registered" idempotency guard on the global registry.
	name := tools.ToolName("test_echo_" + t.Name())

	prof := Profile{
		Type:        GENERAL_PURPOSE,
		ActiveTools: []tools.ToolName{},
		LLMProvider: constant.ANTHROPIC,
		LLMModel:    constant.SONNET_4_6,
	}
	withProviderAPI(t, constant.ANTHROPIC.Name, config.APIConfig{
		ApiURL:    constant.ANTHROPIC.ApiUrl,
		ApiSecret: "fake-key",
	})

	a, err := New(nil, prof,
		WithCustomTool(name, func(s tools.State) (tools.Tool, error) {
			return echoTool{name: string(name)}, nil
		}),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	tool, ok := a.active[string(name)]
	if !ok {
		t.Fatalf("custom tool %q missing from active set; have %v", name, mapKeys(a.active))
	}
	if !strings.Contains(tool.Description(), "echoes") {
		t.Errorf("unexpected description: %q", tool.Description())
	}

	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(res.Content, "echo: ") {
		t.Errorf("unexpected echo result: %q", res.Content)
	}
}

// TestActiveSetRebuild_PreservesCustomTools is the regression guard for the
// Veronica swarm bug where a restart-resume (or any active-set rebuild) silently
// revoked every WithCustomTool-injected tool. The leader created tasks fine, then
// after an approval timeout + reload, task_assign/send_message/etc. all returned
// "not in active set or deferred allowlist" — only the profile's static tools
// survived. SwitchProfile/ResumeSnapshot/SwitchWorkdir all rebuild through the
// shared activeToolNames helper, so exercising one (SwitchProfile) proves the
// custom tools are re-merged on every rebuild.
func TestActiveSetRebuild_PreservesCustomTools(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()

	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile(evva): %v", err)
	}

	name := tools.ToolName("test_echo_" + t.Name())
	a, err := New(nil, prof,
		WithName("test"),
		WithAgentRegistry(reg),
		WithPersona("evva"),
		WithCustomTool(name, func(s tools.State) (tools.Tool, error) {
			return echoTool{name: string(name)}, nil
		}),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	// Present before the rebuild (the New() merge path).
	if _, err := a.ResolveTool(name); err != nil {
		t.Fatalf("custom tool not resolvable pre-rebuild: %v", err)
	}

	prevDefault := cfg.DefaultProfile
	t.Cleanup(func() { _ = cfg.SetDefaultProfile(prevDefault) })

	// A profile switch rebuilds the active set. Before the fix this dropped
	// every custom tool; now activeToolNames re-merges them.
	if err := a.SwitchProfile("evva"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}

	if _, err := a.ResolveTool(name); err != nil {
		t.Fatalf("custom tool revoked after active-set rebuild (regression): %v; active=%v", err, mapKeys(a.active))
	}
}

func mapKeys(m map[string]tools.Tool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
