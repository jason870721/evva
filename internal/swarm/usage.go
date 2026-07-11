package swarm

import (
	"time"

	"github.com/johnny1110/evva/pkg/constant"
)

// usage.go is the metering state: per-member daily counters (RP-13) and the
// budget-breaker bookkeeping, widened by CST to all four usage classes plus
// a USD figure priced at meter time, and to a space-wide daily ceiling. The
// supervisor feeds it at run boundaries (runOnce reads the controller's
// cumulative Usage before/after each run — the member's own loop goroutine,
// so no concurrent-session read) and trips the breakers; the state itself
// lives on the space so persistRuntime/Reload carry it across restarts
// alongside membership and schedules.

// memberDay is one member's spend today: the four usage classes and the USD
// priced at meter time. Costs are LOCKED per delta — each run's slice is
// priced with the model that produced it, so a mid-day model switch or a
// future rate-card edit never rewrites history. Unpriced marks that at
// least one delta had no rate-card entry (custom/SDK models — the
// deliberate loose pin), making CostUSD a floor rather than a total; every
// surface that renders the figure flags it. JSON tags are the runtime.json
// v2 persistence shape.
type memberDay struct {
	In       int     `json:"in"`
	Out      int     `json:"out"`
	CacheR   int     `json:"cache_r,omitempty"`
	CacheW   int     `json:"cache_w,omitempty"`
	CostUSD  float64 `json:"cost_usd,omitempty"`
	Unpriced bool    `json:"unpriced,omitempty"`
}

// BudgetTokens is the RP-13 budget figure: In+Out only. Token caps bound
// GENERATION volume; cache traffic is priced in dollars but never counted
// against token caps — the asymmetry is deliberate (a cache-heavy day is a
// spend concern, not a generation-volume concern).
func (d memberDay) BudgetTokens() int { return d.In + d.Out }

// spaceDay is the space-wide aggregate the ceiling check reads: today's
// In+Out tokens, today's PRICED spend, whether any member had unpriced
// deltas (the USD figure then excludes them, and the trip mail says so),
// and whether the space ceiling already tripped today.
type spaceDay struct {
	Tokens   int
	CostUSD  float64
	Unpriced bool
	Tripped  bool
}

// usageMeter is the per-space daily ledger. Guarded by sp.mu.
//
// frozen maps a breaker-frozen member to the LOCAL DAY it tripped. Carrying the
// day on the mark (instead of comparing the meter's day at sweep time) is what
// makes the release edge un-stealable: any run ending after midnight advances
// meter.day via ensureMeterLocked, and a sweep that keyed off "day changed"
// would then never fire — leaving budget-frozen members (who never run) frozen
// forever. A mark is stale exactly when its own day != today.
//
// tripped is the space ceiling's own mark (CST): the local day the SPACE
// crossed daily_budget_total_tokens/usd ("" = not tripped). Same day-stamped
// release rule as the member marks.
type usageMeter struct {
	day     string               // local calendar day the counters belong to ("2006-01-02")
	daily   map[string]memberDay // member -> today's spend, all four classes + USD
	frozen  map[string]string    // member frozen BY THE BREAKER -> the day it tripped
	tripped string               // day the SPACE ceiling tripped; "" = armed
}

// localDay is the meter's day key — the LOCAL calendar date, matching the
// operator's wall clock and the reviewer-at-midnight rhythm (timezone semantics
// per pkg/common: bare local, stamped elsewhere).
func localDay(t time.Time) string { return t.Local().Format("2006-01-02") }

// ensureMeterLocked lazily initialises the meter maps and resets the counters
// (and the space-trip mark) when the calendar day moved on. It does NOT
// unfreeze anyone — the supervisor's tick owns that (it must also unfreeze
// members that never run). Caller holds sp.mu.
func (sp *SwarmSpace) ensureMeterLocked(today string) {
	if sp.meter.daily == nil {
		sp.meter.daily = map[string]memberDay{}
	}
	if sp.meter.frozen == nil {
		sp.meter.frozen = map[string]string{}
	}
	if sp.meter.day != today {
		sp.meter.day = today
		sp.meter.daily = map[string]memberDay{}
	}
}

// BudgetFor resolves a member's effective daily token budget: a manifest
// member-level override wins (>0 = own cap, <0 = unlimited even when the space
// sets a default), otherwise the space-wide Settings.DailyBudgetTokens.
// 0 means unlimited. Exported so list_members can render "today X/Y".
func (sp *SwarmSpace) BudgetFor(name string) int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if ov, ok := sp.budgets[name]; ok {
		switch {
		case ov > 0:
			return ov
		case ov < 0:
			return 0 // explicitly unlimited
		}
	}
	return sp.settings.DailyBudgetTokens
}

