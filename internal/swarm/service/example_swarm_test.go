package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// TestExampleSwarmConstructs guards the shipped example in
// examples/evva-swarm/starter/: it must actually parse + construct through the
// real Register path (manifest, agent dirs, and every tool name in active.yml
// resolving), so "copy it and run" really works. Copied to a temp dir first so
// the test never writes a .vero/ into the repo. Uses the stub LLM provider, so
// no network — it validates wiring, not model behaviour.
func TestExampleSwarmConstructs(t *testing.T) {
	src := filepath.Join("..", "..", "..", "examples", "evva-swarm", "starter")
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
	// Every member carries its effective permission stance on the wire (RP-24);
	// agent.New's fallback chain guarantees it is never empty on a real space.
	for _, m := range roster {
		if m.PermissionMode == "" {
			t.Errorf("member %q has no effective permission mode on the wire", m.Name)
		}
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

// TestVeroTechSwarmConstructs guards examples/evva-swarm/tech-team/ the same way:
// the shipped 7-member engineering team must parse + construct through the real
// Register path — manifest, all seven agent dirs, and every tool name in every
// active.yml resolving — so "copy it and run" really works.
func TestVeroTechSwarmConstructs(t *testing.T) {
	src := filepath.Join("..", "..", "..", "examples", "evva-swarm", "tech-team")
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
	src := filepath.Join("..", "..", "..", "examples", "evva-swarm", "starter")
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

// TestReloadSpace re-reads the manifest + agent dirs and rebuilds a space under the
// SAME id WITHOUT wiping its ledger — the web "re-apply config" path. It proves both
// halves of the contract: a manifest edit (a new per-member permission_mode) takes
// effect on the rebuilt roster, AND a seeded task survives (reload, unlike reset,
// keeps the .vero ledger + transcripts).
func TestReloadSpace(t *testing.T) {
	src := filepath.Join("..", "..", "..", "examples", "evva-swarm", "starter")
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

	modeOf := func(name string) string {
		roster, _ := svc.Roster(id)
		for _, m := range roster {
			if m.Name == name {
				return m.PermissionMode
			}
		}
		return ""
	}
	// Starter manifest sets permission_mode: bypass at the settings level, so every
	// member inherits it before the edit.
	if got := modeOf("builder"); got != "bypass" {
		t.Fatalf("pre-reload builder mode = %q, want bypass", got)
	}

	// Seed a task so there's ledger state that reload must PRESERVE (unlike reset).
	ent, ok := svc.entry(id)
	if !ok {
		t.Fatal("no entry after register")
	}
	if _, err := ent.space.Store.CreateTask(store.Task{Title: "seed", Assignee: "builder", CreatedBy: "lead"}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Edit the on-disk manifest: pin the builder worker to plan mode. Reload must
	// re-read this and apply it to the rebuilt member.
	mf := filepath.Join(dst, "evva-swarm.yml")
	raw, err := os.ReadFile(mf)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	// Normalize CRLF→LF first: Git's autocrlf checks the example out with CRLF
	// on Windows, which would defeat the LF-anchored Replace below.
	manifest := strings.ReplaceAll(string(raw), "\r\n", "\n")
	edited := strings.Replace(manifest, "  - agent: builder\n", "  - agent: builder\n    permission_mode: plan\n", 1)
	if edited == manifest {
		t.Fatal("manifest edit did not match — starter layout changed?")
	}
	if err := os.WriteFile(mf, []byte(edited), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	newID, err := svc.ReloadSpace(id)
	if err != nil {
		t.Fatalf("ReloadSpace: %v", err)
	}
	if newID != id {
		t.Errorf("reload changed the space id: %q -> %q", id, newID)
	}
	if !svc.HasSpace(id) {
		t.Error("space gone after reload; it should be rebuilt under the same id")
	}

	// Manifest re-read took effect: builder is now plan, reviewer still inherits bypass.
	if got := modeOf("builder"); got != "plan" {
		t.Errorf("post-reload builder mode = %q, want plan (manifest not re-read?)", got)
	}
	if got := modeOf("reviewer"); got != "bypass" {
		t.Errorf("post-reload reviewer mode = %q, want bypass", got)
	}

	// Ledger preserved: the seeded task survives (the reload-vs-reset difference).
	page, ok := svc.Tasks(id)
	if !ok {
		t.Fatal("no tasks view after reload (store not reopened?)")
	}
	if len(page.Tasks) != 1 {
		t.Errorf("post-reload tasks = %d, want 1 (ledger should be preserved)", len(page.Tasks))
	}

	if _, err := svc.ReloadSpace("nope"); err == nil {
		t.Error("reload of an unknown space should error")
	}
}
