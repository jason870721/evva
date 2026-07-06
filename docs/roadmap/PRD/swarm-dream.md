# PRD — Swarm Dream (team-level memory consolidation & retro reports) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W31**, suggested horizon H4
> per [../long-range.md](../long-range.md) §3b. Builds on auto-dream
> (shipped v1.8), member-native memory (RP-25, shipped), the
> blackboard (W2), and — for economics — the batch lane (W27).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> Solo evva dreams: idle-time consolidation keeps its memory store
> healthy. A swarm generates 10× the experience — task outcomes,
> verification verdicts, failed approaches, who-was-good-at-what —
> and retains none of it structurally: the ledger and event log are
> *records*, not *lessons*. Space #2 makes every mistake space #1
> made.
> **Reference source:** none — evva-native (the dream architecture is
> the in-repo precedent being lifted one level).

---

## 1. TL;DR

Two artifacts, produced by a gated background pass over a swarm
space's durable records (ledger, event log, chatlogs, blackboard
history, verification verdicts):

1. **The retro report** (operator-facing, per space, on completion or
   periodically for long-lived spaces): what was attempted / shipped /
   abandoned; where time and money actually went (cost-accounting
   data); verification failure patterns; task-graph shapes that
   flowed vs stalled; interject/escalation hotspots. A team
   retrospective nobody had to run — rendered on the web console and
   exportable.
2. **Graduated memories** (agent-facing): durable lessons distilled
   into the *typed memory system* at the right scope — member-scoped
   ("nono's briefs work better with explicit file lists"),
   space/team-scoped (into the team protocol's advisory notes or a
   standing blackboard section), and operator-approved global
   memories ("this repo's e2e suite is flaky under parallel runs").
   Same review discipline as dream: merge/prune/propose, with the
   swarm twist that **cross-boundary graduations (member → global)
   require operator approval** — a lesson one space learned may be
   another space's poison.

Same safety envelope as solo dream: idle-gated, fenced (one background
agent machine-wide — dream, gardener, and swarm-dream share the fence
and a priority order), budget-capped, journal-logged, and everything
it writes is reviewable/revertable.

## 2. Goals / non-goals

### Goals

- Source digestion: deterministic pre-passes over ledger/events/
  chatlogs produce compact per-task and per-member summaries BEFORE
  any model call (the discovery-first economics gardener uses) — the
  model consolidates digests, never raw logs.
- Retro report: schema'd sections (outcomes, economics, verification
  patterns, graph flow analysis, member observations), rendered on
  the console, markdown-exportable; report generation is idempotent
  per space-epoch (re-running refines, never duplicates).
- Memory graduation pipeline: candidate lessons with evidence links
  (task ids, event refs) → scope classification (member / team /
  global-proposal) → member-scope and team-scope apply automatically
  under caps; global proposals queue for operator review (console
  card + notification).
- Trigger model: space completion (all tasks terminal), operator
  command (`swarm retro <space>`), or long-lived-space cadence
  (weekly, config); always subject to the shared idle gate + fence.
- Batch-lane execution (W27) for the fan-out digestion stages — this
  workload is the poster child for half-price async.
- Feedback loop closure: graduated team lessons land where the next
  space actually reads them (protocol advisory section / blackboard
  seed for `swarm init` templates — W21 synergy: templates can carry
  "lessons learned" slots).

### Non-goals (this wave)

- Cross-*operator* learning (nothing leaves the machine).
- Automatic protocol *rewriting* (lessons append to advisory
  sections; changing the core team protocol is a human's diff).
- Real-time in-flight coaching (this is post-hoc; in-flight guidance
  is the leader's job, with doctor/watchdog covering pathology).
- Performance *rating* of members for pruning/hiring decisions — the
  report observes patterns; roster decisions are the operator's, and
  the docs say so explicitly (this is a teammate-shaped system;
  surveillance framing would poison it).
- Solo-session retro reports (a natural sibling, but session-tree +
  memory already cover the solo loop's needs; revisit on demand).

## 3. Design sketch

- **Epoch model:** a space accumulates records; a retro pass consumes
  records since the last epoch mark and stamps a new one — long-lived
  spaces get incremental retros, completed spaces get one final pass.
  Epochs live in the store; re-running an epoch is idempotent.
- **Digestion determinism:** per-task digests (state history, cost,
  verify verdicts, durations) and per-member digests (task mix,
  outcome rates, mail patterns) are pure functions over the store —
  testable without any LLM, and independently useful (the console
  could render them raw).
- **Scope classifier discipline:** the consolidation prompt must
  justify scope per lesson with evidence links; anything ambiguous
  defaults DOWN (member < team < global) — over-scoping a wrong
  lesson is the failure mode that matters.
- **Fence arbitration:** dream > swarm-dream > gardener priority when
  multiple background passes contend (memory hygiene beats retro
  beats chores); one shared fence implementation (the gardener wave's
  GRD-2 generalization is the same seam — whichever lands second
  consumes it).

## 4. Work items

- **SDR-1 — Epochs + deterministic digests.** Epoch marks, per-task/
  per-member digest functions, console raw-render. *Accept:* digest
  outputs are stable across re-runs on a fixture space; epoch
  incrementality correct (records counted once).
- **SDR-2 — Retro report generation.** Consolidation prompts, report
  schema, console render + export, idempotency. *Accept:* fixture
  space (seeded with failures, cost spread, a stalled branch) yields
  a report whose sections cite real task ids; re-run refines in
  place.
- **SDR-3 — Graduation pipeline.** Candidate extraction, scope
  classification with evidence, member/team auto-apply under caps,
  global proposal queue + console review card. *Accept:* seeded
  lessons land at correct scopes; an ambiguous lesson defaults down;
  global candidates never auto-apply.
- **SDR-4 — Triggers + fence + budget.** Completion/command/cadence
  triggers, shared-fence integration with priority order, per-pass
  budget caps, journal. *Accept:* retro never runs while dream holds
  the fence; budget exhaustion stops cleanly with a journaled
  partial.
- **SDR-5 — Batch-lane adoption.** Digestion fan-out via W27 where
  available, sync fallback. *Accept:* fixture pass completes on the
  fake batch provider; costs tagged `lane: batch`.
- **SDR-6 — Docs + changelog.** User-guide (en + zh-tw): what retros
  contain, the graduation scopes, the no-surveillance posture, how
  lessons reach future spaces.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Wrong lessons graduate and steer future spaces badly | evidence-linked candidates, default-down scoping, operator gate on global, caps per pass, and memories remain background-context-not-instructions per the established recall contract |
| Retro cost on big spaces | deterministic digestion first, batch lane, budget caps — the model sees kilobytes, not the event log |
| Report reads as employee surveillance | framing is in the schema (patterns, not rankings); docs are explicit; no per-member scores exist anywhere |
| Fence complexity across three background systems | one shared implementation, priority table, and the doctor checks fence health |

## 6. Open questions

1. Should the retro report feed the eval harness (W4) — failed
   verification patterns becoming regression fixtures automatically?
   (Tempting closed loop; needs the W4 fixture format to stabilize
   first.)
2. Team-scope lesson placement: protocol advisory section vs standing
   blackboard section — which do members actually re-read? Audit the
   wake-brief composition to decide.
3. Long-lived space cadence default (weekly?) — tune against real
   ops-monitor-style spaces once W21 templates make them common.
