package service

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExampleSwarmConstructs guards the shipped example in
// docs/veronica/example-swarm/: it must actually parse + construct through the
// real Register path (manifest, agent dirs, and every tool name in active.yml
// resolving), so "copy it and run" really works. Copied to a temp dir first so
// the test never writes a .vero/ into the repo. Uses the stub LLM provider, so
// no network — it validates wiring, not model behaviour.
func TestExampleSwarmConstructs(t *testing.T) {
	src := filepath.Join("..", "..", "..", "docs", "veronica", "example-swarm")
	if _, err := os.Stat(filepath.Join(src, "evva-swarm.yml")); err != nil {
		t.Fatalf("example swarm not found at %s: %v", src, err)
	}
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy example into temp: %v", err)
	}

	svc := New("127.0.0.1:0")
	svc.loadConfig = scriptedLoadConfig(t.TempDir())
	defer svc.Stop()

	id, err := svc.Register(dst)
	if err != nil {
		t.Fatalf("register example swarm: %v", err)
	}

	roster, ok := svc.Roster(id)
	if !ok {
		t.Fatal("no roster for the registered example")
	}
	if len(roster) != 3 {
		t.Fatalf("example roster has %d members, want 3", len(roster))
	}
	names := map[string]string{} // name -> role
	for _, m := range roster {
		names[m.Name] = m.Role
	}
	for _, want := range []string{"lead", "builder", "reviewer"} {
		if _, ok := names[want]; !ok {
			t.Errorf("example missing member %q (roster: %v)", want, names)
		}
	}
	if names["lead"] != "leader" {
		t.Errorf("lead role = %q, want leader", names["lead"])
	}
	if names["builder"] != "worker" || names["reviewer"] != "worker" {
		t.Errorf("workers misrostered: builder=%q reviewer=%q", names["builder"], names["reviewer"])
	}
}

// TestVeroTechSwarmConstructs guards docs/veronica/vero-tech-swarm/ the same way:
// the shipped 7-member engineering team must parse + construct through the real
// Register path — manifest, all seven agent dirs, and every tool name in every
// active.yml resolving — so "copy it and run" really works.
func TestVeroTechSwarmConstructs(t *testing.T) {
	src := filepath.Join("..", "..", "..", "docs", "veronica", "vero-tech-swarm")
	if _, err := os.Stat(filepath.Join(src, "evva-swarm.yml")); err != nil {
		t.Fatalf("vero-tech-swarm not found at %s: %v", src, err)
	}
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy vero-tech-swarm into temp: %v", err)
	}

	svc := New("127.0.0.1:0")
	svc.loadConfig = scriptedLoadConfig(t.TempDir())
	defer svc.Stop()

	id, err := svc.Register(dst)
	if err != nil {
		t.Fatalf("register vero-tech-swarm: %v", err)
	}

	roster, ok := svc.Roster(id)
	if !ok {
		t.Fatal("no roster for the registered vero-tech-swarm")
	}
	if len(roster) != 7 {
		t.Fatalf("vero-tech-swarm roster has %d members, want 7", len(roster))
	}
	role := map[string]string{} // name -> role
	for _, m := range roster {
		role[m.Name] = m.Role
	}
	if role["lead"] != "leader" {
		t.Errorf("lead role = %q, want leader (roster: %v)", role["lead"], role)
	}
	for _, w := range []string{"pm", "designer", "backend-a", "backend-b", "frontend", "qa"} {
		if role[w] != "worker" {
			t.Errorf("member %q role = %q, want worker (roster: %v)", w, role[w], role)
		}
	}
}