// addDailyUsage folds one run's four-class token delta into the member's day
// and prices it immediately against the rate card with the model that
// produced it (CST meter v2). Negative class deltas (a session cleared
// mid-day) clamp to zero, the pre-v2 guard. Returns the member's In+Out
// total — the RP-13 budget figure, semantics unchanged — and the space-day
// aggregate snapshot for the ceiling check. Day rollover of the counters
// happens here too (a run can end right after midnight, before the tick
// sweep), but unfreezing stays with the supervisor's sweep.
func (sp *SwarmSpace) addDailyUsage(name, model string, dIn, dOut, dCR, dCW int, today string) (memberTotal int, space spaceDay) {
	dIn, dOut, dCR, dCW = max(dIn, 0), max(dOut, 0), max(dCR, 0), max(dCW, 0)

	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.ensureMeterLocked(today)

	d := sp.meter.daily[name]
	d.In += dIn
	d.Out += dOut
	d.CacheR += dCR
	d.CacheW += dCW
	if dIn+dOut+dCR+dCW > 0 {
		if cost, ok := constant.CostOf(constant.Model(model), dIn, dOut, dCR, dCW); ok {
			d.CostUSD += cost
		} else {
			d.Unpriced = true
		}
	}
	sp.meter.daily[name] = d
	return d.BudgetTokens(), sp.spaceTodayLocked()
}

// spaceTodayLocked sums the day's members into the space aggregate. The map
// is at most roster-sized; summing on demand keeps one source of truth.
// Caller holds sp.mu.
func (sp *SwarmSpace) spaceTodayLocked() spaceDay {
	var s spaceDay
	for _, d := range sp.meter.daily {
		s.Tokens += d.BudgetTokens()
		s.CostUSD += d.CostUSD
		s.Unpriced = s.Unpriced || d.Unpriced
	}
	s.Tripped = sp.meter.tripped != ""
	return s
}

// SpaceToday reports the space-wide aggregate for the metrics/health
// surfaces (CST).
func (sp *SwarmSpace) SpaceToday() spaceDay {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.spaceTodayLocked()
}

// DayFor reports one member's full day (all classes + USD) for the roster
// surfaces. The zero value means "nothing metered today".
func (sp *SwarmSpace) DayFor(name string) memberDay {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.meter.daily[name]
}

// dailyFor returns a member's budget-figure spend (In+Out) for the current
// meter day — the RP-13 gauge the roster shows.
func (sp *SwarmSpace) dailyFor(name string) int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.meter.daily[name].BudgetTokens()
}

// markBudgetFrozen records that the breaker (not the operator) froze a member
// today. Reports whether the mark was new — the caller only trips (freeze +
// notify) on a fresh mark, so a re-run after a manual unfreeze re-trips exactly
// once.
func (sp *SwarmSpace) markBudgetFrozen(name string) (fresh bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	today := localDay(time.Now())
	sp.ensureMeterLocked(today)
	if _, held := sp.meter.frozen[name]; held {
		return false
	}
	sp.meter.frozen[name] = today
	return true
}

// markSpaceTripped records that the SPACE ceiling fired today (CST). Fresh-
// mark-only, the member-mark discipline: a member the operator unfroze may
// keep spending, and the held mark stops the ceiling re-tripping (and
// re-mailing) until rollover re-arms it.
func (sp *SwarmSpace) markSpaceTripped() (fresh bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	today := localDay(time.Now())
	sp.ensureMeterLocked(today)
	if sp.meter.tripped != "" {
		return false
	}
	sp.meter.tripped = today
	return true
}

// clearBudgetFrozen drops the breaker mark, e.g. when the operator manually
// unfreezes a member ("let it run" overrides the breaker until it trips again).
func (sp *SwarmSpace) clearBudgetFrozen(name string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	delete(sp.meter.frozen, name)
}

// isBudgetFrozen reports whether the breaker currently holds this member.
func (sp *SwarmSpace) isBudgetFrozen(name string) bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	_, held := sp.meter.frozen[name]
	return held
}

// sweepMeter advances the counters to `today` (idempotent — a run ending right
// after midnight may already have done it via ensureMeterLocked) and — unless
// the space pins budget-frozen members with BudgetStayFrozen — returns the
// members whose breaker mark is from an EARLIER day, clearing those marks so
// the caller (the supervisor tick) can unfreeze them. The space-trip mark
// releases on the same edge: its own day != today re-arms the ceiling.
// Keying the release on each mark's own day, not on observing the day change,
// means a counter rollover stolen by another member's run can never strand a
// frozen member.
func (sp *SwarmSpace) sweepMeter(today string, stayFrozen bool) (unfreeze []string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.ensureMeterLocked(today)
	if stayFrozen {
		return nil
	}
	if sp.meter.tripped != "" && sp.meter.tripped != today {
		sp.meter.tripped = ""
	}
	for name, day := range sp.meter.frozen {
		if day != today {
			unfreeze = append(unfreeze, name)
			delete(sp.meter.frozen, name)
		}
	}
	return unfreeze
}
