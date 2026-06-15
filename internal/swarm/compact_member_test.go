package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// TestCompactMember_IdleMember: compacting an idle member reaches the
// controller's Compact with the chosen kind. An unknown member and an unknown
// kind both error with the keyword the web layer maps (404 / 400 respectively).
func TestCompactMember_IdleMember(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	sup := startSup(t, sp)

	if err := sup.CompactMember("w", "micro"); err != nil {
		t.Fatalf("CompactMember micro: %v", err)
	}
	if got := ctls["w"].compacts.Load(); got != 1 {
		t.Errorf("controller Compact calls: got %d, want 1", got)
	}
	if got := ctls["w"].lastKind(); got != "micro" {
		t.Errorf("compact kind: got %q, want micro", got)
	}

	if err := sup.CompactMember("w", "full"); err != nil {
		t.Fatalf("CompactMember full: %v", err)
	}
	if got := ctls["w"].compacts.Load(); got != 2 {
		t.Errorf("controller Compact calls after full: got %d, want 2", got)
	}

	// Unknown member → an 'unknown'-keyword error (web maps to 404).
	if err := sup.CompactMember("ghost", "full"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown member: got %v, want an 'unknown' error", err)
	}

	// Unknown kind → a 'kind'-keyword error (web maps to 400), propagated up
	// from the agent's own validation.
	if err := sup.CompactMember("w", "bogus"); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Errorf("unknown kind: got %v, want a 'kind' error", err)
	}
}

// TestCompactMember_BusyMemberRefuses: a member with a run in flight is refused
// with a "busy" error (the web maps it to 409) and its controller's Compact is
// never reached — mirrors the ClearMemberSession busy guard.
func TestCompactMember_BusyMemberRefuses(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	ctls["w"].block = true
	sup := startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "boss", Recipient: "w", Body: "long task"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "w is running", func() bool { return ctls["w"].inFlight.Load() == 1 })

	err := sup.CompactMember("w", "full")
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy compact: got %v, want a 'busy' error", err)
	}
	if got := ctls["w"].compacts.Load(); got != 0 {
		t.Errorf("Compact reached on a busy member: %d calls", got)
	}
}
