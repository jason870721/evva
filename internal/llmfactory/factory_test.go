package llmfactory

import (
	"strings"
	"testing"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	_ "github.com/johnny1110/evva/pkg/llm/builtins" // populate the LLM registry for these tests
)

// Phase 1 analysis — Of code paths:
//   - Look up provider config in cfg.LLMProviderConfig (keyed by provider.Name).
//     Missing key → "API_KEY not set" error.
//   - Switch on provider.Name:
//       - "anthropic" → claude.New
//       - "deepseek"  → deepseek.New
//       - "ollama"    → ollama.New
//       - default     → "unknown provider" error
//   - The returned Client's Name() reports the provider.Name and Model() the
//     supplied model string.
//
// All tests mutate config.Get().LLMProviderConfig and save/restore the
// previous value to keep isolation across cases (and across the rest of
// the suite, which also reads the same singleton).

// withProviderConfig installs apiCfg under name for the duration of the
// test. Restores the previous value (or removes the entry if there was
// none) on cleanup.
func withProviderConfig(t *testing.T, name string, apiCfg config.LLMProviderAPIConfig) {
	t.Helper()
	cfg := config.Get()
	if cfg.LLMProviderConfig == nil {
		cfg.LLMProviderConfig = make(map[string]config.LLMProviderAPIConfig)
	}
	prev, had := cfg.LLMProviderConfig[name]
	cfg.LLMProviderConfig[name] = apiCfg
	t.Cleanup(func() {
		if had {
			cfg.LLMProviderConfig[name] = prev
		} else {
			delete(cfg.LLMProviderConfig, name)
		}
	})
}

// withoutProviderConfig removes the entry for name for the duration of
// the test. Restores it on cleanup if it was present.
func withoutProviderConfig(t *testing.T, name string) {
	t.Helper()
	cfg := config.Get()
	if cfg.LLMProviderConfig == nil {
		return
	}
	prev, had := cfg.LLMProviderConfig[name]
	delete(cfg.LLMProviderConfig, name)
	t.Cleanup(func() {
		if had {
			cfg.LLMProviderConfig[name] = prev
		}
	})
}

func TestOf_MissingProviderConfigErrors(t *testing.T) {
	withoutProviderConfig(t, constant.ANTHROPIC.Name)

	client, err := Of(constant.ANTHROPIC, constant.SONNET_4_6, nil)

	if err == nil {
		t.Fatal("expected error when provider config is missing")
	}
	if client != nil {
		t.Errorf("expected nil client on error; got %v", client)
	}
	if !strings.Contains(err.Error(), constant.ANTHROPIC.Name) {
		t.Errorf("error should mention provider name; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "API_KEY not set") {
		t.Errorf("error should mention 'API_KEY not set'; got %q", err.Error())
	}
}

func TestOf_Anthropic_ReturnsClaudeClient(t *testing.T) {
	withProviderConfig(t, constant.ANTHROPIC.Name, config.LLMProviderAPIConfig{
		ApiURL:    constant.ANTHROPIC.ApiUrl,
		ApiSecret: "fake-key",
		Models:    constant.ANTHROPIC.Models,
	})

	client, err := Of(constant.ANTHROPIC, constant.SONNET_4_6, nil)

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if got, want := client.Name(), constant.ANTHROPIC.Name; got != want {
		t.Errorf("Name(): got %q, want %q", got, want)
	}
	if got, want := client.Model(), string(constant.SONNET_4_6); got != want {
		t.Errorf("Model(): got %q, want %q", got, want)
	}
}

func TestOf_DeepSeek_ReturnsDeepSeekClient(t *testing.T) {
	withProviderConfig(t, constant.DEEPSEEK.Name, config.LLMProviderAPIConfig{
		ApiURL:    constant.DEEPSEEK.ApiUrl,
		ApiSecret: "fake-key",
		Models:    constant.DEEPSEEK.Models,
	})

	client, err := Of(constant.DEEPSEEK, constant.DEEPSEEK_V4_FLASH, nil)

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := client.Name(), constant.DEEPSEEK.Name; got != want {
		t.Errorf("Name(): got %q, want %q", got, want)
	}
	if got, want := client.Model(), string(constant.DEEPSEEK_V4_FLASH); got != want {
		t.Errorf("Model(): got %q, want %q", got, want)
	}
}

func TestOf_Ollama_ReturnsOllamaClient(t *testing.T) {
	withProviderConfig(t, constant.OLLAMA.Name, config.LLMProviderAPIConfig{
		ApiURL: constant.OLLAMA.ApiUrl,
		// Ollama is local — no secret required.
		Models: constant.OLLAMA.Models,
	})

	client, err := Of(constant.OLLAMA, constant.QWEN_3_6, nil)

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := client.Name(), constant.OLLAMA.Name; got != want {
		t.Errorf("Name(): got %q, want %q", got, want)
	}
	if got, want := client.Model(), string(constant.QWEN_3_6); got != want {
		t.Errorf("Model(): got %q, want %q", got, want)
	}
}

func TestOf_UnknownProvider_ErrorsAfterConfigLookupSucceeds(t *testing.T) {
	// Construct a synthetic provider with a name the switch doesn't handle.
	// We need to seed the config so the first guard passes; then the switch
	// falls through to the default branch.
	unknown := constant.LLMProvider{Name: "totally-made-up", ApiUrl: "http://x"}
	withProviderConfig(t, unknown.Name, config.LLMProviderAPIConfig{
		ApiURL:    unknown.ApiUrl,
		ApiSecret: "fake",
	})

	client, err := Of(unknown, constant.Model("any"), nil)

	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if client != nil {
		t.Errorf("expected nil client on error; got %v", client)
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error should mention 'unknown provider'; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), unknown.Name) {
		t.Errorf("error should echo the bad provider name; got %q", err.Error())
	}
}

