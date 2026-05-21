package agent

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// withProviderAPI installs a synthetic APIConfig under name for the
// duration of the test, restoring the previous entry on cleanup. Uses
// the process-wide singleton because the LLMProviderConfig map is shared
// — package-level tests already touch it via config.Get().
func withProviderAPI(t *testing.T, name string, api llm.APIConfig) {
	t.Helper()
	cfg := config.Get()
	if cfg.LLMProviderConfig == nil {
		cfg.LLMProviderConfig = make(map[string]llm.APIConfig)
	}
	prev, had := cfg.LLMProviderConfig[name]
	cfg.LLMProviderConfig[name] = api
	t.Cleanup(func() {
		if had {
			cfg.LLMProviderConfig[name] = prev
		} else {
			delete(cfg.LLMProviderConfig, name)
		}
	})
}

func withoutProviderAPI(t *testing.T, name string) {
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

func TestBuildLLMClient_MissingConfigErrors(t *testing.T) {
	withoutProviderAPI(t, constant.ANTHROPIC.Name)

	_, err := buildLLMClient(config.Get(), constant.ANTHROPIC, constant.SONNET_4_6, nil)
	if err == nil {
		t.Fatal("expected error when provider config is missing")
	}
	if !strings.Contains(err.Error(), "API_KEY not set") {
		t.Errorf("error should mention 'API_KEY not set'; got %q", err.Error())
	}
}

func TestBuildLLMClient_UnknownProviderErrors(t *testing.T) {
	unknown := constant.LLMProvider{Name: "totally-made-up", ApiUrl: "http://x"}
	withProviderAPI(t, unknown.Name, llm.APIConfig{ApiURL: unknown.ApiUrl, ApiSecret: "fake"})

	_, err := buildLLMClient(config.Get(), unknown, constant.Model("any"), nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error should mention 'unknown provider'; got %q", err.Error())
	}
}

func TestBuildLLMClient_AnthropicReturnsClient(t *testing.T) {
	withProviderAPI(t, constant.ANTHROPIC.Name, llm.APIConfig{
		ApiURL:    constant.ANTHROPIC.ApiUrl,
		ApiSecret: "fake-key",
		Models:    constant.ANTHROPIC.Models,
	})

	client, err := buildLLMClient(config.Get(), constant.ANTHROPIC, constant.SONNET_4_6, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := client.Name(), constant.ANTHROPIC.Name; got != want {
		t.Errorf("Name(): got %q, want %q", got, want)
	}
	if got, want := client.Model(), string(constant.SONNET_4_6); got != want {
		t.Errorf("Model(): got %q, want %q", got, want)
	}
}

func TestBuildLLMClient_NilConfigErrors(t *testing.T) {
	_, err := buildLLMClient(nil, constant.ANTHROPIC, constant.SONNET_4_6, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}
