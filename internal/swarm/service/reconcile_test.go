package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
)

// writeSwarmFixture lays down a minimal on-disk swarm (manifest + a leader and a
// worker agent dir) so Service.Register can BuildAll + NewSpace from it.
func writeSwarmFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	must := func(p, content string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("evva-swarm.yml", "name: "+name+"\nleader:\n  agent: leader\nworkers:\n  - agent: worker\nsettings:\n  permission_mode: bypass\n  max_iterations: 3\n")
	must("agents/main/leader/system_prompt.md", "You are the leader.")
	must("agents/sub/worker/system_prompt.md", "You are a worker.")
	return dir
}

// stubLoadConfigInto returns a Service loadConfig that resolves a config rooted
// at a shared AppHome (so the persona registry + session store are consistent
// across restarts) with the fake LLM provider wired in.
func stubLoadConfigInto(appHome string) func(string) (*config.Config, error) {
	return func(workdir string) (*config.Config, error) {
		cfg, err := config.Load(config.LoadOptions{AppName: "svctest", AppHome: appHome, WorkDir: workdir})
		if err != nil {
			return nil, err
		}
		cfg.LLMProviderConfig[stubProvider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x", Models: []constant.Model{stubModel}}
		cfg.DefaultProvider = constant.LLMProvider{Name: stubProvider, Models: []constant.Model{stubModel}}
		cfg.DefaultModel = constant.Model(stubModel)
		return cfg, nil
	}
}

// AC#5 + boot reconcile: two on-disk spaces are registered (and recorded in
// spaces.json); a fresh Service over the same state dir rebuilds both,
// isolated, on Reconcile.
func TestReconcileRebuildsRegisteredSpaces(t *testing.T) {
	stateDir := t.TempDir()
	appHome := t.TempDir()
	loadCfg := stubLoadConfigInto(appHome)

	dirA := writeSwarmFixture(t, "team-a")
	dirB := writeSwarmFixture(t, "team-b")

	// --- first process ---
	svc1 := New("127.0.0.1:0")
	svc1.SetStateDir(stateDir)
	svc1.loadConfig = loadCfg
	if _, err := svc1.Register(dirA, ""); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if _, err := svc1.Register(dirB, ""); err != nil {
		t.Fatalf("register B: %v", err)
	}
	if got := len(svc1.ListSpaces()); got != 2 {
		t.Fatalf("svc1 has %d spaces, want 2", got)
	}
	// spaces.json was written.
	if _, err := os.Stat(filepath.Join(stateDir, spacesFileName)); err != nil {
		t.Fatalf("spaces.json not persisted: %v", err)
	}
	svc1.Stop() // process dies

	// --- restart ---
	svc2 := New("127.0.0.1:0")
	svc2.SetStateDir(stateDir)
	svc2.loadConfig = loadCfg
	defer svc2.Stop()

	if err := svc2.Reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	spaces := svc2.ListSpaces()
	if len(spaces) != 2 {
		t.Fatalf("after reconcile: %d spaces, want 2", len(spaces))
	}

	// Both rebuilt spaces are isolated and each has its own leader.
	ids := map[string]bool{}
	for _, sp := range spaces {
		ids[sp.ID] = true
		if _, ok := svc2.controller(sp.ID, "leader"); !ok {
			t.Errorf("space %s missing its leader after reconcile", sp.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("reconciled spaces share an id: %v", ids)
	}

	// Stopping one KEEPS it (as stopped) so it stays listed and can be restarted;
	// only removing it drops it from spaces.json so a further restart won't revive.
	first := spaces[0].ID
	if err := svc2.StopSpace(first); err != nil {
		t.Fatalf("stop space: %v", err)
	}
	if len(svc2.ListSpaces()) != 2 {
		t.Fatalf("after stop: %d spaces, want 2 (a stopped space is still listed)", len(svc2.ListSpaces()))
	}
	if svc2.HasSpace(first) {
		t.Fatal("a stopped space must not report as serving (HasSpace)")
	}
	// Restarting it brings it back up under the same id.
	if _, err := svc2.RunSpace(first); err != nil {
		t.Fatalf("run space: %v", err)
	}
	if !svc2.HasSpace(first) {
		t.Fatal("space did not come back up after run")
	}
	// Removing it drops it for good.
	if err := svc2.RemoveSpace(first); err != nil {
		t.Fatalf("remove space: %v", err)
	}
	if len(svc2.ListSpaces()) != 1 {
		t.Fatalf("after rm: %d spaces, want 1", len(svc2.ListSpaces()))
	}
}

// Reconcile with no state dir / no file is a clean no-op.
func TestReconcileNoState(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	if err := svc.Reconcile(); err != nil {
		t.Fatalf("reconcile without state dir should be a no-op, got %v", err)
	}

	svc.SetStateDir(t.TempDir()) // empty dir, no spaces.json yet
	if err := svc.Reconcile(); err != nil {
		t.Fatalf("reconcile with absent spaces.json should be a no-op, got %v", err)
	}
}
