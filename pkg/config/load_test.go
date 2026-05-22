package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadTwoConfigsInOneProcess proves the singleton-free design: two
// Load calls with different LoadOptions produce two distinct Config
// instances whose AppHome / AppName / WorkDir do not bleed into each
// other. This is the key invariant Phase 13a enabled — multi-tenant
// hosts running several agents must not share a global config.
func TestLoadTwoConfigsInOneProcess(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	wdA := t.TempDir()
	wdB := t.TempDir()

	cfgA, err := Load(LoadOptions{AppName: "alpha", AppHome: homeA, WorkDir: wdA})
	if err != nil {
		t.Fatalf("Load alpha: %v", err)
	}
	cfgB, err := Load(LoadOptions{AppName: "beta", AppHome: homeB, WorkDir: wdB})
	if err != nil {
		t.Fatalf("Load beta: %v", err)
	}

	if cfgA == cfgB {
		t.Fatal("two Loads must return distinct pointers")
	}
	if cfgA.AppName != "alpha" || cfgB.AppName != "beta" {
		t.Errorf("AppName mixed: %q vs %q", cfgA.AppName, cfgB.AppName)
	}
	if cfgA.AppHome != homeA || cfgB.AppHome != homeB {
		t.Errorf("AppHome mixed: %q vs %q", cfgA.AppHome, cfgB.AppHome)
	}
	if cfgA.WorkDir != wdA || cfgB.WorkDir != wdB {
		t.Errorf("WorkDir mixed: %q vs %q", cfgA.WorkDir, cfgB.WorkDir)
	}

	// Workdir-local skills dir derives from .{AppName}/skills so two apps
	// in the same workdir still get isolated paths.
	wantA := filepath.Join(wdA, ".alpha", "skills")
	wantB := filepath.Join(wdB, ".beta", "skills")
	if cfgA.WorkDirSkillsDir != wantA {
		t.Errorf("alpha WorkDirSkillsDir: got %q, want %q", cfgA.WorkDirSkillsDir, wantA)
	}
	if cfgB.WorkDirSkillsDir != wantB {
		t.Errorf("beta WorkDirSkillsDir: got %q, want %q", cfgB.WorkDirSkillsDir, wantB)
	}

	// AppHome layout follows AppName as well.
	if !strings.HasSuffix(cfgA.AppHomeConfigFile, "alpha-config.yml") {
		t.Errorf("alpha config file should be alpha-config.yml; got %q", cfgA.AppHomeConfigFile)
	}
	if !strings.HasSuffix(cfgB.AppHomeConfigFile, "beta-config.yml") {
		t.Errorf("beta config file should be beta-config.yml; got %q", cfgB.AppHomeConfigFile)
	}
}

