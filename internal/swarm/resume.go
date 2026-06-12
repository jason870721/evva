package swarm

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
)

// resume.go makes a space survive process death (SPRD-1-11). The durable state
// is already split across three stores: the task ledger + messages live in
// vero.db (1-2) — which since RP-20 also holds runtime schedule overrides
// (per-member rows + tombstones; see store/schedules.go) — and each agent's
// transcript lives in the SDK session store (<AppHome>/sessions/<workdir-slug>/,
// keyed by persona == member name). What none of them hold is membership
// (active vs frozen) and the budget meter, so runtime.json carries those.
// Reload stitches the pieces back on a rebuild.

// runtimeState is the per-space volatile snapshot persisted to
// <workdir>/.vero/runtime.json: membership so a frozen member comes back
// frozen, plus the budget meter.
type runtimeState struct {
	Membership map[string]string `json:"membership"` // name -> "active" | "frozen"

	// Schedules is LEGACY, read-only (pre-RP-20): older builds persisted the
	// WHOLE live schedule map here — manifest seeds included — and treated a
	// present map as authoritative on Reload, which silently hijacked manifest
	// authority for members the leader never touched. RP-20 moved runtime
	// overrides into per-member store rows; Reload imports a legacy map once
	// (diffing it against the manifest seeds to recover provenance) and
	// rewrites the file without it. persistRuntime never writes it again.
	Schedules map[string]agentdef.Schedule `json:"schedules,omitempty"`

	// RP-13 budget meter: the local day the counters belong to, each member's
	// spend that day, and which members the BREAKER froze mapped to the day
	// they tripped (so a restart can tell a budget freeze from an operator
	// freeze, and the sweep can release stale marks — only breaker freezes
	// auto-unfreeze at rollover). Absent in a pre-RP-13 file → zero meter.
	UsageDay     string            `json:"usage_day,omitempty"`
	UsageDaily   map[string]int    `json:"usage_daily,omitempty"`
	BudgetFrozen map[string]string `json:"budget_frozen,omitempty"`

	// PermModes holds RUNTIME permission-mode overrides (the web's per-member
	// switch) — overrides ONLY, never construction-time seeds, so the manifest
	// stays authoritative for members the operator never touched (the RP-20
	// schedules lesson applied from day one). Reload reapplies entries for
	// members still on the roster; a fresh register discards the field.
	PermModes map[string]string `json:"perm_modes,omitempty"`
}

func runtimePath(workdir string) string {
	return filepath.Join(workdir, ".vero", "runtime.json")
}

// persistRuntime writes the current roster membership and budget meter to
// runtime.json. Called whenever membership changes (freeze/unfreeze/add/
// remove) or the meter persists; a best-effort write — a failure only costs
// the restore-on-restart guarantee, never correctness. Schedules are NOT
// here: runtime overrides live as store rows (RP-20), and writing the live
// map — manifest seeds included — is exactly the hijack RP-20 removed.
func (sp *SwarmSpace) persistRuntime() {
	rs := runtimeState{
		Membership: map[string]string{},
	}
	for _, mv := range sp.Roster.Snapshot() {
		rs.Membership[mv.Name] = string(mv.Membership)
	}
	sp.mu.Lock()
	rs.UsageDay = sp.meter.day
	if len(sp.meter.daily) > 0 {
		rs.UsageDaily = make(map[string]int, len(sp.meter.daily))
		maps.Copy(rs.UsageDaily, sp.meter.daily)
	}
	if len(sp.meter.frozen) > 0 {
		rs.BudgetFrozen = make(map[string]string, len(sp.meter.frozen))
		maps.Copy(rs.BudgetFrozen, sp.meter.frozen)
	}
	if len(sp.permOverrides) > 0 {
		rs.PermModes = make(map[string]string, len(sp.permOverrides))
		maps.Copy(rs.PermModes, sp.permOverrides)
	}
	sp.mu.Unlock()
	writeRuntime(sp.Workdir, rs)
}

// writeRuntime serialises one runtimeState to runtime.json (best-effort).
func writeRuntime(workdir string, rs runtimeState) {
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(runtimePath(workdir), data, 0o644)
}

// loadRuntime reads runtime.json; a missing/corrupt file yields an empty state
// (every member active — the first-boot default).
func loadRuntime(workdir string) runtimeState {
	rs := runtimeState{Membership: map[string]string{}}
	b, err := os.ReadFile(runtimePath(workdir))
	if err != nil {
		return rs
	}
	if err := json.Unmarshal(b, &rs); err != nil || rs.Membership == nil {
		return runtimeState{Membership: map[string]string{}}
	}
	return rs
}

