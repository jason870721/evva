package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// TestExampleSwarmConstructs guards the shipped example in
// docs/roadmap/veronica/example-swarm/: it must actually parse + construct through the
// real Register path (manifest, agent dirs, and every tool name in active.yml
// resolving), so "copy it and run" really works. Copied to a temp dir first so
// the test never writes a .vero/ into the repo. Uses the stub LLM provider, so
// no network — it validates wiring, not model behaviour.
func TestExampleSwarmConstructs(t *testing.T) {
	src := filepath.Join("..", "..", "..", "docs", "roadmap", "veronica", "example-swarm")
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

	id, err := svc.Register(dst, "")
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

// TestVeroTechSwarmConstructs guards docs/roadmap/veronica/vero-tech-swarm/ the same way:
// the shipped 7-member engineering team must parse + construct through the real
// Register path — manifest, all seven agent dirs, and every tool name in every
// active.yml resolving — so "copy it and run" really works.
func TestVeroTechSwarmConstructs(t *testing.T) {
	src := filepath.Join("..", "..", "..", "docs", "roadmap", "veronica", "vero-tech-swarm")
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

	id, err := svc.Register(dst, "")
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

// TestResetSpace wipes a space's ledger + agent context and rebuilds it under the
// SAME id (so the operator's URL keeps working). Uses the on-disk example swarm so
// the reset's manifest re-read has something real to load.
func TestResetSpace(t *testing.T) {
	src := filepath.Join("..", "..", "..", "docs", "roadmap", "veronica", "example-swarm")
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy example: %v", err)
	}
	svc := New("127.0.0.1:0")
	svc.loadConfig = scriptedLoadConfig(t.TempDir())
	defer svc.Stop()

	id, err := svc.Register(dst, "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Seed a task so there's ledger state to wipe.
	ent, ok := svc.entry(id)
	if !ok {
		t.Fatal("no entry after register")
	}
	if _, err := ent.space.Store.CreateTask(store.Task{Title: "seed", Assignee: "builder", CreatedBy: "lead"}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if page, _ := svc.Tasks(id); len(page.Tasks) != 1 {
		t.Fatalf("pre-reset tasks = %d, want 1", len(page.Tasks))
	}

	newID, err := svc.ResetSpace(id)
	if err != nil {
		t.Fatalf("ResetSpace: %v", err)
	}
	if newID != id {
		t.Errorf("reset changed the space id: %q -> %q", id, newID)
	}
	if !svc.HasSpace(id) {
		t.Error("space gone after reset; it should be rebuilt under the same id")
	}
	page, ok := svc.Tasks(id)
	if !ok {
		t.Fatal("no tasks view after reset (store not reopened?)")
	}
	if len(page.Tasks) != 0 {
		t.Errorf("post-reset tasks = %d, want 0 (ledger should be wiped)", len(page.Tasks))
	}

	if _, err := svc.ResetSpace("nope"); err == nil {
		t.Error("reset of an unknown space should error")
	}
}
