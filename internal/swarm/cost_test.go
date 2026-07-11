package swarm

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

func centsEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestMeterV2PricesAtMeterTime (CST-1): the four classes accumulate
// separately, each delta is priced with the model that produced it, cache
// traffic is priced but never counted against token budgets, and an
// unpriced model counts tokens while flagging the dollars.
func TestMeterV2PricesAtMeterTime(t *testing.T) {
	sp := liteGraphSpace(t)
	today := localDay(time.Now())
	sonnet := string(constant.SONNET_4_6) // $3 in / $15 out / $0.30 cr / $3.75 cw per 1M

	// Cache-heavy delta: 1M in of which 800k cache-read + 100k cache-write.
	total, space := sp.addDailyUsage("w", sonnet, 1_000_000, 100_000, 800_000, 100_000, today)
	// uncached 100k*3 + cr 800k*0.30 + cw 100k*3.75 + out 100k*15 = .3+.24+.375+1.5
	wantUSD := 0.3 + 0.24 + 0.375 + 1.5
	d := sp.DayFor("w")
	if !centsEq(d.CostUSD, wantUSD) || d.Unpriced {
		t.Fatalf("cache-heavy day = %+v, want $%.3f priced", d, wantUSD)
	}
	if d.In != 1_000_000 || d.Out != 100_000 || d.CacheR != 800_000 || d.CacheW != 100_000 {
		t.Fatalf("classes = %+v", d)
	}
	// Budget figure = In+Out only — cache never counts against token caps.
	if total != 1_100_000 || space.Tokens != 1_100_000 {
		t.Fatalf("budget figure = %d / space %d, want 1.1M", total, space.Tokens)
	}
	if !centsEq(space.CostUSD, wantUSD) || space.Unpriced {
		t.Fatalf("space = %+v", space)
	}

	// A later delta on an UNPRICED model: tokens count, dollars freeze, flag set.
	total, space = sp.addDailyUsage("w", "custom-model", 50_000, 10_000, 0, 0, today)
	d = sp.DayFor("w")
	if !d.Unpriced || !centsEq(d.CostUSD, wantUSD) {
		t.Fatalf("after unpriced delta = %+v, want flag + unchanged USD", d)
	}
	if total != 1_160_000 {
		t.Fatalf("budget figure after unpriced delta = %d", total)
	}
	if !space.Unpriced {
		t.Fatal("space aggregate lost the unpriced flag")
	}

	// A second member's spend lands in the space aggregate.
	sp.addDailyUsage("lead", sonnet, 0, 100_000, 0, 0, today) // $1.50
	if got := sp.SpaceToday(); !centsEq(got.CostUSD, wantUSD+1.5) || got.Tokens != 1_260_000 {
		t.Fatalf("space today = %+v", got)
	}
}

// TestMeterV2PersistRoundTrip (CST-1): v2 persists all classes + USD +
// the space-trip mark; a legacy v1 file imports once as an In-lump.
func TestMeterV2PersistRoundTrip(t *testing.T) {
	sp, _ := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	today := localDay(time.Now())
	sp.addDailyUsage("w", string(constant.SONNET_4_6), 1000, 500, 200, 0, today)
	sp.markSpaceTripped()
	sp.persistRuntime()

	rt := loadRuntime(sp.Workdir)
	if rt.UsageDaily != nil {
		t.Fatal("persistRuntime still writes the legacy v1 map")
	}
	got, ok := rt.UsageDailyV2["w"]
	if !ok || got.In != 1000 || got.Out != 500 || got.CacheR != 200 || got.CostUSD <= 0 {
		t.Fatalf("persisted v2 day = %+v ok=%v", got, ok)
	}
	if rt.SpaceTripped != today {
		t.Fatalf("SpaceTripped = %q, want %q", rt.SpaceTripped, today)
	}

	// Restore: a fresh space over the same workdir reloads the v2 meter.
	sp2 := &SwarmSpace{Workdir: sp.Workdir}
	sp2.Reload()
	if d := sp2.DayFor("w"); d != got {
		t.Fatalf("reloaded day = %+v, want %+v", d, got)
	}
	if !sp2.SpaceToday().Tripped {
		t.Fatal("reloaded space lost the trip mark")
	}

	// Legacy import: a v1-only file lands as an In-lump with zero cost.
	writeRuntime(sp2.Workdir, runtimeState{
		Membership: map[string]string{},
		UsageDay:   today,
		UsageDaily: map[string]int{"w": 4200},
	})
	sp3 := &SwarmSpace{Workdir: sp.Workdir}
	sp3.Reload()
	if d := sp3.DayFor("w"); d.In != 4200 || d.CostUSD != 0 || d.Unpriced {
		t.Fatalf("legacy import = %+v, want In-lump, cost 0", d)
	}
	if d := sp3.dailyFor("w"); d != 4200 {
		t.Fatalf("legacy budget figure = %d, want 4200", d)
	}
}

