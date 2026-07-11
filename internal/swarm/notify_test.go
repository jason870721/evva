package swarm

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

// TestNotifyOpsEmitsOpsAlertEvent (NTF-1): every notifyOps notice produces
// its durable mail exactly as before PLUS one ops_alert space event carrying
// "subject\nbody" with the about-member as AgentID.
func TestNotifyOpsEmitsOpsAlertEvent(t *testing.T) {
	sp := liteGraphSpace(t, "w")
	r := newRoster()
	if err := r.add("lead", agentdef.RoleLeader, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := r.add("w", agentdef.RoleWorker, "", "", nil); err != nil {
		t.Fatal(err)
	}
	sp.Roster = r
	sp.out = make(chan SpacedEvent, 4)
	sup := NewSupervisor(sp)

	sup.notifyOps("w", "⚠️ budget breaker: w frozen", "spent 2M tokens today")

	if n := len(mailsFor(t, sp, "lead")); n != 1 {
		t.Fatalf("leader mails = %d, want 1 (unchanged)", n)
	}
	if n := len(mailsFor(t, sp, "user")); n != 1 {
		t.Fatalf("operator mails = %d, want 1 (unchanged)", n)
	}
	select {
	case e := <-sp.out:
		if e.Event.Kind != KindOpsAlert || e.Event.AgentID != "w" {
			t.Fatalf("event = %s about %q, want ops_alert about w", e.Event.Kind, e.Event.AgentID)
		}
		if got := e.Event.Text.Text; !strings.HasPrefix(got, "⚠️ budget breaker: w frozen\n") || !strings.Contains(got, "spent 2M tokens") {
			t.Fatalf("event text = %q, want subject\\nbody", got)
		}
	default:
		t.Fatal("no ops_alert event emitted alongside the mail")
	}

	// An alert about the leader itself: no self-mail (the pre-NTF rule), the
	// event still fires.
	sup.notifyOps("lead", "⏳ stall: lead busy", "details")
	if n := len(mailsFor(t, sp, "lead")); n != 1 {
		t.Fatalf("leader self-alert mailed the leader: %d mails", n)
	}
	select {
	case e := <-sp.out:
		if e.Event.Kind != KindOpsAlert || e.Event.AgentID != "lead" {
			t.Fatalf("event = %s about %q, want ops_alert about lead", e.Event.Kind, e.Event.AgentID)
		}
	default:
		t.Fatal("no ops_alert for the leader-about alert")
	}
}
