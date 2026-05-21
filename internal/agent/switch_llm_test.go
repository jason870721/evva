package agent

import (
	"testing"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/internal/memdir"
)

// TestSwitchLLMClearsSessionAndUpdatesProfile drives the phase-3 swap
// flow end-to-end: seed config with credentials for two providers,
// construct an agent on one of them, push a message into the session,
// swap to the other, and verify (a) the session is empty, (b)
// a.profile.LLMProvider / LLMModel reflect the new pair, (c) a.llm was
// replaced (Name() reports the new provider).
//
// The test mutates the process-wide config singleton, but only fields
// the rest of the suite doesn't depend on; restoring on cleanup keeps
// other tests insulated.
func TestSwitchLLMClearsSessionAndUpdatesProfile(t *testing.T) {
	cfg := config.Get()
	// Seed test credentials for both providers we're going to bounce
	// between. SetProviderAPIKey persists to disk; capture and restore
	// the prior state so we don't trample the user's real config.
	prevAnthropic := cfg.LLMProviderConfig[constant.ANTHROPIC.Name]
	prevDeepseek := cfg.LLMProviderConfig[constant.DEEPSEEK.Name]
	t.Cleanup(func() {
		if prevAnthropic.ApiSecret == "" {
			_ = cfg.SetProviderAPIKey(constant.ANTHROPIC.Name, "")
		} else {
			_ = cfg.SetProviderAPIKey(constant.ANTHROPIC.Name, prevAnthropic.ApiSecret)
		}
		if prevDeepseek.ApiSecret == "" {
			_ = cfg.SetProviderAPIKey(constant.DEEPSEEK.Name, "")
		} else {
			_ = cfg.SetProviderAPIKey(constant.DEEPSEEK.Name, prevDeepseek.ApiSecret)
		}
	})
	if err := cfg.SetProviderAPIKey(constant.DEEPSEEK.Name, "test-dskey"); err != nil {
		t.Fatalf("seed deepseek: %v", err)
	}
	if err := cfg.SetProviderAPIKey(constant.ANTHROPIC.Name, "test-anthkey"); err != nil {
		t.Fatalf("seed anthropic: %v", err)
	}

	prof := Main(cfg, constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO, nil, memdir.Snapshot{}, nil)
	a, err := New(nil, prof, WithName("test"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if got := len(a.session.GetMessages()); got != 1 {
		t.Fatalf("pre-swap session len: want 1, got %d", got)
	}

	if err := a.SwitchLLM(constant.ANTHROPIC, constant.OPUS_4_7); err != nil {
		t.Fatalf("SwitchLLM: %v", err)
	}

	if got := len(a.session.GetMessages()); got != 0 {
		t.Errorf("post-swap session len: want 0, got %d", got)
	}
	if a.profile.LLMProvider.Name != constant.ANTHROPIC.Name {
		t.Errorf("profile provider: want anthropic, got %s", a.profile.LLMProvider.Name)
	}
	if a.profile.LLMModel != constant.OPUS_4_7 {
		t.Errorf("profile model: want %s, got %s", constant.OPUS_4_7, a.profile.LLMModel)
	}
	if a.llm.Name() != constant.ANTHROPIC.Name {
		t.Errorf("llm.Name(): want anthropic, got %s", a.llm.Name())
	}
}

// TestSwitchLLMRefusedWhileRunning verifies the running guard — a
// concurrent Run shouldn't get its a.llm yanked out mid-call.
func TestSwitchLLMRefusedWhileRunning(t *testing.T) {
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

	prof := Main(cfg, constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO, nil, memdir.Snapshot{}, nil)
	a, err := New(nil, prof, WithName("test"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	a.running.Store(true)
	defer a.running.Store(false)

	if err := a.SwitchLLM(constant.DEEPSEEK, constant.DEEPSEEK_V4_FLASH); err == nil {
		t.Fatal("expected ErrRunInProgress, got nil")
	}
}
