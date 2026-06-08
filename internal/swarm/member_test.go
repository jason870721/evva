package swarm

import (
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/tools"
)

// TestRosterRemoveAndRoleOf covers the roster primitives RP-8 adds: roleOf reads
// a member's role (for the leader guard), and remove forgets a member entirely
// (entry + insertion-order slot), unlike freeze which keeps the seat.
func TestRosterRemoveAndRoleOf(t *testing.T) {
	r := newRoster()
	_ = r.add("lead", agentdef.RoleLeader, "", nil)
	_ = r.add("w", agentdef.RoleWorker, "", nil)

	if role, ok := r.roleOf("lead"); !ok || role != agentdef.RoleLeader {
		t.Errorf("roleOf(lead) = %q,%v want leader,true", role, ok)
	}
	r.remove("w")
	if _, ok := r.roleOf("w"); ok {
		t.Error("w still present after remove")
	}
	if names := r.Names(); len(names) != 1 || names[0] != "lead" {
		t.Errorf("order after remove = %v, want [lead]", names)
	}
	r.remove("nope") // unknown → no-op, no panic
}

// TestSupervisorCreateAndRemoveMember: the full live path — CreateMember authors a
// worker (dir written + roster live), duplicates and the leader are protected, and
// RemoveMember retires a worker (gone from roster) while keeping its dir (the
// supervisor never deletes; deleteDir is the service's concern).
func TestSupervisorCreateAndRemoveMember(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("m", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sup := startSup(t, sp)

	spec := agentdef.MemberSpec{Name: "qa", SystemPrompt: "You are QA.", WhenToUse: "testing", Active: []tools.ToolName{"read"}}
	if err := sup.CreateMember(spec); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if _, ok := sp.Roster.roleOf("qa"); !ok {
		t.Error("qa not in roster after create")
	}
	if !agentdef.MemberDirExists(cfg.WorkDir, "qa") {
		t.Error("qa dir not written")
	}
	if err := sup.CreateMember(spec); err == nil {
		t.Error("duplicate CreateMember should error")
	}

	if err := sup.RemoveMember("leader"); err == nil {
		t.Error("removing the leader should error")
	}
	if err := sup.RemoveMember("qa"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, ok := sp.Roster.roleOf("qa"); ok {
		t.Error("qa still in roster after remove")
	}
	if !agentdef.MemberDirExists(cfg.WorkDir, "qa") {
		t.Error("supervisor remove must keep the dir (deleteDir is the service's job)")
	}
}

// TestCreateMemberMountsExistingDir: a name-only spec whose dir already exists
// mounts it (the `evva swarm add-member` CLI path), rather than erroring.
func TestCreateMemberMountsExistingDir(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("m2", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sup := startSup(t, sp)

	if err := agentdef.WriteMemberDir(cfg.WorkDir, agentdef.MemberSpec{Name: "ops", SystemPrompt: "You are ops."}); err != nil {
		t.Fatalf("pre-write dir: %v", err)
	}
	if err := sup.CreateMember(agentdef.MemberSpec{Name: "ops"}); err != nil {
		t.Fatalf("mount existing dir: %v", err)
	}
	if _, ok := sp.Roster.roleOf("ops"); !ok {
		t.Error("ops not mounted into the roster")
	}
}
