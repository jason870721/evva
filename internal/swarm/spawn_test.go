package swarm

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

func spawnLeader() store.Actor { return store.Actor{Name: "leader", Role: store.RoleLeader} }

// TestSpawnMemberLifecycle: spawn derives the name, puts a full member on the
// roster (mailbox, memory dir, provenance), refuses the bad bases, and a
// manual retire tears it down through RemoveMember (provenance dropped,
// leader safe).
func TestSpawnMemberLifecycle(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("sp1", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sup := startSup(t, sp)

	name, err := sup.SpawnMember("worker-a", "")
	if err != nil {
		t.Fatalf("SpawnMember: %v", err)
	}
	if name != "worker-a-2" {
		t.Fatalf("clone name = %q, want worker-a-2", name)
	}
	if _, ok := sp.Roster.roleOf(name); !ok {
		t.Fatal("clone not on roster")
	}
	meta, ok := sp.SpawnedInfo(name)
	if !ok || meta.From != "worker-a" || meta.Retire != RetireOnComplete || meta.Seq != 2 {
		t.Fatalf("SpawnedInfo = %+v, %v", meta, ok)
	}
	// The clone's registered def is grounded in ITS OWN name, not the base's.
	def, ok := sp.reg.Get(name)
	if !ok {
		t.Fatal("clone def missing from space registry")
	}
	if !strings.Contains(def.SystemPrompt, "worker-a-2") {
		t.Fatal("clone protocol grounding still carries the base name")
	}

	// Bad bases.
	if _, err := sup.SpawnMember(name, ""); err == nil || !strings.Contains(err.Error(), "clone of") {
		t.Fatalf("spawn-from-clone: err = %v, want refusal", err)
	}
	if _, err := sup.SpawnMember("leader", ""); err == nil || !strings.Contains(err.Error(), "leader") {
		t.Fatalf("spawn leader: err = %v, want refusal", err)
	}
	if _, err := sup.SpawnMember("ghost", ""); err == nil {
		t.Fatal("spawn unknown base should error")
	}
	if _, err := sup.SpawnMember("worker-a", "sometimes"); err == nil {
		t.Fatal("bad retire policy should error")
	}

	// Manual retire: spawned-only, then gone (roster + provenance).
	if err := sup.RetireSpawned("worker-b"); err == nil {
		t.Fatal("retiring a manifest member must be refused")
	}
	if err := sup.RetireSpawned(name); err != nil {
		t.Fatalf("RetireSpawned: %v", err)
	}
	if _, ok := sp.Roster.roleOf(name); ok {
		t.Fatal("clone still on roster after retire")
	}
	if _, ok := sp.SpawnedInfo(name); ok {
		t.Fatal("provenance survived retire")
	}

	// Names are never reused: the next spawn from the same base moves on.
	name2, err := sup.SpawnMember("worker-a", RetireManual)
	if err != nil {
		t.Fatalf("respawn: %v", err)
	}
	if name2 != "worker-a-3" {
		t.Fatalf("respawn name = %q, want worker-a-3 (no reuse)", name2)
	}
}

// TestSpawnMaxMembers: the cap gates spawning at the LIVE roster size, and
// names the knob.
func TestSpawnMaxMembers(t *testing.T) {
	cfg := stubConfig(t)
	m := testManifest()
	m.Settings.MaxMembers = 4
	sp, err := NewSpace("sp2", m, testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sup := startSup(t, sp)

	if _, err := sup.SpawnMember("worker-a", ""); err != nil {
		t.Fatalf("spawn within cap: %v", err)
	}
	_, err = sup.SpawnMember("worker-a", "")
	if err == nil || !strings.Contains(err.Error(), "max_members") {
		t.Fatalf("over-cap spawn: err = %v, want max_members refusal", err)
	}
}

// TestSweepSpawnedRetire: an on_complete clone retires only once it has
// completed work and holds nothing incomplete — a fresh clone and one with an
// open task both survive the sweep.
func TestSweepSpawnedRetire(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("sp3", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sup := startSup(t, sp)

	name, err := sup.SpawnMember("worker-a", "")
	if err != nil {
		t.Fatalf("SpawnMember: %v", err)
	}

	// Fresh clone, no tasks: never retired (completed == 0).
	sup.sweepSpawnedRetire()
	if _, ok := sp.Roster.roleOf(name); !ok {
		t.Fatal("fresh clone retired by sweep")
	}

	// Open task: still not retired.
	id, err := sp.Store.CreateTask(store.Task{Title: "t", Spec: "s", Assignee: name, CreatedBy: "leader"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := sp.Store.TransitionTask(id, store.StatusRunning, spawnLeader(), ""); err != nil {
		t.Fatal(err)
	}
	sup.sweepSpawnedRetire()
	if _, ok := sp.Roster.roleOf(name); !ok {
		t.Fatal("clone with an open task retired by sweep")
	}

	// Work settled: the sweep retires it (idle, no unread, nothing open).
	if err := sp.Store.CompleteWork(id, store.Actor{Name: name, Role: store.RoleWorker}, "done"); err != nil {
		t.Fatal(err)
	}
	if err := sp.Store.TransitionTask(id, store.StatusCompleted, spawnLeader(), "lgtm"); err != nil {
		t.Fatal(err)
	}
	sup.sweepSpawnedRetire()
	if _, ok := sp.Roster.roleOf(name); ok {
		t.Fatal("settled on_complete clone not retired by sweep")
	}
	if _, ok := sp.SpawnedInfo(name); ok {
		t.Fatal("provenance survived the sweep retire")
	}

	// A manual-policy clone never auto-retires.
	manual, err := sup.SpawnMember("worker-b", RetireManual)
	if err != nil {
		t.Fatalf("spawn manual: %v", err)
	}
	sup.sweepSpawnedRetire()
	if _, ok := sp.Roster.roleOf(manual); !ok {
		t.Fatal("manual clone retired by sweep")
	}
}

// TestSpawnedRestartResume: a rebuild re-clones spawned members from
// runtime.json before the resume loop, a fresh register discards them, and
// the per-base sequence survives so names are never reused across lives.
func TestSpawnedRestartResume(t *testing.T) {
	cfg := stubConfig(t)

	// Life 1: spawn, then die.
	sp1, err := NewSpace("sr", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace 1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sup1 := NewSupervisor(sp1)
	sup1.Start(ctx)
	name, err := sup1.SpawnMember("worker-a", "")
	if err != nil {
		t.Fatalf("SpawnMember: %v", err)
	}
	// Mail the clone so the rebuilt life has something to requeue.
	if _, err := sp1.Bus.Send(store.Message{Sender: "leader", Recipient: name, Body: "hello clone"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	sup1.Wait()
	sp1.Shutdown()

	// Life 2 (rebuild): Reload re-clones it with provenance + unread intact.
	sp2, err := NewSpace("sr", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace 2: %v", err)
	}
	sp2.Reload()
	if _, ok := sp2.Roster.roleOf(name); !ok {
		t.Fatal("clone not restored on rebuild")
	}
	meta, ok := sp2.SpawnedInfo(name)
	if !ok || meta.From != "worker-a" || meta.Seq != 2 {
		t.Fatalf("restored SpawnedInfo = %+v, %v", meta, ok)
	}
	if ids, err := sp2.Store.UnreadFor(name); err != nil || len(ids) != 1 {
		t.Fatalf("clone unread after rebuild = %v, %v; want the 1 durable row", ids, err)
	}
	// Sequence continues across lives.
	sup2 := startSup(t, sp2)
	name2, err := sup2.SpawnMember("worker-a", "")
	if err != nil {
		t.Fatalf("respawn on life 2: %v", err)
	}
	if name2 != "worker-a-3" {
		t.Fatalf("life-2 spawn = %q, want worker-a-3", name2)
	}
	sp2.Shutdown()

	// Life 3 (fresh register): discard drops the records; nothing re-clones.
	sp3, err := NewSpace("sr", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace 3: %v", err)
	}
	defer sp3.Shutdown()
	sp3.DiscardRuntimeSpawned()
	sp3.Reload()
	if _, ok := sp3.Roster.roleOf(name); ok {
		t.Fatal("fresh register must not restore clones")
	}
}

// TestSpawnedRestoreMissingBase: a clone whose base vanished is dropped with
// one durable leader mail; the rebuild survives.
func TestSpawnedRestoreMissingBase(t *testing.T) {
	cfg := stubConfig(t)
	sp1, err := NewSpace("mb", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace 1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sup1 := NewSupervisor(sp1)
	sup1.Start(ctx)
	if _, err := sup1.SpawnMember("worker-b", ""); err != nil {
		t.Fatalf("SpawnMember: %v", err)
	}
	cancel()
	sup1.Wait()
	sp1.Shutdown()

	// Rebuild WITHOUT worker-b in the manifest.
	loaded := testLoaded()[:2] // leader + worker-a only
	sp2, err := NewSpace("mb", testManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace 2: %v", err)
	}
	defer sp2.Shutdown()
	sp2.Reload()
	if _, ok := sp2.Roster.roleOf("worker-b-2"); ok {
		t.Fatal("clone of a vanished base must not restore")
	}
	msgs, err := sp2.Store.ListMessages(0)
	if err != nil {
		t.Fatal(err)
	}
	warned := 0
	for _, m := range msgs {
		if m.Recipient == "leader" && strings.Contains(m.Subject, "not restored") {
			warned++
		}
	}
	if warned != 1 {
		t.Fatalf("leader warnings = %d, want exactly 1", warned)
	}
}

// TestPersonaClone: cloning a persona member composes from the base persona
// under the clone's own name (PromptSuffix re-derived, registry entry under
// the clone name).
func TestPersonaClone(t *testing.T) {
	cfg := stubConfig(t)
	ld := personaLoaded("evva", agentdef.RoleWorker)
	sp, err := NewSpace("pc", testManifest(), []agentdef.Loaded{dirLoaded("leader", agentdef.RoleLeader), ld}, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sup := startSup(t, sp)

	name, err := sup.SpawnMember("evva", "")
	if err != nil {
		t.Fatalf("spawn persona clone: %v", err)
	}
	if name != "evva-2" {
		t.Fatalf("clone name = %q", name)
	}
	def, ok := sp.reg.Get(name)
	if !ok {
		t.Fatal("clone def missing from registry")
	}
	if !def.LongRunning || !strings.Contains(def.PromptSuffix, "evva-2") {
		t.Fatalf("clone def not composed for its own name: LongRunning=%v suffix has name=%v",
			def.LongRunning, strings.Contains(def.PromptSuffix, "evva-2"))
	}
	if !sp.isPersonaMember(name) {
		t.Fatal("persona clone must be tracked as a persona member")
	}
}
