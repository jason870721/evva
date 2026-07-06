# PRD — Swarm Retrospective (post-run learning into memory + templates) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W33**, suggested horizon H4
> per [../long-range.md](../long-range.md) §3b. Composes dream (memory
> consolidation, shipped), the durable event log (RP-16), cost
> accounting (W1), and — for the payoff loop — swarm templates (W21)
> and semantic memory (W6).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> A swarm run generates enormous signal — the full event log, every
> task's plan/verify/rework history, per-member cost, stalls, retries,
> takeovers — and then it's archived and forgotten. The next run of a
> similar shape re-learns the same lessons. Dream proved evva can turn
> a session's residue into durable knowledge for *memory*; this wave
> does it for *team operation*.
> **Reference source:** none — evva-native.

---

## 1. TL;DR

When a swarm run reaches a terminal state (goal met, or wound down), a
**retrospective agent** runs — gated and fenced like dream — over the
run's durable artifacts (event log, ledger history, cost metrics,
chatlog) and produces two outputs:

1. **A retro report** (`.vero/retro/<date>.md`): what the team was
   asked to do, what actually happened (timeline of task states,
   rework, stalls, takeovers), where time and tokens went (cost
   attribution from W1), and — the valuable part — **findings**:
   "task X was re-verified 4 times → the brief was underspecified";
   "member Y idled 40% → over-provisioned"; "the auth refactor
   blocked everything → should have been front-loaded". Operator
   reads it like a sprint retro.

2. **Learnings routed to where they change future behavior:**
   - team-shaped lessons → the swarm memory store (W6 recall surfaces
     them to future leaders: "last time this repo's test suite was
     flaky under parallel members — serialize the test task");
   - structural lessons → a proposed template diff (W21: "this team
     always needed a docs member — add one to the recipe?");
   - protocol lessons → flagged for the operator (not auto-applied to
     prompts — that's EX-10's supervised lane).

The loop this closes: swarms get *better at being swarms* over runs,
with the human approving what's learned. It's the swarm's version of
the gardener — background, gated, digest-delivered, human-gated.

## 2. Goals / non-goals

### Goals

- Retrospective agent: reuses dream's gate/fence (a third consumer —
  further validating the generalized idle-gate from GRD-2/W17);
  triggered on run terminal-state (leader winds down) or on demand
  (`swarm retro <space>`); reads only durable artifacts, never live
  state.
- Report schema: goal recap, phase timeline, task-flow analysis
  (rework/stall/blocked-duration per task), cost breakdown (member ×
  phase, from W1 metering), incident log (stalls, takeovers,
  budget events), findings (ranked, each with evidence pointers into
  the event log).
- Memory routing: team lessons written as typed memories
  (project/reference scope) with provenance (which run, which
  evidence); W6 recall makes them available to future leaders'
  session-open context — the mechanism that makes learning
  *operational* rather than archival.
- Template feedback: structural findings emit a proposed diff against
  the source template (if the space was template-born, W21) —
  operator applies or discards.
- Metrics baseline: retros accumulate a per-repo/per-template baseline
  (typical duration, cost, rework rate) so future retros can say
  "2× the usual rework" — comparison needs history, so this seeds it.

### Non-goals (this wave)

- Auto-applying protocol/prompt changes (EX-10's supervised
  self-tuning lane owns prompt edits; retro only *proposes* and routes
  to memory/templates).
- Real-time coaching mid-run (that's the watchdog/doctor lane; retro
  is post-hoc by definition).
- Cross-run analytics dashboards (the report is markdown + memory;
  a metrics UI is a possible fast-follow, not this wave).
- Judging individual member "performance" punitively (findings are
  about *the system* — briefs, provisioning, graph shape — not blame).

## 3. Design sketch

- **Evidence-anchored findings:** every finding cites event-log
  entries / ledger transitions (ids + timestamps) — the report is
  auditable, and the memory it writes carries the pointers so a future
  skeptic can check. No unsupported assertions; this mirrors the
  plan-mode-v2 evidence discipline.
- **The analysis is mostly deterministic:** task-flow stats, cost
  rollups, stall/rework counts, blocked-duration are computed from the
  event log (cheap, exact); the *model* is used only to interpret and
  rank findings and to phrase lessons — which keeps retros cheap
  (batch-lane eligible, W27) and grounded.
- **Routing gates:** memory writes and template diffs are *proposals*
  in the digest until the operator confirms (the gardener trust model:
  a menu, not an autopilot). Confirmed memories enter the store and
  become recall-eligible.
- **Baseline storage:** a small per-repo/template stats ledger beside
  the retros; grows monotonically; feeds comparison language.

## 4. Work items

- **RET-1 — Deterministic analyzer.** Event-log/ledger → task-flow +
  cost + incident stats; baseline ledger. *Accept:* fixture run
  (recorded event log) yields exact rework/stall/cost numbers;
  baseline updates.
- **RET-2 — Retro agent + gate.** Idle-gate third consumer,
  terminal-state trigger, on-demand command, durable-artifacts-only
  read. *Accept:* retro runs post-winddown in a fixture; never runs
  against a live/active space; batch-lane path works.
- **RET-3 — Report generation.** Schema, evidence anchoring, ranked
  findings, `.vero/retro/`. *Accept:* report validates against schema;
  every finding resolves to real event-log entries.
- **RET-4 — Memory routing.** Typed-memory writes with provenance,
  operator confirmation gate, W6 recall eligibility. *Accept:* a
  confirmed team lesson appears in a subsequent fixture leader's
  session-open recall for the same repo.
- **RET-5 — Template feedback.** Structural-finding → template diff
  proposal (W21). *Accept:* a fixture "missing role" finding produces
  a valid template diff the operator can apply.
- **RET-6 — Docs + changelog.** User-guide (en + zh-tw): reading a
  retro, the confirmation gates, how lessons reach future runs, the
  no-blame framing.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Findings are plausible-but-wrong model narratives | deterministic stats are the backbone; model only interprets; every finding is evidence-anchored and auditable; operator-gated before anything persists |
| Memory pollution (low-value lessons crowd recall) | operator confirmation gate + W6's score-floored recall + dream's later consolidation prunes stale team memories |
| Retro cost on large runs | deterministic-first + batch lane + gate ensures it's idle-time and cheap; caps on chatlog interpretation |
| Learnings overfit one repo/team | provenance + scope tagging; recall is repo/template-scoped by default; baselines make "unusual" detectable rather than treating one run as truth |

## 6. Open questions

1. Should retro run automatically on *every* terminal run, or only
   above a size/duration threshold (small teams aren't worth it)?
   Leaning threshold, operator-tunable.
2. Cross-run trend reports ("this team's rework rate over 5 runs") —
   part of RET or a fast-follow once baselines have data?
3. Does a confirmed protocol finding open a pre-filled EX-10 prompt
   experiment, or just a note? Depends on EX-10's maturity at pickup.