// ceilingMailCount counts the space-ceiling notices for one recipient.
func ceilingMailCount(t *testing.T, sp *SwarmSpace, recipient string) int {
	t.Helper()
	msgs, err := sp.Store.ListMessages(0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Recipient == recipient && strings.Contains(m.Subject, "space budget ceiling") {
			n++
		}
	}
	return n
}

// TestSpaceCeilingTokenTrip (CST-2): crossing daily_budget_total_tokens
// freezes EVERY member — the leader included — with exactly one operator
// notice; a manual unfreeze of one member is honored while the held space
// mark stops a re-trip storm.
func TestSpaceCeilingTokenTrip(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w1": agentdef.RoleWorker, "w2": agentdef.RoleWorker})
	sp.settings.DailyBudgetTotalTokens = 1000
	ctls["w1"].usagePerRun = llm.Usage{InputTokens: 600}
	ctls["w2"].usagePerRun = llm.Usage{InputTokens: 600}
	sup := startSup(t, sp)

	// w1 runs: 600 < 1000 — everyone stays active.
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w1", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "w1 metered", func() bool { return sp.dailyFor("w1") == 600 })
	if membershipOf(sp, "lead") != MembershipActive {
		t.Fatal("premature freeze")
	}

	// w2 runs: space 1200 ≥ 1000 — the ceiling freezes the whole roster.
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w2", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "everyone frozen", func() bool {
		return membershipOf(sp, "lead") == MembershipFrozen &&
			membershipOf(sp, "w1") == MembershipFrozen &&
			membershipOf(sp, "w2") == MembershipFrozen
	})
	if !sp.SpaceToday().Tripped {
		t.Fatal("space trip mark missing")
	}
	waitFor(t, 5*time.Second, "one ceiling notice", func() bool { return ceilingMailCount(t, sp, "user") == 1 })
	msgs, _ := sp.Store.ListMessages(0)
	var body string
	for _, m := range msgs {
		if m.Recipient == "user" && strings.Contains(m.Subject, "space budget ceiling") {
			body = m.Body
		}
	}
	if !strings.Contains(body, "daily_budget_total_tokens") || !strings.Contains(body, "Largest spender") {
		t.Fatalf("notice lacks knob/standings: %q", body)
	}
	// fake-model members are unpriced — the $-exclusion note must ride along.
	if !strings.Contains(body, "EXCLUDES") {
		t.Fatalf("notice lacks the unpriced exclusion note: %q", body)
	}

	// Frozen means frozen: mail wakes nobody.
	runsBefore := ctls["lead"].runs.Load()
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "lead", Body: "hello?"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if got := ctls["lead"].runs.Load(); got != runsBefore {
		t.Fatalf("frozen leader ran: %d -> %d", runsBefore, got)
	}

	// Operator override: unfreeze w1 alone — new mail runs it again (600 more
	// tokens over an already-crossed ceiling); the held space mark prevents a
	// second ceiling notice; the others stay frozen.
	if err := sup.Unfreeze("w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w1", Body: "one more"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "unfrozen w1 runs again", func() bool { return ctls["w1"].runs.Load() >= 2 })
	time.Sleep(60 * time.Millisecond)
	if got := ceilingMailCount(t, sp, "user"); got != 1 {
		t.Fatalf("ceiling re-tripped: %d notices", got)
	}
	if membershipOf(sp, "w2") != MembershipFrozen || membershipOf(sp, "lead") != MembershipFrozen {
		t.Fatal("unfreezing w1 released others")
	}
}

