package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/llm"
)

// budgetMailCount counts the breaker's notification mail addressed to one
// recipient about one member.
func budgetMailCount(t *testing.T, st *store.Store, recipient, member string) int {
	t.Helper()
	msgs, err := st.ListMessages(0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Recipient == recipient && strings.Contains(m.Subject, "budget breaker: "+member) {
			n++
		}
	}
	return n
}

func membershipOf(sp *SwarmSpace, name string) Membership {
	m, _ := sp.Roster.membership(name)
	return m
}

func viewOf(t *testing.T, sp *SwarmSpace, name string) MemberView {
	t.Helper()
	for _, mv := range sp.Roster.Snapshot() {
		if mv.Name == name {
			return mv
		}
	}
	t.Fatalf("member %q not in snapshot", name)
	return MemberView{}
}

// RP-13 AC#2: a member that crosses its daily budget is frozen at the next run
// boundary, both the leader and the operator are notified, and further mail no
// longer triggers runs.
func TestBudgetBreakerTripsFreezesAndNotifies(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.DailyBudgetTokens = 1000
	ctls["w"].usagePerRun = llm.Usage{InputTokens: 400, OutputTokens: 200} // 600/run
	startSup(t, sp)

	// Run 1: 600 < 1000 — stays active, counter visible.
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "first run metered", func() bool {
		return ctls["w"].runs.Load() == 1 && sp.dailyFor("w") == 600
	})
	if m := membershipOf(sp, "w"); m != MembershipActive {
		t.Fatalf("membership after run 1 = %s, want active", m)
	}

	// Run 2: 1200 ≥ 1000 — frozen, marked, notified (leader + user, once each).
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go again"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "breaker froze the member", func() bool {
		return membershipOf(sp, "w") == MembershipFrozen
	})
	if !sp.isBudgetFrozen("w") {
		t.Error("breaker mark missing after trip")
	}
	waitFor(t, 5*time.Second, "notifications delivered", func() bool {
		return budgetMailCount(t, sp.Store, "user", "w") == 1 && budgetMailCount(t, sp.Store, "lead", "w") == 1
	})
	waitFor(t, 5*time.Second, "leader woken by the notice", func() bool {
		return ctls["lead"].runs.Load() >= 1 && strings.Contains(ctls["lead"].lastPrompt(), "budget breaker: w")
	})

	// The roster snapshot carries the meter (RP-13 AC#1).
	v := viewOf(t, sp, "w")
	if v.Usage.InputTokens != 800 || v.Usage.OutputTokens != 400 || v.DailyTokens != 1200 {
		t.Errorf("snapshot usage = %+v daily %d, want in 800 out 400 daily 1200", v.Usage, v.DailyTokens)
	}

	// RP-28: the run-token histogram counted the SAME two runs the daily
	// counter metered (one source, no double books) — 600 tokens/run lands
	// both in the <1k bucket.
	mm, _ := sp.MetricsSnapshot()
	if got := mm["w"].RunTokens; got != [4]int64{2, 0, 0, 0} {
		t.Errorf("RunTokens = %v, want [2 0 0 0] — two 600-token runs in the <1k bucket", got)
	}

	// Frozen: further mail queues but never runs.
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "ignored"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // a dozen ticks
	if got := ctls["w"].runs.Load(); got != 2 {
		t.Errorf("frozen member ran: runs = %d, want 2", got)
	}
}

// RP-13 AC#3: the day rollover resets the counters and auto-unfreezes members
// the breaker held — driven by the tick, since a frozen member never runs.
func TestBudgetRolloverUnfreezes(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.DailyBudgetTokens = 100
	ctls["w"].usagePerRun = llm.Usage{InputTokens: 200}
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "tripped", func() bool { return membershipOf(sp, "w") == MembershipFrozen })

	// Model "tripped yesterday": age both the mark and the counter day. The
	// mark's own day is what releases the member — even if another member's
	// run already advanced the counter day past midnight (the stolen-edge bug).
	sp.mu.Lock()
	sp.meter.day = "2000-01-01"
	sp.meter.frozen["w"] = "2000-01-01"
	sp.mu.Unlock()

	waitFor(t, 5*time.Second, "rollover unfroze the member", func() bool {
		return membershipOf(sp, "w") == MembershipActive && !sp.isBudgetFrozen("w") && sp.dailyFor("w") == 0
	})
}