// TestLoadDefaultUsesEvvaName locks down LoadDefault's backward-compat
// behavior: ~/.evva, evva-config.yml, AppName="evva". Critical because
// cmd/evva keeps booting through this path.
func TestLoadDefaultAppName(t *testing.T) {
	// LoadDefault writes to the user's actual home dir on first launch
	// (creates a fresh evva-config.yml). Skip if running in CI where the
	// home dir may not be writable. Use Load with the default AppName
	// instead — it exercises the same code path against a temp home.
	cfg, err := Load(LoadOptions{AppName: DefaultAppName, AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppName != "evva" {
		t.Errorf("AppName: got %q, want evva", cfg.AppName)
	}
	if !strings.HasSuffix(cfg.AppHomeConfigFile, "evva-config.yml") {
		t.Errorf("AppHomeConfigFile: got %q", cfg.AppHomeConfigFile)
	}
}

// TestSetProviderCredentials covers the Phase 19b thread-safe setter:
// empty name rejected, existing Models slice preserved, repeated writes
// last-write-wins.
func TestSetProviderCredentials(t *testing.T) {
	cfg, err := Load(LoadOptions{AppName: "alpha", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.SetProviderCredentials("", "x", "y"); err == nil {
		t.Error("expected error for empty provider name")
	}

	if err := cfg.SetProviderCredentials("custom-llm", "https://api.example/v1", "sk-test"); err != nil {
		t.Fatalf("SetProviderCredentials: %v", err)
	}
	got := cfg.LLMProviderConfig["custom-llm"]
	if got.ApiURL != "https://api.example/v1" || got.ApiSecret != "sk-test" {
		t.Errorf("creds: got %+v", got)
	}

	// Last-write-wins.
	if err := cfg.SetProviderCredentials("custom-llm", "https://second", "sk-second"); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	got = cfg.LLMProviderConfig["custom-llm"]
	if got.ApiSecret != "sk-second" {
		t.Errorf("re-set didn't overwrite: %+v", got)
	}
}

// TestLoadEnvAliases verifies Phase 19b LoadOptions.EnvAliases: setting
// a friday-flavoured alias like LOGDIR before Load promotes into
// LOG_DIR, which evva's loader then picks up natively. Non-overriding:
// an existing canonical export wins.
func TestLoadEnvAliases(t *testing.T) {
	tmpLog := t.TempDir()
	os.Setenv("FRIDAY_LOGDIR_ALIAS", tmpLog)
	defer os.Unsetenv("FRIDAY_LOGDIR_ALIAS")
	defer os.Unsetenv("LOG_DIR")

	// Ensure LOG_DIR is empty so the alias promotion actually fires.
	os.Unsetenv("LOG_DIR")

	cfg, err := Load(LoadOptions{
		AppName: "alpha",
		AppHome: t.TempDir(),
		WorkDir: t.TempDir(),
		EnvAliases: map[string]string{
			"FRIDAY_LOGDIR_ALIAS": "LOG_DIR",
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogDir == nil || *cfg.LogDir != tmpLog {
		got := "<nil>"
		if cfg.LogDir != nil {
			got = *cfg.LogDir
		}
		t.Errorf("LogDir: got %q, want %q (alias promotion failed)", got, tmpLog)
	}
}

// TestLoadEnvOverrides verifies LoadOptions.EnvOverrides:
// post-Load mutations applied in declaration order, first error short-
// circuits the rest, and the wrapped error names the failing override.
func TestLoadEnvOverrides(t *testing.T) {
	called := 0
	cfg, err := Load(LoadOptions{
		AppName: "alpha",
		AppHome: t.TempDir(),
		WorkDir: t.TempDir(),
		EnvOverrides: []EnvOverride{
			{Name: "set_max_iters", Fn: func(c *Config) error {
				called++
				return c.SetMaxIterations(99)
			}},
			{Name: "noop", Fn: func(c *Config) error {
				called++
				return nil
			}},
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if called != 2 {
		t.Errorf("expected 2 overrides invoked, got %d", called)
	}
	if cfg.DefaultMaxIterations != 99 {
		t.Errorf("override didn't mutate MaxIterations: got %d", cfg.DefaultMaxIterations)
	}

	// First error short-circuits AND the wrapped error names the override.
	called = 0
	_, err = Load(LoadOptions{
		AppName: "beta",
		AppHome: t.TempDir(),
		WorkDir: t.TempDir(),
		EnvOverrides: []EnvOverride{
			{Name: "failing_one", Fn: func(c *Config) error {
				called++
				return fmt.Errorf("boom")
			}},
			{Name: "never_runs", Fn: func(c *Config) error { called++; return nil }},
		},
	})
	if err == nil {
		t.Fatal("expected first override's error to short-circuit Load")
	}
	if !strings.Contains(err.Error(), "failing_one") {
		t.Errorf("wrapped error should name the failing override; got %q", err.Error())
	}
	if called != 1 {
		t.Errorf("override #2 should not have run; called=%d", called)
	}
}

// TestLoadEnvOverridesRejectEmptyName covers the validation check —
// an EnvOverride entry without a Name produces an unhelpful
// "EnvOverrides[]" wrapper, so Load rejects it at validation time.
func TestLoadEnvOverridesRejectEmptyName(t *testing.T) {
	_, err := Load(LoadOptions{
		AppName: "alpha",
		AppHome: t.TempDir(),
		WorkDir: t.TempDir(),
		EnvOverrides: []EnvOverride{
			{Name: "", Fn: func(c *Config) error { return nil }},
		},
	})
	if err == nil {
		t.Fatal("expected Load to reject nameless EnvOverride")
	}
	if !strings.Contains(err.Error(), "Name is required") {
		t.Errorf("error should mention Name is required; got %q", err.Error())
	}
}

// TestLoadProviderCredentials covers the declarative provider-creds
// wiring. EnvAliases promote the friendly name, ProviderCredentials
// reads the canonical env var and installs the result on cfg.
func TestLoadProviderCredentials(t *testing.T) {
	os.Setenv("DEEPSEEK_API_KEY_TEST", "sk-from-env")
	defer os.Unsetenv("DEEPSEEK_API_KEY_TEST")

	cfg, err := Load(LoadOptions{
		AppName: "alpha",
		AppHome: t.TempDir(),
		WorkDir: t.TempDir(),
		ProviderCredentials: map[string]ProviderCredsFromEnv{
			"deepseek": {
				APIKeyEnv:     "DEEPSEEK_API_KEY_TEST",
				APIURLDefault: "https://api.example/v1",
			},
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.LLMProviderConfig["deepseek"]
	if got.ApiSecret != "sk-from-env" {
		t.Errorf("ApiSecret: got %q, want %q", got.ApiSecret, "sk-from-env")
	}
	if got.ApiURL != "https://api.example/v1" {
		t.Errorf("ApiURL: got %q, want default URL", got.ApiURL)
	}
}

// TestLoadSeedEnvTemplate covers the first-run .env seeding: a missing
// .env file is created from the template; an existing one is never
// overwritten.
func TestLoadSeedEnvTemplate(t *testing.T) {
	home := t.TempDir()
	template := "DEEPSEEK_API_KEY=\nLOG_LEVEL=info\n"

	_, err := Load(LoadOptions{
		AppName:         "alpha",
		AppHome:         home,
		WorkDir:         t.TempDir(),
		SeedEnvTemplate: template,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	envPath := filepath.Join(home, ".env")
	got, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read seeded .env: %v", err)
	}
	if string(got) != template {
		t.Errorf("seeded .env mismatch:\ngot:  %q\nwant: %q", got, template)
	}

	// Second Load with a different template must NOT overwrite the
	// existing file.
	_, err = Load(LoadOptions{
		AppName:         "alpha",
		AppHome:         home,
		WorkDir:         t.TempDir(),
		SeedEnvTemplate: "OVERWRITE_ATTEMPT=yes",
	})
	if err != nil {
		t.Fatalf("Load (second): %v", err)
	}
	got2, _ := os.ReadFile(envPath)
	if string(got2) != template {
		t.Errorf("second Load clobbered .env:\ngot:  %q\nwant: %q", got2, template)
	}
}

// TestLoadFirstRunYAMLStampsAppName verifies Phase 19b first-launch
// behavior: a friday-flavoured Load writes default_profile=friday into
// the seeded YAML, not the hardcoded "evva".
func TestLoadFirstRunYAMLStampsAppName(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(LoadOptions{AppName: "friday", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProfile != "friday" {
		t.Errorf("DefaultProfile: got %q, want %q", cfg.DefaultProfile, "friday")
	}

	// Re-read the YAML on disk to confirm the stamp actually landed
	// there (not just in-memory).
	data, err := os.ReadFile(cfg.AppHomeConfigFile)
	if err != nil {
		t.Fatalf("read YAML: %v", err)
	}
	if !strings.Contains(string(data), "default_profile: friday") {
		t.Errorf("YAML missing default_profile=friday\n%s", data)
	}
}
