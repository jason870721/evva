package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// opsMailCount counts "system" notices to one recipient whose subject contains substr.
func opsMailCount(t *testing.T, st *store.Store, recipient, substr string) int {
	t.Helper()
	msgs, err := st.ListMessages(0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Sender == "system" && m.Recipient == recipient && strings.Contains(m.Subject, substr) {
			n++
		}
	}
	return n
}

// RP-14 AC#1: a run busy past the threshold raises exactly ONE stall alert
// (operator + leader), and alert-only mode never touches the run.
func TestStallAlertOncePerRun(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.StallThreshold = 20 * time.Millisecond
	ctls["w"].block = true
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "hang"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "stall alert delivered to user and leader", func() bool {
		return opsMailCount(t, sp.Store, "user", "stall: w") == 1 &&
			opsMailCount(t, sp.Store, "lead", "stall: w") == 1
	})

	time.Sleep(60 * time.Millisecond) // many more ticks past the threshold
	if got := opsMailCount(t, sp.Store, "user", "stall: w"); got != 1 {
		t.Errorf("stall alert repeated within one run: %d notices", got)
	}
	if got := runStatusOf(sp, "w"); got != RunBusy {
		t.Errorf("alert-only watchdog changed the run: status %s, want busy", got)
	}
}

// RP-14 AC#2: a member blocked on a human (waiting-approval) is exempt — the
// alert fires only once it is back to actually running.
func TestStallExemptHumanWait(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.StallThreshold = 150 * time.Millisecond
	ctls["w"].block = true
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "hang"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "run started", func() bool { return ctls["w"].runs.Load() == 1 })
	sp.Roster.setPhase("w", PhaseWaitingApproval, "bash")

	time.Sleep(300 * time.Millisecond) // well past the threshold while exempt
	if got := opsMailCount(t, sp.Store, "user", "stall: w"); got != 0 {
		t.Fatalf("exempt phase still alerted: %d notices", got)
	}

	sp.Roster.setPhase("w", PhaseExecuting, "bash")
	waitFor(t, 5*time.Second, "alert fired once the human wait ended", func() bool {
		return opsMailCount(t, sp.Store, "user", "stall: w") == 1
	})
}

// RP-14 AC#3: with a hard timeout the run is cancelled, its mail unclaims, and
// the member retries it on the next wake (runs climbs past 1).
func TestStallHardTimeoutKillsAndRetries(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.StallThreshold = 20 * time.Millisecond
	sp.settings.StallHardTimeout = 50 * time.Millisecond
	ctls["w"].block = true
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "hang forever"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "run killed and retried", func() bool {
		return opsMailCount(t, sp.Store, "user", "run cancelled") >= 1 && ctls["w"].runs.Load() >= 2
	})
	if got := opsMailCount(t, sp.Store, "user", "stall: w"); got < 1 {
		t.Errorf("kill should be preceded by a stall alert, got %d", got)
	}
}

// RP-14 AC#4: a zero threshold disables the watchdog entirely (the ctlSpace
// default — unit spaces opt in explicitly).
func TestStallDisabledWhenThresholdZero(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	ctls["w"].block = true
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "hang"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "run started", func() bool { return ctls["w"].runs.Load() == 1 })
	time.Sleep(80 * time.Millisecond)
	if got := opsMailCount(t, sp.Store, "user", "stall"); got != 0 {
		t.Errorf("watchdog alerted with threshold 0: %d notices", got)
	}
}
