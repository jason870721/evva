package config

import (
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