func TestOf_OpenAI_FallsThroughToUnknownBranch(t *testing.T) {
	// OPENAI is in constant.GetAllProviders() and in the env-loaded config
	// when its API_KEY is set — but the factory's switch has NO case for
	// it. Seed the config so the missing-config guard passes, then verify
	// we land in the unknown-provider branch.
	withProviderConfig(t, constant.OPENAI.Name, config.LLMProviderAPIConfig{
		ApiURL:    constant.OPENAI.ApiUrl,
		ApiSecret: "fake-openai-key",
		Models:    constant.OPENAI.Models,
	})

	_, err := Of(constant.OPENAI, constant.GPT_5_5, nil)

	if err == nil {
		t.Fatal("expected error — factory has no OpenAI case")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("expected 'unknown provider' for OpenAI; got %q", err.Error())
	}
}

func TestOf_ForwardsOptionsToClient(t *testing.T) {
	withProviderConfig(t, constant.ANTHROPIC.Name, config.LLMProviderAPIConfig{
		ApiURL:    constant.ANTHROPIC.ApiUrl,
		ApiSecret: "fake-key",
	})

	// Build with an option and verify the constructed client returns
	// some value for Model() — we can't reach into private params from
	// outside the package, but we can prove options are accepted without
	// panic and the client is usable.
	client, err := Of(constant.ANTHROPIC, constant.SONNET_4_6, []llm.Option{
		llm.WithMaxTokens(2048),
		llm.WithSystem("be brief"),
	})

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if client.Model() != string(constant.SONNET_4_6) {
		t.Errorf("Model() after WithMaxTokens: got %q", client.Model())
	}
}

func TestOf_EmptyProviderName_ErrorsOnLookup(t *testing.T) {
	// Defensive: provider zero value (Name="") shouldn't match any config
	// entry and must fail the lookup guard, not crash.
	withoutProviderConfig(t, "")

	_, err := Of(constant.LLMProvider{}, "", nil)
	if err == nil {
		t.Fatal("expected error for zero-value provider")
	}
	if !strings.Contains(err.Error(), "API_KEY not set") {
		t.Errorf("expected missing-config message; got %q", err.Error())
	}
}
