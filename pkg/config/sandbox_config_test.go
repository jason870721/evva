package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadWithConfig(t *testing.T, body string) (*Config, error) {
	t.Helper()
	home := t.TempDir()
	if body != "" {
		writeConfigFile(t, home, body)
	}
	return Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: t.TempDir()})
}

// writeConfigFile seeds <AppHome>/config/evva-config.yml — the path Load
// actually reads (AppHomeConfigFile), not the AppHome root.
func writeConfigFile(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evva-config.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Sandboxing is opt-in: a config that never mentions it behaves exactly as
// before the wave.
func TestSandboxDefaultsOff(t *testing.T) {
	cfg, err := loadWithConfig(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetSandboxRuntime(); got != "" {
		t.Errorf("SandboxRuntime = %q, want empty (off)", got)
	}
	if got := cfg.GetSandboxNetwork(); got != "allow" {
		t.Errorf("SandboxNetwork = %q, want allow", got)
	}
	if got := cfg.GetSandboxImage(); got != "" {
		t.Errorf("SandboxImage = %q, want empty", got)
	}
}

func TestSandboxKnobsLoad(t *testing.T) {
	cfg, err := loadWithConfig(t, "sandbox_runtime: docker\nsandbox_image: golang:1.23\nsandbox_network: none\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetSandboxRuntime(); got != "docker" {
		t.Errorf("SandboxRuntime = %q", got)
	}
	if got := cfg.GetSandboxImage(); got != "golang:1.23" {
		t.Errorf("SandboxImage = %q", got)
	}
	if got := cfg.GetSandboxNetwork(); got != "none" {
		t.Errorf("SandboxNetwork = %q", got)
	}
}

// A typo'd runtime is a startup error, not a surprise the first time a
// subagent asks for isolation.
func TestSandboxRuntimeValidatedAtLoad(t *testing.T) {
	_, err := loadWithConfig(t, "sandbox_runtime: dokcer\n")
	if err == nil {
		t.Fatal("want an error for an unknown sandbox_runtime")
	}
	if !strings.Contains(err.Error(), "sandbox_runtime") {
		t.Errorf("error should name the key, got: %v", err)
	}
}

func TestSandboxNetworkValidatedAtLoad(t *testing.T) {
	_, err := loadWithConfig(t, "sandbox_network: sometimes\n")
	if err == nil {
		t.Fatal("want an error for an unknown sandbox_network")
	}
	if !strings.Contains(err.Error(), "sandbox_network") {
		t.Errorf("error should name the key, got: %v", err)
	}
}

func TestSandboxRuntimeCaseAndSpaceTolerant(t *testing.T) {
	cfg, err := loadWithConfig(t, "sandbox_runtime: \"  Docker \"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetSandboxRuntime(); got != "docker" {
		t.Errorf("SandboxRuntime = %q, want normalized to docker", got)
	}
}

func TestSandboxSurvivesClone(t *testing.T) {
	cfg, err := loadWithConfig(t, "sandbox_runtime: podman\nsandbox_image: img:1\nsandbox_network: none\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	clone := cfg.Clone()
	if clone.SandboxRuntime != "podman" || clone.SandboxImage != "img:1" || clone.SandboxNetwork != "none" {
		t.Errorf("Clone dropped sandbox knobs: %+v", clone)
	}
}

// The knobs must survive a save/load cycle, or /config edits would silently
// erase them.
func TestSandboxRoundTripsThroughFileConfig(t *testing.T) {
	home := t.TempDir()
	writeConfigFile(t, home, "sandbox_runtime: docker\nsandbox_image: alpine:3.20\nsandbox_network: none\n")
	cfg, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveFile(); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(home, "config", "evva-config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var fc FileConfig
	if err := yaml.Unmarshal(raw, &fc); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if fc.SandboxRuntime != "docker" || fc.SandboxImage != "alpine:3.20" || fc.SandboxNetwork != "none" {
		t.Errorf("round trip lost values: %+v", fc)
	}

	reloaded, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetSandboxRuntime() != "docker" || reloaded.GetSandboxNetwork() != "none" {
		t.Error("reload lost sandbox settings")
	}
}

// Off must not be written back as noise: omitempty keeps a config that never
// opted in free of sandbox keys.
func TestSandboxOffOmittedFromWrittenConfig(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(LoadOptions{AppName: "evva", AppHome: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveFile(); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config", "evva-config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sandbox_runtime") {
		t.Errorf("an off default should not be written out:\n%s", raw)
	}
}
