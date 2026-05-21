package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/tools"
)

// seedDeepseek installs a fake credential so llmfactory.Of can build a
// client during the tests. Restores the prior key on cleanup.
func seedDeepseek(t *testing.T) {
	t.Helper()
	cfg := config.Get()
	prev := cfg.LLMProviderConfig[constant.DEEPSEEK.Name]
	t.Cleanup(func() {
		if prev.ApiSecret == "" {
			_ = cfg.SetProviderAPIKey(constant.DEEPSEEK.Name, "")
		} else {
			_ = cfg.SetProviderAPIKey(constant.DEEPSEEK.Name, prev.ApiSecret)
		}
	})
	if err := cfg.SetProviderAPIKey(constant.DEEPSEEK.Name, "test-dskey"); err != nil {
		t.Fatalf("seed deepseek: %v", err)
	}
}

// TestSwitchProfile_BuiltinToDisk constructs an agent on the built-in
// evva persona, drops a disk persona "nono" under a temp EVVA_HOME,
// then switches via SwitchProfile. Verifies: persona name updates,
// session is reset, the active tool list reflects the new profile,
// and DefaultProfile is persisted to config.
func TestSwitchProfile_BuiltinToDisk(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()

	// Build a registry containing only built-ins + a synthetic "nono"
	// disk persona that lists a single active tool (read) so we can
	// observe the rebuild side-effect.
	home := t.TempDir()
	agentsDir := filepath.Join(home, "agents")
	if err := os.MkdirAll(filepath.Join(agentsDir, "nono"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile := func(name, body string) {
		if err := os.WriteFile(filepath.Join(agentsDir, "nono", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("system_prompt.md", "You are nono, a numbers persona.\n")
	writeFile("tools.yml", "active: [read]\ndeferred: []\n")
	writeFile("meta.yml", "as: [main, subagent]\nwhen_to_use: Use for finance questions.\n")

	reg, warns := BuildAgentRegistry(home)
	if len(warns) != 0 {
		t.Fatalf("unexpected registry warnings: %v", warns)
	}

	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile(evva): %v", err)
	}
	a, err := New(nil, prof,
		WithName("test"),
		WithAgentRegistry(reg),
		WithPersona("evva"),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "hello evva"})

	prevDefault := cfg.DefaultProfile
	t.Cleanup(func() { _ = cfg.SetDefaultProfile(prevDefault) })

	if err := a.SwitchProfile("nono"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}

	if a.ProfileName() != "nono" {
		t.Errorf("ProfileName: want nono, got %q", a.ProfileName())
	}
	if got := len(a.session.GetMessages()); got != 0 {
		t.Errorf("post-swap session: want 0, got %d", got)
	}
	// nono's tools.yml lists exactly one active tool (read).
	if _, ok := a.active[string(tools.READ_FILE)]; !ok {
		t.Errorf("post-swap active map missing %q", tools.READ_FILE)
	}
	if _, ok := a.active[string(tools.BASH)]; ok {
		t.Errorf("post-swap active map should not contain %q (not in nono's tools.yml)", tools.BASH)
	}
	if cfg.DefaultProfile != "nono" {
		t.Errorf("DefaultProfile not persisted: got %q", cfg.DefaultProfile)
	}
}

// TestSwitchProfile_RefusedFromSubagent ensures only the root agent can
// call SwitchProfile. The /profile picker only fires on the root, but
// defense in depth is cheap.
func TestSwitchProfile_RefusedFromSubagent(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()

	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	root, err := New(nil, prof, WithName("root"), WithAgentRegistry(reg))
	if err != nil {
		t.Fatalf("agent.New(root): %v", err)
	}
	// Fabricate a subagent — pass root as Parent so IsSubagent returns true.
	child, err := New(root, prof, WithName("child"), WithAgentRegistry(reg))
	if err != nil {
		t.Fatalf("agent.New(child): %v", err)
	}

	if err := child.SwitchProfile("evva"); err == nil {
		t.Fatal("expected error switching profile on a subagent")
	}
}

// TestSwitchProfile_RefusedWhileRunning mirrors SwitchLLM's running
// guard — a concurrent Run shouldn't get its tool map / system prompt
// yanked mid-call.
func TestSwitchProfile_RefusedWhileRunning(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()

	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof, WithName("test"), WithAgentRegistry(reg))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	a.running.Store(true)
	defer a.running.Store(false)
	if err := a.SwitchProfile("evva"); err == nil {
		t.Fatal("expected ErrRunInProgress, got nil")
	}
}

// TestSwitchProfile_UnknownNameErrors covers the happy unhappy path: a
// typo at the picker shouldn't smash the agent.
func TestSwitchProfile_UnknownNameErrors(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()
	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof, WithName("test"), WithAgentRegistry(reg))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.SwitchProfile("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown profile name")
	}
}

// TestSwitchProfile_NonMainPersonaErrors guards against picking a
// subagent-only name (e.g. "explore") at the /profile picker.
func TestSwitchProfile_NonMainPersonaErrors(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()
	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof, WithName("test"), WithAgentRegistry(reg))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	err = a.SwitchProfile("explore")
	if err == nil {
		t.Fatal("expected error switching to a subagent-only persona")
	}
	if !strings.Contains(err.Error(), "not a main-tier") {
		t.Errorf("unexpected error: %v", err)
	}
}
