package config

import (
	"os"
	"path/filepath"
	"testing"
)

// seedConfig writes body to the path Load actually reads
// (<home>/config/<app>-config.yml) and returns the home dir.
func seedConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evva-config.yml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// loadWithYAML seeds a config file and loads it.
func loadWithYAML(t *testing.T, body string) *Config {
	t.Helper()
	home := t.TempDir()
	seedConfig(t, home, body)
	cfg, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestRedactionDefaultsOn(t *testing.T) {
	// The inversion that matters: every other gate in this file defaults
	// off, and this one must not. An operator who has never heard of
	// redaction is exactly who it protects.
	cfg := loadWithYAML(t, "max_iterations: 20\n")
	if !cfg.GetRedaction() {
		t.Error("redaction should default to on when the key is absent")
	}
}

func TestRedactionCanBeTurnedOff(t *testing.T) {
	cfg := loadWithYAML(t, "redaction: false\n")
	if cfg.GetRedaction() {
		t.Error("redaction: false was not honoured")
	}
}

func TestRedactionListsRoundTrip(t *testing.T) {
	cfg := loadWithYAML(t, `
redaction: true
redaction_allow:
  - "^AKIAIOSFODNN7EXAMPLE$"
  - "example\\.com"
redaction_disable:
  - high-entropy
  - npm-token
`)
	allow := cfg.GetRedactionAllow()
	if len(allow) != 2 || allow[0] != "^AKIAIOSFODNN7EXAMPLE$" {
		t.Errorf("redaction_allow = %v", allow)
	}
	disable := cfg.GetRedactionDisable()
	if len(disable) != 2 || disable[1] != "npm-token" {
		t.Errorf("redaction_disable = %v", disable)
	}
}

func TestRedactionAccessorsDoNotAliasTheConfig(t *testing.T) {
	// The getters hand out slices; a caller mutating one must not be able
	// to reach into the live Config.
	cfg := loadWithYAML(t, "redaction_allow:\n  - \"one\"\n")
	got := cfg.GetRedactionAllow()
	got[0] = "mutated"
	if again := cfg.GetRedactionAllow(); again[0] != "one" {
		t.Errorf("caller mutation reached the Config: %v", again)
	}
}

func TestRedactionSurvivesSaveAndReload(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	seedConfig(t, home, "redaction: false\nredaction_allow:\n  - \"keepme\"\nredaction_disable:\n  - jwt\n")
	cfg, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: work})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveFile(); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	reloaded, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: work})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetRedaction() {
		t.Error("redaction: false did not survive the round trip")
	}
	if a := reloaded.GetRedactionAllow(); len(a) != 1 || a[0] != "keepme" {
		t.Errorf("redaction_allow lost in round trip: %v", a)
	}
	if d := reloaded.GetRedactionDisable(); len(d) != 1 || d[0] != "jwt" {
		t.Errorf("redaction_disable lost in round trip: %v", d)
	}
}

func TestSetRedactionPersists(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	cfg, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: work})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SetRedaction(false); err != nil {
		t.Fatalf("SetRedaction: %v", err)
	}
	reloaded, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: work})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetRedaction() {
		t.Error("SetRedaction(false) did not persist")
	}
}

func TestCloneCopiesRedactionSlices(t *testing.T) {
	cfg := loadWithYAML(t, "redaction_allow:\n  - \"one\"\n")
	clone := cfg.Clone()
	clone.RedactionAllow[0] = "mutated"
	if cfg.RedactionAllow[0] != "one" {
		t.Error("Clone aliased RedactionAllow instead of copying it")
	}
}
