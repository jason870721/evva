package agent_test

import (
	"context"
	"testing"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// These tests are the v2.4 acceptance gate: a downstream host builds a full
// agent — persona catalog, permission stance, memory + skills — from the ONE
// converged constructor agent.New(Config, opts...), with no hand-wired
// bootstrap. This file imports zero internal/*.
//
// stubClient lives in downstream_test.go; seedDiskAgent + recordingSink in the
// sibling downstream test files (same agent_test package).

func newConvergedConfig(t *testing.T, home string) *config.Config {
	t.Helper()
	const providerName = "converged_stub"
	if !llm.DefaultRegistry().Has(providerName) {
		err := llm.DefaultRegistry().Register(providerName, func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &stubClient{name: providerName, model: model}, nil
		})
		if err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}
	cfg, err := config.Load(config.LoadOptions{AppName: "converged_test", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.LLMProviderConfig[providerName] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "fake"}
	cfg.DefaultProvider = constant.LLMProvider{Name: providerName, Models: []constant.Model{constant.Model("stub-model")}}
	cfg.DefaultModel = constant.Model("stub-model")
	return cfg
}

// One constructor, default-from-disk catalog: a host that passes neither a
// persona registry nor a persona name still gets the built-in evva plus any
// on-disk personas, with a working /profile picker and a live switch — all
// from agent.New(Config{}).
func TestDownstream_OneConstructor_DefaultRegistry(t *testing.T) {
	home := t.TempDir()
	seedDiskAgent(t, home, "nono", []string{"main", "subagent"})

	cfg := newConvergedConfig(t, home)
	sink := &recordingSink{}

	ag, err := agent.New(agent.Config{
		AppConfig:      cfg,
		PermissionMode: "default",
	}, agent.WithSink(sink), agent.WithMaxIterations(5))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	if got := ag.ProfileName(); got != "evva" {
		t.Errorf("ProfileName = %q, want evva (default)", got)
	}
	if got := ag.PermissionModeName(); got != "default" {
		t.Errorf("PermissionModeName = %q, want default", got)
	}

	have := map[string]bool{}
	for _, p := range ag.ListMainProfiles() {
		have[p.Name] = true
	}
	for _, want := range []string{"evva", "nono"} {
		if !have[want] {
			t.Errorf("/profile picker missing %q; got %v", want, have)
		}
	}

	// The catalog the constructor built from disk drives a live persona switch.
	if err := ag.SwitchProfile("nono"); err != nil {
		t.Fatalf("SwitchProfile(nono): %v", err)
	}
	if got := ag.ProfileName(); got != "nono" {
		t.Errorf("ProfileName after switch = %q, want nono", got)
	}

	if _, err := ag.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// One constructor, in-code persona + headless bypass: a host registers its own
// main persona and boots the agent as that persona with bypass permissions —
// every choice declared on Config, no option plumbing beyond the sink.
func TestDownstream_OneConstructor_InCodePersona(t *testing.T) {
	home := t.TempDir()
	cfg := newConvergedConfig(t, home)

	reg, _ := agent.BuildAgentRegistry(home)
	reg.Register(agent.AgentDefinition{
		Name:         "finbot",
		WhenToUse:    "financial questions",
		As:           []string{"main"},
		SystemPrompt: "You are finbot.",
	})

	ag, err := agent.New(agent.Config{
		AppConfig:      cfg,
		Personas:       reg,
		Persona:        "finbot",
		PermissionMode: "bypass",
	}, agent.WithSink(&recordingSink{}))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	if got := ag.ProfileName(); got != "finbot" {
		t.Errorf("ProfileName = %q, want finbot", got)
	}
	if got := ag.PermissionModeName(); got != "bypass" {
		t.Errorf("PermissionModeName = %q, want bypass", got)
	}

	have := map[string]bool{}
	for _, p := range ag.ListMainProfiles() {
		have[p.Name] = true
	}
	for _, want := range []string{"evva", "finbot"} {
		if !have[want] {
			t.Errorf("/profile picker missing %q; got %v", want, have)
		}
	}

	if _, err := ag.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// An unknown persona name degrades to evva rather than failing the boot.
func TestDownstream_OneConstructor_UnknownPersonaFallsBack(t *testing.T) {
	home := t.TempDir()
	cfg := newConvergedConfig(t, home)

	ag, err := agent.New(agent.Config{
		AppConfig: cfg,
		Persona:   "does-not-exist",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if got := ag.ProfileName(); got != "evva" {
		t.Errorf("ProfileName = %q, want evva (fallback)", got)
	}
}
