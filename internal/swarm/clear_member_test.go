package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/llm"
)

// TestClearMemberSession_IdleMember: clearing an idle member reaches the
// controller's ClearSession, deletes ONLY that member's persisted snapshots,
// and zeroes the roster's cached token snapshot while keeping today's
// budget-meter spend.
func TestClearMemberSession_IdleMember(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{
		"w": agentdef.RoleWorker, "peer": agentdef.RoleWorker,
	})
	appHome := t.TempDir()
	sp.cfg = &config.Config{AppHome: appHome}
	sup := startSup(t, sp)

	// Simulate a metered prior life: persisted snapshots for w and a teammate,
	// a roster token snapshot, and today's budget spend.
	slug := memdir.ProjectKey(sp.Workdir)
	for id, persona := range map[string]string{"s-w": "w", "s-peer": "peer"} {
		err := session.Save(appHome, &session.Snapshot{
			Version: session.SnapshotVersion, SessionID: id,
			Workdir: sp.Workdir, WorkdirSlug: slug, Profile: persona,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	today := localDay(time.Now())
	sp.addDailyUsage("w", 500, today)
	sp.Roster.setUsage("w", llm.Usage{InputTokens: 100, OutputTokens: 40}, 100, 500)

	if err := sup.ClearMemberSession("w"); err != nil {
		t.Fatalf("ClearMemberSession: %v", err)
	}
	if got := ctls["w"].clears.Load(); got != 1 {
		t.Errorf("controller ClearSession calls: got %d, want 1", got)
	}

	// Only w's snapshot is gone; the teammate's survives.
	entries, _, err := session.List(appHome, slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Snapshot.Profile != "peer" {
		t.Errorf("snapshots after clear: want only peer's, got %d entries", len(entries))
	}

	// Roster cache zeroed, daily budget spend preserved.
	for _, mv := range sp.Roster.Snapshot() {
		if mv.Name != "w" {
			continue
		}
		if mv.Usage.InputTokens != 0 || mv.Usage.OutputTokens != 0 || mv.LastTurnInput != 0 {
			t.Errorf("roster usage after clear: got %+v / %d, want zero", mv.Usage, mv.LastTurnInput)
		}
		if mv.DailyTokens != 500 {
			t.Errorf("daily spend after clear: got %d, want 500 (tokens burned still count)", mv.DailyTokens)
		}
	}

	// Unknown member errors with the 404-mapped keyword.
	if err := sup.ClearMemberSession("ghost"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown member: got %v, want an 'unknown' error", err)
	}
}

// TestClearMemberSession_BusyMemberRefuses: a member with a run in flight is
// refused with a "busy" error (the web maps it to 409) and its controller's
// ClearSession is never reached.
func TestClearMemberSession_BusyMemberRefuses(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	ctls["w"].block = true
	sup := startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "boss", Recipient: "w", Body: "long task"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "w is running", func() bool { return ctls["w"].inFlight.Load() == 1 })

	err := sup.ClearMemberSession("w")
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy clear: got %v, want a 'busy' error", err)
	}
	if got := ctls["w"].clears.Load(); got != 0 {
		t.Errorf("ClearSession reached on a busy member: %d calls", got)
	}
}
