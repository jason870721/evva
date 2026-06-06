package swarm

import (
	"reflect"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
)

// The Roster is the bus's roster view for expanding "to: all" broadcasts. The
// interface lives in package bus so the bus never imports swarm; this asserts
// the structural satisfaction at compile time (SPRD-1-5).
var _ bus.Membership = (*Roster)(nil)

func TestRosterAddSnapshotAndDuplicate(t *testing.T) {
	r := newRoster()
	if err := r.add("leader", agentdef.RoleLeader, "leads", nil); err != nil {
		t.Fatal(err)
	}
	if err := r.add("worker", agentdef.RoleWorker, "works", nil); err != nil {
		t.Fatal(err)
	}
	if err := r.add("leader", agentdef.RoleLeader, "", nil); err == nil {
		t.Fatal("want a duplicate-member error")
	}

	if got := r.Names(); !reflect.DeepEqual(got, []string{"leader", "worker"}) {
		t.Fatalf("Names = %v, want insertion order [leader worker]", got)
	}

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snap))
	}
	// Every member starts active + idle.
	for _, mv := range snap {
		if mv.Membership != MembershipActive || mv.Run != RunIdle {
			t.Errorf("%s: %s/%s, want active/idle", mv.Name, mv.Membership, mv.Run)
		}
	}
	if snap[0].Name != "leader" || snap[0].Role != agentdef.RoleLeader || snap[0].WhenToUse != "leads" {
		t.Errorf("entry[0] = %+v", snap[0])
	}
}

func TestRosterStatusMutatorsReflectInSnapshot(t *testing.T) {
	r := newRoster()
	_ = r.add("w", agentdef.RoleWorker, "", nil)

	r.setRun("w", RunBusy)
	r.setMembership("w", MembershipFrozen)
	r.setCurrentTask("w", 42)

	mv := r.Snapshot()[0]
	if mv.Run != RunBusy || mv.Membership != MembershipFrozen || mv.CurrentTask != 42 {
		t.Fatalf("snapshot did not reflect mutators: %+v", mv)
	}

	// Unknown names are no-ops, not panics.
	r.setRun("ghost", RunBusy)
	r.setMembership("ghost", MembershipFrozen)
	r.setCurrentTask("ghost", 1)
}

func TestRosterActiveMembersExcludesFrozen(t *testing.T) {
	r := newRoster()
	_ = r.add("a", agentdef.RoleWorker, "", nil)
	_ = r.add("b", agentdef.RoleWorker, "", nil)
	_ = r.add("c", agentdef.RoleWorker, "", nil)

	r.setMembership("b", MembershipFrozen)

	if got := r.ActiveMembers(); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("ActiveMembers = %v, want insertion-ordered actives [a c]", got)
	}
}

func TestRosterControllerLookup(t *testing.T) {
	r := newRoster()
	_ = r.add("w", agentdef.RoleWorker, "", nil)
	if _, ok := r.Controller("w"); !ok {
		t.Error("Controller(w) should be present")
	}
	if _, ok := r.Controller("nope"); ok {
		t.Error("Controller(nope) should be absent")
	}
}