// Reload restores a rebuilt space to where it died:
//
//  1. resume each member's most recent real transcript (its persona session),
//  2. re-queue each member's durable unread mail onto its mailbox, and
//  3. reapply persisted frozen membership.
//
// Call it AFTER NewSpace (agents + mailboxes exist) and BEFORE the Supervisor
// starts the run loops — §6.2's ordering: requeue after the inbox exists, before
// the loop drains it, so no wake is lost. Tasks need no handling: they live in
// vero.db, so a rebuilt space already sees the same ledger (a row left `running`
// is still `running`). Reload is idempotent.
func (sp *SwarmSpace) Reload() {
	sp.mu.Lock()
	members := make(map[string]agent.Agent, len(sp.agents))
	maps.Copy(members, sp.agents)
	sp.mu.Unlock()

	rt := loadRuntime(sp.Workdir)

	// Runtime schedule overrides live in the store (RP-20); a hand-built lite
	// space (tests) may have none — skip like the nil-safe metrics.
	if sp.Store != nil {
		// One-time legacy import (pre-RP-20 runtime.json carried the whole
		// schedule map): convert it to per-member store rows BEFORE applying
		// rows below, so old and new files rebuild through one path. The file
		// is rewritten without the field at the end of Reload.
		if rt.Schedules != nil {
			sp.importLegacySchedules(rt.Schedules)
		}

		// Apply the overrides (RP-20 §2.3): per-member priority — a store row
		// beats the manifest seed (a tombstone row means "no schedule, even
		// though the manifest declares one"); a member without a row keeps its
		// manifest seed, so editing the manifest takes effect for members the
		// leader never touched. Runs before the supervisor seeds each member's
		// timer from sp.schedules in startMemberLoop. Rows for members no
		// longer on the roster (manifest edited while down) stay dormant.
		if rows, err := sp.Store.ListSchedules(); err == nil {
			for _, r := range rows {
				if _, ok := sp.Roster.membership(r.Member); !ok {
					continue
				}
				if r.Cleared {
					sp.dropScheduleEntry(r.Member)
					continue
				}
				sp.setRuntimeSchedule(r.Member, scheduleFromRow(r), r.UpdatedAt)
			}
		}
	}

	// Restore the budget meter (RP-13). A stale day is left as-is: the
	// supervisor's first tick sweep rolls it over and releases budget-frozen
	// members (their frozen MEMBERSHIP below is what keeps them parked until
	// then). Same-day restart keeps counters and marks — no budget reset by
	// bouncing the service.
	sp.mu.Lock()
	sp.meter.day = rt.UsageDay
	sp.meter.daily = make(map[string]int, len(rt.UsageDaily))
	maps.Copy(sp.meter.daily, rt.UsageDaily)
	sp.meter.frozen = make(map[string]string, len(rt.BudgetFrozen))
	maps.Copy(sp.meter.frozen, rt.BudgetFrozen)
	sp.mu.Unlock()

	for name, ag := range members {
		if id := latestSessionFor(ag, name); id != "" {
			_ = ag.ResumeSession(id)
		}
		// A run that died mid-flight may have left messages claimed (claimed_at
		// set, read_at NULL). Reset those to unread first so they re-queue and
		// re-fold — otherwise ClaimUnread would skip them and they'd never be
		// delivered (RP-1: the DB is truth, a dangling claim is recoverable).
		_ = sp.Store.UnclaimFor(name)
		if ids, err := sp.Store.UnreadFor(name); err == nil && len(ids) > 0 {
			sp.Bus.Requeue(name, ids)
		}
		if rt.Membership[name] == string(MembershipFrozen) {
			sp.Roster.setMembership(name, MembershipFrozen)
		}
		// Reapply a runtime permission-mode override (web switch) over the
		// construction-time seed. Members no longer on the roster simply
		// don't iterate here — their stale override drops at the next
		// persistRuntime, which snapshots the live map re-seeded below.
		if mode, ok := rt.PermModes[name]; ok {
			if err := ag.SetPermissionModeName(mode); err == nil {
				sp.Roster.setPermMode(name, mode)
				sp.recordPermOverride(name, mode)
			}
		}
	}

	// Retire the legacy schedules field now that it is imported. Done LAST —
	// persistRuntime snapshots the roster, so it must run after the frozen
	// membership restore above or a crash right here would resurrect frozen
	// members as active.
	if rt.Schedules != nil {
		sp.persistRuntime()
	}
}

// importLegacySchedules is the one-time RP-20 upgrade for a pre-RP-20
// runtime.json, whose schedules map held the WHOLE live state — manifest
// seeds and runtime changes alike, indistinguishable. Provenance is recovered
// by diffing against the fresh manifest seeds (sp.schedules holds exactly
// those at this point in Reload): an entry equal to its seed is just the
// snapshot — skip it, the manifest stays authoritative; a differing or extra
// entry was a runtime set — write a row; a seeded member missing from the map
// was runtime-cleared — write a tombstone. Best-effort: a failed row write
// costs that member's override, never the rebuild.
func (sp *SwarmSpace) importLegacySchedules(legacy map[string]agentdef.Schedule) {
	now := time.Now().UnixMilli()
	sp.mu.Lock()
	seeds := make(map[string]agentdef.Schedule, len(sp.schedules))
	maps.Copy(seeds, sp.schedules)
	sp.mu.Unlock()

	for name, sch := range legacy {
		if seed, ok := seeds[name]; ok && seed == sch {
			continue
		}
		_ = sp.Store.PutSchedule(store.ScheduleRow{
			Member: name, Cron: sch.Cron, EveryNS: int64(sch.Every), Prompt: sch.Prompt,
			UpdatedAt: now,
		})
	}
	for name := range seeds {
		if _, ok := legacy[name]; !ok {
			_ = sp.Store.PutSchedule(store.ScheduleRow{Member: name, Cleared: true, UpdatedAt: now})
		}
	}
}

// scheduleFromRow converts a store row back to the scheduler's value type.
func scheduleFromRow(r store.ScheduleRow) agentdef.Schedule {
	return agentdef.Schedule{Cron: r.Cron, Every: time.Duration(r.EveryNS), Prompt: r.Prompt}
}

// latestSessionFor returns the id of the most recent persisted session that
// belongs to this member (Profile == name) and carries a real transcript
// (MessageCount > 0). ListSessions is mtime-descending, so the first match is
// the newest; "" means the member has no prior transcript (first boot, or it
// never ran). Filtering on a non-empty transcript skips the empty snapshot a
// freshly-constructed agent may have just written.
func latestSessionFor(ag agent.Agent, name string) string {
	rows, _ := ag.ListSessions()
	for _, r := range rows {
		if r.Profile == name && r.MessageCount > 0 {
			return r.ID
		}
	}
	return ""
}