// TestSpaceCeilingUSDTrip (CST-2): the dollar axis trips on PRICED spend.
func TestSpaceCeilingUSDTrip(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings.DailyBudgetTotalUSD = 0.005
	ctls["w"].model = string(constant.SONNET_4_6)
	ctls["w"].usagePerRun = llm.Usage{InputTokens: 1000} // $0.003/run at $3/1M
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "run 1 metered", func() bool { return centsEq(sp.DayFor("w").CostUSD, 0.003) })
	if membershipOf(sp, "w") != MembershipActive {
		t.Fatal("USD ceiling tripped early")
	}

	if _, err := sp.Bus.Send(store.Message{Sender: "user", Recipient: "w", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "USD trip froze everyone", func() bool {
		return membershipOf(sp, "w") == MembershipFrozen && membershipOf(sp, "lead") == MembershipFrozen
	})
	waitFor(t, 5*time.Second, "USD notice", func() bool { return ceilingMailCount(t, sp, "user") == 1 })
}

// TestSpaceCeilingRolloverReleases (CST-2): the day rollover re-arms the
// ceiling and unfreezes everyone together — unless budget_stay_frozen pins.
func TestSpaceCeilingRolloverReleases(t *testing.T) {
	sp, _ := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sup := startSup(t, sp)
	today := localDay(time.Now())
	sp.addDailyUsage("w", "", 2000, 0, 0, 0, today)
	sp.markSpaceTripped()
	sp.markBudgetFrozen("lead")
	sp.markBudgetFrozen("w")
	_ = sup.Freeze("lead")
	_ = sup.Freeze("w")

	tomorrow := localDay(time.Now().Add(24 * time.Hour))
	sup.sweepBudgetDay(time.Now().Add(24 * time.Hour))
	waitFor(t, 5*time.Second, "rollover released everyone", func() bool {
		return membershipOf(sp, "lead") == MembershipActive && membershipOf(sp, "w") == MembershipActive
	})
	st := sp.SpaceToday()
	if st.Tripped || st.Tokens != 0 {
		t.Fatalf("space after rollover = %+v, want re-armed + zeroed", st)
	}
	_ = tomorrow

	// stay_frozen pins across the rollover.
	sp2, _ := ctlSpace(t, map[string]agentdef.Role{"lead": agentdef.RoleLeader})
	sp2.settings.BudgetStayFrozen = true
	sup2 := startSup(t, sp2)
	sp2.markSpaceTripped()
	sp2.markBudgetFrozen("lead")
	_ = sup2.Freeze("lead")
	sup2.sweepBudgetDay(time.Now().Add(24 * time.Hour))
	time.Sleep(30 * time.Millisecond)
	if membershipOf(sp2, "lead") != MembershipFrozen {
		t.Fatal("budget_stay_frozen did not pin the freeze")
	}
	if !sp2.SpaceToday().Tripped {
		t.Fatal("stay_frozen should also hold the space mark")
	}
}

// TestCeilingKnobsRoundTrip (CST-3): parse, clamp, and manifest round-trip.
func TestCeilingKnobsRoundTrip(t *testing.T) {
	m := agentdef.Manifest{Leader: agentdef.Member{Agent: "lead"}}
	m.Settings.DailyBudgetTotalTokens = 2_000_000
	m.Settings.DailyBudgetTotalUSD = 20.5
	p := t.TempDir() + "/evva-swarm.yml"
	if err := agentdef.WriteManifest(p, m); err != nil {
		t.Fatal(err)
	}
	got, err := agentdef.LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.DailyBudgetTotalTokens != 2_000_000 || got.Settings.DailyBudgetTotalUSD != 20.5 {
		t.Fatalf("round-trip = %+v", got.Settings)
	}
}