// RP-13: budget_stay_frozen pins breaker-frozen members across the rollover —
// counters reset, the freeze (and its mark) stays until an operator unfreezes.
func TestBudgetRolloverStayFrozen(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.DailyBudgetTokens = 100
	sp.settings.BudgetStayFrozen = true
	ctls["w"].usagePerRun = llm.Usage{InputTokens: 200}
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "tripped", func() bool { return membershipOf(sp, "w") == MembershipFrozen })

	sp.mu.Lock()
	sp.meter.day = "2000-01-01"
	sp.meter.frozen["w"] = "2000-01-01"
	sp.mu.Unlock()

	waitFor(t, 5*time.Second, "counters reset on rollover", func() bool { return sp.dailyFor("w") == 0 })
	time.Sleep(60 * time.Millisecond) // give the sweep ample ticks to (wrongly) unfreeze
	if m := membershipOf(sp, "w"); m != MembershipFrozen {
		t.Fatalf("membership after stay-frozen rollover = %s, want frozen", m)
	}
	if !sp.isBudgetFrozen("w") {
		t.Error("breaker mark should survive a stay-frozen rollover")
	}
}

// RP-13: a manual unfreeze overrides the breaker (mark cleared) — and a member
// still over budget re-trips exactly once after its next run.
func TestUnfreezeClearsMarkAndRetrips(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.DailyBudgetTokens = 100
	ctls["w"].usagePerRun = llm.Usage{InputTokens: 200}
	sup := startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "first trip", func() bool {
		return membershipOf(sp, "w") == MembershipFrozen && budgetMailCount(t, sp.Store, "user", "w") == 1
	})

	if err := sup.Unfreeze("w"); err != nil {
		t.Fatalf("Unfreeze: %v", err)
	}
	if sp.isBudgetFrozen("w") {
		t.Fatal("Unfreeze should clear the breaker mark")
	}

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "one more"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 5*time.Second, "re-tripped after the operator override", func() bool {
		return membershipOf(sp, "w") == MembershipFrozen && budgetMailCount(t, sp.Store, "user", "w") == 2
	})
}

// RP-13 AC#1/#3: the roster seeds a resumed member's cumulative usage before
// its first run, and the meter survives a persist → load → Reload round-trip.
func TestUsageSeedAndPersistRoundTrip(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	ctls["w"].usage = llm.Usage{InputTokens: 5, OutputTokens: 7} // pre-start: models a resumed session
	startSup(t, sp)

	if v := viewOf(t, sp, "w"); v.Usage.InputTokens != 5 || v.Usage.OutputTokens != 7 {
		t.Errorf("seeded snapshot usage = %+v, want in 5 out 7", v.Usage)
	}

	today := localDay(time.Now())
	sp.addDailyUsage("w", 42, today)
	sp.markBudgetFrozen("w")
	sp.persistRuntime()

	rt := loadRuntime(sp.Workdir)
	if rt.UsageDay != today || rt.UsageDaily["w"] != 42 || rt.BudgetFrozen["w"] != today {
		t.Fatalf("persisted meter = day %q daily %v frozen %v", rt.UsageDay, rt.UsageDaily, rt.BudgetFrozen)
	}

	sp2 := &SwarmSpace{Workdir: sp.Workdir, Roster: newRoster(), schedules: map[string]agentdef.Schedule{}}
	sp2.Reload()
	if sp2.dailyFor("w") != 42 || !sp2.isBudgetFrozen("w") {
		t.Errorf("restored meter: daily %d frozen %v, want 42/true", sp2.dailyFor("w"), sp2.isBudgetFrozen("w"))
	}
}

// RP-13: BudgetFor resolution — member override beats the space default, a
// negative override means exempt, zero inherits.
func TestBudgetForResolution(t *testing.T) {
	sp, _ := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	sp.settings.DailyBudgetTokens = 500
	sp.budgets = map[string]int{"capped": 100, "exempt": -1}

	if got := sp.BudgetFor("capped"); got != 100 {
		t.Errorf("override = %d, want 100", got)
	}
	if got := sp.BudgetFor("exempt"); got != 0 {
		t.Errorf("exempt = %d, want 0 (unlimited)", got)
	}
	if got := sp.BudgetFor("w"); got != 500 {
		t.Errorf("inherit = %d, want 500", got)
	}
}
