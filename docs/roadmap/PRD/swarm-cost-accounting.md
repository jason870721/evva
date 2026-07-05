# PRD — Swarm Cost Accounting & Space Budget — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed.
> **Target release:** TBD — wave-sized minor (`v1.11+` candidate). Per the
> checkpoint-rewind precedent, the CLAUDE.md wave → minor row is added only
> when the operator confirms the wave.
> **Roadmap source:** swarm design review 2026-07-04 — RP-13 caps each
> member's daily *tokens*, but a swarm has no aggregate ceiling and no
> currency anywhere; an operator cannot answer "what did today cost" or
> "stop everything at $20".
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1), 2026-07-04/05. All file:line references verified against
> that commit. **Notably: the audit corrected this wave's original premise —
> a full USD rate card already ships in `pkg/constant` and is already
> rendered live in the solo TUI. This wave is wiring, not invention.**
> **Reference source:** none — evva-native.

---

## 1. TL;DR

The pricing layer exists and is proven: `constant.Pricing`
(pkg/constant/llm.go:146, USD per 1M tokens with separate cache-read/write
rates), a maintained rate card `MODEL_PRICING` (:177, "verified 2026-06"
across Anthropic/DeepSeek/OpenAI/GLM/Qwen), and `CostOf(model, in, out,
cacheRead, cacheWrite)` (:215). The **solo** TUI already shows live spend
through it (`renderSpend`, pkg/ui/bubbletea/components/status/model.go:288-291;
the `/cost` overlay's rate-card lookup, overlays/cost.go:57).

The **swarm** sees none of it. Its meter counts one integer per member per
day — and only `InputTokens + OutputTokens` (`meterRun`,
internal/swarm/scheduler.go:255-256), ignoring the cache fields that
`llm.Usage` carries (pkg/llm/usage.go:11-15) and that dominate real spend on
cache-heavy providers. There is no space-wide total (`usageMeter.daily` is
per-member, usage.go:20-24), no dollar figure on any swarm surface
(`MemberInfo` exposes four token ints, webapi/api.go:243-246), and no
"stop the whole space" breaker — `HaltAll` is suspend-all
(supervisor.go:565-568), a kill switch, not a budget device.

This wave: **meter v2** (all four usage classes + a USD figure accumulated
at meter time with the member's effective model), a **space-wide daily
ceiling** in tokens and/or dollars whose trip freezes every member through
the existing RP-13 freeze/rollover machinery, and **cost on every swarm
surface** (roster, metrics, healthz, web).

---

## 2. Goals / non-goals

### Goals

- Every member row answers "tokens today (by class) and dollars today";
  every space answers "total today" — live, on the web roster, `list_members`,
  `/metrics`, and `/healthz`.
- `settings.daily_budget_total_tokens` / `daily_budget_total_usd`: crossing
  either freezes the whole space (leader included, §4), notifies the
  operator, and auto-releases at day rollover under the existing
  `budget_stay_frozen` discipline (usage.go:126-138).
- Costs are locked at spend time: each run's delta is priced with the model
  that produced it, so mid-day model switches and future rate-card edits
  never rewrite history.
- Unpriced models (custom/SDK-registered — the deliberate loose pin,
  space.go:322-327) degrade gracefully: tokens count, dollars show "n/a",
  the space USD ceiling ignores what it cannot price (and says so).
- RP-13 per-member semantics are untouched: member caps still mean
  In+Out tokens, trip and release exactly as today.

### Non-goals (this wave)

- No historical timeseries or per-day ledgers — today's numbers plus the
  `run_end` event stream (which already carries `Usage`,
  pkg/event/event.go:237) remain the raw history; exporters own the rest.
- No per-member *dollar* caps (member caps stay token-denominated; open
  question #3).
- No operator-editable rate card in the manifest (open question #2) — the
  constant table is the single source this wave.
- No provider billing-API reconciliation; this is metered-usage × list
  price, an estimate and labeled as such.
- No changes to the solo TUI cost surfaces.

---

## 3. Verified current state

### 3.1 The meter is one int per member

`usageMeter` (usage.go:20-24): `day string`, `daily map[string]int`,
`frozen map[string]string`. Fed by `meterRun` (scheduler.go:254):
`delta := (post.In+Out) − (pre.In+Out)` (:255-256) → histogram (:257) →
`addDailyUsage` (:258) → `BudgetFor` compare (:261-262) → `tripBudget`
(:276) which `Freeze`s the member (:277) and mails via `notifyOps` (:292).
Rollover release: `sweepMeter` (usage.go:126). Persistence:
`runtimeState.UsageDay/UsageDaily/BudgetFrozen` (resume.go:44-46), written
:74-81, restored :171-175.

### 3.2 Cache tokens are dropped on the floor

`llm.Usage` carries `CacheReadTokens`/`CacheCreationTokens`
(pkg/llm/usage.go:14-15) and `ctl.Usage()` exposes them at meter time —
`meterRun` reads only In+Out. `Pricing.CostUSD` prices all four classes
(llm.go:159). Any dollar figure derived from today's meter would misprice
cache-heavy runs; meter v2 must widen first.

### 3.3 No model at the meter

`meterRun` never learns *which* model produced the delta. The member's
effective model is fixed at construction (`acfg.DefaultModel`,
space.go:327-332) — the meter needs a per-member model lookup at metering
time (a space-level getter over the construction-time pin).

### 3.4 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| Rate card + math | `Pricing`/`MODEL_PRICING`/`CostOf` (llm.go:146,177,215) | The entire pricing layer — do not fork it |
| Proven rendering | solo TUI spend (status/model.go:288-291) | Display precedent (2-dp USD, "n/a" behavior) |
| Per-member breaker | `BudgetFor`/`markBudgetFrozen`/`sweepMeter` (usage.go:52,91,126); `tripBudget` (scheduler.go:276) | The trip/release semantics the space ceiling copies |
| Freeze primitive | `Supervisor.Freeze` (supervisor.go:258) | Space trip = iterate it; `Unfreeze` (:269) already clears breaker marks |
| Ops alerting | `notifyOps` (scheduler.go:299) | Trip notification (and `ops_alert` event if the notifications wave lands) |
| Legacy-import precedent | resume.go:136 (one-time schedule import) | The UsageDaily → v2 migration pattern |
| Settings parsing | `DailyBudgetTokens` clamp (manifest.go:336,344) | The two new knobs' style |

---

## 4. The trip decision: a space ceiling freezes everyone, leader included

When the space crosses its daily ceiling, three candidate blast radii:

1. **Freeze workers, leave the leader** — rejected. The leader is routinely
   the most expensive member (largest context, most wakes); exempting it
   makes the ceiling soft precisely where spend concentrates.
2. **Suspend-all (`HaltAll`)** — rejected. Suspension is the operator's
   mid-run kill switch and doesn't participate in rollover release; budget
   state belongs to the freeze axis (`MembershipFrozen` + breaker marks),
   which restart-resumes and auto-releases correctly.
3. **Freeze every member via the RP-13 axis** — chosen. Order: mark the
   space-trip in the meter → `notifyOps` (mail is store-write, not LLM —
   it works with everyone frozen) → `Freeze` each active member. Rollover
   (`sweepMeter`) releases the space mark and members together unless
   `budget_stay_frozen`; a manual `Unfreeze` of one member mid-freeze is an
   operator override and is honored (it already clears breaker marks,
   supervisor.go:277) — but the space mark keeps *new* wakes of everyone
   else frozen until rollover.

The invariant matches RP-13's: the breaker changes *whether members run*,
never any ledger or mail state; a frozen space is fully inspectable.

---

## 5. Design

### 5.1 D1 — Meter v2

`daily` becomes `map[string]memberDay`:

```go
type memberDay struct {
    In, Out, CacheR, CacheW int
    CostUSD  float64 // accumulated at meter time, locked per delta
    Unpriced bool    // some delta had no rate-card entry
}
```

`meterRun` widens its delta to all four classes and prices it immediately:
`cost, ok := constant.CostOf(modelFor(name), dIn, dOut, dCR, dCW)`; `!ok`
sets `Unpriced` and adds tokens only. `modelFor` is a new space getter over
the construction-time pin (§3.3). **Budget comparisons keep In+Out** — both
the member cap (unchanged RP-13 semantics) and the space token ceiling;
cache traffic is priced in USD but never counted against token caps (the
asymmetry is deliberate and documented: token caps bound *generation*
volume, dollar caps bound *spend*).

Persistence: `runtimeState` gains `UsageDailyV2 map[string]memberDayJSON`
(+ space-trip mark); the old `UsageDaily` int map is read once as a legacy
import (In+Out lump, cost 0 — the resume.go:136 pattern) and no longer
written.

### 5.2 D2 — The space ceiling

```yaml
settings:
  daily_budget_total_tokens: 2000000   # 0 = off
  daily_budget_total_usd: 20.0         # 0 = off; either knob may trip
```

After each `addDailyUsage`, the meter also folds the delta into a space-day
aggregate; `meterRun`'s trip guard checks member cap first (unchanged),
then the space ceilings. Trip → §4 sequence, one `notifyOps` naming which
knob tripped and the standings ("space at 2.01M tokens / $20.44; largest:
lead $9.10"). The USD ceiling compares only priced spend; if any member is
`Unpriced`, the trip mail and the metrics flag it ("$-figures exclude
member X (unpriced model)").

### 5.3 D3 — Surfaces

- `MemberInfo` (api.go:211) gains `costTodayUsd float64` + `unpriced bool`
  + the two cache-class token fields; `list_members` mirrors.
- `MetricsInfo` (api.go:371) gains space totals: tokens by class, priced
  USD, ceiling values + tripped flag.
- `HealthInfo` (api.go:359) gains service-wide `costTodayUsd` (sum over
  spaces).
- Web: roster rows show `$x.xx` beside the token gauge; the header shows
  the space total and, when a ceiling is set, a fill meter (the CTX-bar
  pattern); unpriced members show `~` with a tooltip.
- `run_end` already carries full `Usage` (event.go:237) — unchanged; the
  event log remains the per-run cost history.

---

## 6. Work items

**CST-1 — Meter v2.**
Four-class accounting + at-meter USD + `modelFor` getter + space-day
aggregate; `runtimeState` v2 fields with one-time legacy import; member-cap
semantics proven unchanged.
*Accept:* table tests — cache-heavy delta prices correctly per the rate
card; unpriced model sets the flag and never contributes USD; legacy
runtime.json imports as In+Out lump; member trip fires at the same
threshold as before the change.

**CST-2 — Space ceiling + trip.**
The two knobs' evaluation, §4 trip sequence (mark → notify → freeze-all),
rollover release + `budget_stay_frozen`, restart persistence of the trip
mark.
*Accept:* integration — token-trip and USD-trip each freeze all members and
send exactly one ops mail; day rollover releases (or holds, per the knob);
a restart mid-freeze resumes frozen; a manual member `Unfreeze` runs that
member while the space mark holds the rest.

**CST-3 — Config knobs.**
`daily_budget_total_tokens` / `daily_budget_total_usd` parse, clamp,
round-trip (manifest.go Settings + YAML mirror + LoadManifest).
*Accept:* negatives clamp to 0/off; round-trip preserves; both-zero =
byte-identical behavior.

**CST-4 — Surfaces.**
DTO fields, metrics/health aggregates, web roster + header meter,
`list_members` columns.
*Accept:* DTO tests; FE renders priced, unpriced, and tripped states; no
NaN/negative artifacts at rollover.

**CST-5 — Docs.**
User guide (en, zh-tw): "budgets and cost" — the two axes (tokens =
generation volume, USD = spend), the estimate disclaimer, rate-card
provenance (`MODEL_PRICING`, "verified 2026-06") and staleness note;
CHANGELOG.
*Accept:* docs in both languages.

Sequencing: `CST-1 → CST-2 → CST-3 → CST-4 → CST-5` (CST-3 may land with
CST-2).

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| CST-1/2 | usage/scheduler suite extensions; no new fixtures | seconds |
| CST-4 | webapi DTO tests + web2 type-check (Node 24 rig) | unchanged |
| all | no new dependencies | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Rate card goes stale → wrong dollars | Figures labeled "estimate at list price"; card carries its verification date; docs tell operators to treat USD ceilings as guardrails, not invoices; open question #2 tracks an override knob |
| Meter v2 counts more than v1 (cache classes) → surprise trips after upgrade | Token *caps* still compare In+Out only (5.1) — trip thresholds are unchanged by construction; only USD is new |
| Float accumulation drift | float64 cents-scale over a day of deltas — display-precision noise; each delta is priced independently (no compounding) |
| Freeze-all strands an in-flight approval gate | Freeze stops future dispatch, never kills a run (supervisor.go:255-257 semantics); a blocked gate stays answerable and the run finishes; the member just doesn't wake again |
| Unpriced members hide real spend from the USD ceiling | Flagged everywhere it displays; token ceiling still bounds them; trip mail names excluded members |
| Model switched by `ReloadSpace` mid-day misprices | Costs are locked per delta at meter time (5.1) — a switch only affects future deltas, correctly |

---

## 9. Open questions

1. **Leader-exempt mode for the space trip?** Recommend no (§4) — a soft
   ceiling is a broken promise; the operator can set `budget_tokens: -1`
   on the leader to exempt it from *member* caps if they truly want that.
2. **Operator rate-card overrides (`settings.model_pricing`)?** Recommend
   defer — needed only for custom/self-hosted models; when it comes, it
   slots as a merge-over-constant map and `Unpriced` disappears for those
   entries.
3. **Per-member USD caps?** Recommend defer — the space ceiling plus
   member token caps cover the runaway cases; two denominations per member
   doubles knob surface for little control gain.
4. **Ollama/local models price as $0 — show or hide?** Recommend show
   `$0.00` (they're genuinely free at the margin) — hiding reads as
   missing data.

---

## 10. Rollout

1. CST-1..CST-5 via `feature/swarm-cost` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation.
3. Beta validation: a cache-heavy Anthropic space (prompt-cache traffic
   dominant) checked against the provider console's same-day numbers for
   sanity; a deliberate $0.50 ceiling trip end-to-end; a mixed space with
   one unpriced custom model.
4. `release` promotes.
