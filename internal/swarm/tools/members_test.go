package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm"
)

// supSpace is realSpace plus a live supervisor — member_spawn/member_retire
// delegate to it. Cleanup order (LIFO): supervisor stops before the space
// shuts down, mirroring teardownSpace.
func supSpace(t *testing.T) *swarm.SwarmSpace {
	t.Helper()
	sp := realSpace(t)
	ctx, cancel := context.WithCancel(context.Background())
	sup := swarm.NewSupervisor(sp)
	sup.Start(ctx)
	t.Cleanup(func() {
		cancel()
		sup.Wait()
	})
	return sp
}

// TestMemberSpawnAndRetireTools: the leader fans out clones in one call,
// tasks route to them like any member, and retire distinguishes clones from
// manifest members.
func TestMemberSpawnAndRetireTools(t *testing.T) {
	sp := supSpace(t)

	res := exec(t, newMemberSpawn(leaderMC(sp)), `{"from":"worker-a","count":2}`)
	if res.IsError {
		t.Fatalf("member_spawn: %s", res.Content)
	}
	if !strings.Contains(res.Content, "worker-a-2, worker-a-3") {
		t.Fatalf("spawn result = %q, want both clone names", res.Content)
	}

	// Clones are real assignees: task_create's roster guard accepts them.
	res = exec(t, newTaskCreate(leaderMC(sp)), `{"title":"t","assignee":"worker-a-2"}`)
	if res.IsError {
		t.Fatalf("task_create for clone: %s", res.Content)
	}

	res = exec(t, newMemberRetire(leaderMC(sp)), `{"name":"worker-b"}`)
	if !res.IsError || !strings.Contains(res.Content, "not a spawned member") {
		t.Fatalf("retire manifest member = %+v, want refusal", res)
	}
	res = exec(t, newMemberRetire(leaderMC(sp)), `{"name":"worker-a-3"}`)
	if res.IsError {
		t.Fatalf("member_retire clone: %s", res.Content)
	}
}

// TestMemberSpawnValidation: bad input surfaces as correctable tool errors;
// a lite space (no supervisor) refuses gracefully.
func TestMemberSpawnValidation(t *testing.T) {
	sp := supSpace(t)

	res := exec(t, newMemberSpawn(leaderMC(sp)), `{}`)
	if !res.IsError || !strings.Contains(res.Content, "'from' is required") {
		t.Fatalf("missing from = %+v", res)
	}
	res = exec(t, newMemberSpawn(leaderMC(sp)), `{"from":"worker-a","count":9}`)
	if !res.IsError || !strings.Contains(res.Content, "per-call max") {
		t.Fatalf("over-count = %+v", res)
	}
	res = exec(t, newMemberSpawn(leaderMC(sp)), `{"from":"worker-a","retire":"sometimes"}`)
	if !res.IsError || !strings.Contains(res.Content, "retire") {
		t.Fatalf("bad retire = %+v", res)
	}

	lite := liteSpace(t, "a")
	res = exec(t, newMemberSpawn(leaderMC(lite)), `{"from":"a"}`)
	if !res.IsError || !strings.Contains(res.Content, "no supervisor") {
		t.Fatalf("lite spawn = %+v, want graceful refusal", res)
	}
}
