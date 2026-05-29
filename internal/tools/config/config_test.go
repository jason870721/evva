package configtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
)

// loadTemp builds a real *config.Config rooted at a temp AppHome so SaveFile
// round-trips to disk. It returns the config and the AppHome so a test can
// reload and assert persistence.
func loadTemp(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	cfg, err := config.Load(config.LoadOptions{
		AppName: "configtool-test",
		AppHome: home,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg, home
}

func run(t *testing.T, tool *Tool, in string) tools.Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(in))
	if err != nil {
		t.Fatalf("Execute returned a transport error (should never happen): %v", err)
	}
	return res
}

// A2 — GET, known setting.
func TestConfigGet(t *testing.T) {
	cfg, _ := loadTemp(t)
	if err := cfg.SetDisplayThinking(true); err != nil {
		t.Fatal(err)
	}
	tool := New(cfg)
	res := run(t, tool, `{"setting":"display_thinking"}`)
	if res.IsError {
		t.Fatalf("GET errored: %s", res.Content)
	}
	if res.Content != "display_thinking = true" {
		t.Errorf("GET content = %q, want %q", res.Content, "display_thinking = true")
	}
}

// A3 — GET, unknown setting.
func TestConfigGetUnknown(t *testing.T) {
	cfg, _ := loadTemp(t)
	res := run(t, New(cfg), `{"setting":"nope"}`)
	if !res.IsError {
		t.Fatal("unknown setting should be an error")
	}
	if res.Content != `Unknown setting: "nope"` {
		t.Errorf("content = %q, want %q", res.Content, `Unknown setting: "nope"`)
	}
}

// A4 — SET, valid value (assert persistence via reload).
func TestConfigSetValid(t *testing.T) {
	cfg, home := loadTemp(t)
	if err := cfg.SetDisplayThinking(true); err != nil {
		t.Fatal(err)
	}
	res := run(t, New(cfg), `{"setting":"display_thinking","value":false}`)
	if res.IsError {
		t.Fatalf("SET errored: %s", res.Content)
	}
	if res.Content != "Set display_thinking to false" {
		t.Errorf("content = %q, want %q", res.Content, "Set display_thinking to false")
	}
	if cfg.GetDisplayThinking() {
		t.Error("in-memory DisplayThinking still true after SET false")
	}
	// Reload from the same AppHome — the change must have hit the YAML.
	reloaded, err := config.Load(config.LoadOptions{AppName: "configtool-test", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetDisplayThinking() {
		t.Error("reloaded DisplayThinking still true — SaveFile did not persist")
	}
}

// A5 — SET, invalid value (typed): the typed setter's error surfaces.
func TestConfigSetInvalidTyped(t *testing.T) {
	cfg, _ := loadTemp(t)
	res := run(t, New(cfg), `{"setting":"max_iterations","value":-3}`)
	if !res.IsError {
		t.Fatal("negative max_iterations should error")
	}
	if res.Content != "max_iterations: max_iterations must be > 0, got -3" {
		t.Errorf("content = %q", res.Content)
	}
}

// A6 — SET, invalid value (enum).
func TestConfigSetInvalidEnum(t *testing.T) {
	cfg, _ := loadTemp(t)
	res := run(t, New(cfg), `{"setting":"default_effort","value":"insane"}`)
	if !res.IsError {
		t.Fatal("invalid effort should error")
	}
	if !strings.Contains(res.Content, "Invalid value") || !strings.Contains(res.Content, "low, medium, high, ultra") {
		t.Errorf("content = %q, want an options-rejection message", res.Content)
	}
}

// A6b — SET, valid enum value goes through.
func TestConfigSetValidEnum(t *testing.T) {
	cfg, _ := loadTemp(t)
	res := run(t, New(cfg), `{"setting":"default_effort","value":"high"}`)
	if res.IsError {
		t.Fatalf("valid effort errored: %s", res.Content)
	}
	if cfg.Effort() != "high" {
		t.Errorf("effort = %q, want high", cfg.Effort())
	}
}

// A7 — boolean coercion: string "true" works, "yes" doesn't.
func TestConfigBoolCoercion(t *testing.T) {
	cfg, _ := loadTemp(t)
	tool := New(cfg)

	if res := run(t, tool, `{"setting":"display_thinking","value":"true"}`); res.IsError {
		t.Errorf(`value:"true" should coerce: %s`, res.Content)
	} else if !cfg.GetDisplayThinking() {
		t.Error(`value:"true" did not set the flag`)
	}

	res := run(t, tool, `{"setting":"display_thinking","value":"yes"}`)
	if !res.IsError {
		t.Error(`value:"yes" should be rejected`)
	}
	if !strings.Contains(res.Content, "true or false") {
		t.Errorf("content = %q, want a true/false rejection", res.Content)
	}
}

// A5b — non-integer float is rejected, not silently truncated.
func TestConfigIntRejectsFloat(t *testing.T) {
	cfg, _ := loadTemp(t)
	res := run(t, New(cfg), `{"setting":"max_iterations","value":3.7}`)
	if !res.IsError {
		t.Fatal("3.7 should be rejected for an int setting")
	}
	if cfg.GetMaxIterations() == 3 {
		t.Error("3.7 was truncated to 3 — data loss")
	}
}

// A8 — provider settings.
func TestConfigProviderKey(t *testing.T) {
	cfg, home := loadTemp(t)
	res := run(t, New(cfg), `{"setting":"openai.api_key","value":"sk-secret123"}`)
	if res.IsError {
		t.Fatalf("provider key SET errored: %s", res.Content)
	}
	if cfg.GetProviderAPIKey("openai") != "sk-secret123" {
		t.Errorf("in-memory openai key = %q", cfg.GetProviderAPIKey("openai"))
	}
	reloaded, err := config.Load(config.LoadOptions{AppName: "configtool-test", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetProviderAPIKey("openai") != "sk-secret123" {
		t.Errorf("reloaded openai key = %q — not persisted", reloaded.GetProviderAPIKey("openai"))
	}
}

// Secret values are masked on GET — the model never needs the raw key back.
func TestConfigSecretMaskedOnRead(t *testing.T) {
	cfg, _ := loadTemp(t)
	if err := cfg.SetTavilyAPIKey("abcd1234"); err != nil {
		t.Fatal(err)
	}
	res := run(t, New(cfg), `{"setting":"tavily_api_key"}`)
	if res.IsError {
		t.Fatalf("GET secret errored: %s", res.Content)
	}
	if res.Content != "tavily_api_key = ****1234" {
		t.Errorf("content = %q, want masked", res.Content)
	}
	if strings.Contains(res.Content, "abcd") {
		t.Error("raw secret leaked on read")
	}
}

// no config installed → clean error, no panic.
func TestConfigNilConfig(t *testing.T) {
	res := run(t, New(nil), `{"setting":"display_thinking"}`)
	if !res.IsError {
		t.Fatal("nil config should error")
	}
}

// malformed input → clean error.
func TestConfigBadInput(t *testing.T) {
	cfg, _ := loadTemp(t)
	res := run(t, New(cfg), `{"setting":}`)
	if !res.IsError {
		t.Fatal("malformed JSON should error")
	}
}
