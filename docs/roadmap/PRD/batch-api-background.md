# PRD — Batch API for Background Work (half-price dream, gardener, evals) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references and re-verify provider batch
> terms at pickup).
> **Target release:** TBD — batch-2 wave **W27**, suggested horizon H3
> per [../long-range.md](../long-range.md) §3b. Depends on W9 (the
> decorator/routing shape) and pays off every background feature:
> dream (shipped), evals (W4), gardener (W17), auto-titles (W7).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> Anthropic's Message Batches and OpenAI's Batch API both price
> asynchronous work at **~50% of synchronous rates** with a 24h
> completion window. evva's background workload — memory
> consolidation, eval sweeps, nightly chores, session titles — is
> *exactly* the latency-insensitive shape batch pricing was built
> for, and evva pays full interactive price for all of it today.
> **Reference source:** none — evva-native; providers' batch API docs
> are the reference.

---

## 1. TL;DR

Add an **async execution lane** to the LLM layer: a `BatchClient`
capability (optional interface beside `llm.Client`, mirroring how the
Embedder capability is planned in W6) that accepts a set of independent
requests, submits them via the provider's batch endpoint, polls/collects
results, and persists in-flight batch state so restarts don't orphan
jobs. Background subsystems opt in per workload:

| Workload | Shape | Fit |
|---|---|---|
| Eval harness sweeps (W4) | dozens–hundreds of independent replay/judge calls | perfect — the flagship consumer |
| Gardener chores (W17) | per-proposal fix loops are sequential, but *discovery triage* and *per-finding assessments* fan out | good for the fan-out stages |
| Dream consolidation | few calls, idle-time | fine — latency is free at 3am |
| Session auto-titles (W7) | tiny, deferred | trivially batchable in daily sweeps |

The agent's *interactive* loop never touches this lane — a batch call
that might take hours has no place in a conversation. The seam is
explicit: `background` route (W9's role tiers) + batch-capable provider
+ workload marked batchable = the discount happens; anything else runs
synchronous exactly as today.

Expected effect: the marginal cost of "evva working while you sleep"
roughly halves, which materially changes how much eval coverage and
gardening an operator turns on. Economics are a feature.

## 2. Goals / non-goals

### Goals

- `BatchClient` capability: `SubmitBatch(ctx, []Request) (BatchHandle,
  error)`, `PollBatch(ctx, BatchHandle) (BatchStatus, error)`,
  `CollectBatch(ctx, BatchHandle) ([]Response, error)` — neutral types,
  provider adapters for Anthropic Message Batches and OpenAI Batch
  first (others as their APIs allow).
- Durable batch ledger: submitted batches persisted (`<EVVA_HOME>/
  batches/`) with workload provenance; a restart re-attaches to
  in-flight batches instead of resubmitting (idempotency keys where
  the provider supports them).
- Scheduler integration: a lightweight poller (rides the existing
  alarm/cron scheduler — no new daemon) checks in-flight batches at
  provider-appropriate intervals and dispatches results to the
  requesting subsystem's callback/queue.
- Workload adoption, in order: eval harness (BAT-4 — the proof),
  auto-titles, dream's batchable stages, gardener fan-outs (as W17
  lands).
- Cost accounting: batch spend flows into the same metering (`/cost`,
  day ledger, swarm accounting) tagged `lane: batch` — the discount is
  visible, which is half the point.
- Degradation: no batch-capable provider on the background route →
  synchronous fallback with a log note; batch failure/expiry →
  per-request fallback policy (`retry-sync | drop | requeue`).

### Non-goals (this wave)

- Batching *interactive* traffic, ever (hard line).
- A generic job queue for arbitrary user work (this is an LLM-layer
  lane, not a task system).
- Provider batch features beyond independent request sets (no
  batch-level dependencies; workloads needing ordering keep the
  synchronous lane).
- Cross-provider batch splitting of one workload in v1 (one batch,
  one provider; the route picks which).

## 3. Design sketch

- **Request independence is the contract:** the capability accepts
  only self-contained requests (system + messages + tools per item).
  Callers own chunking a workload into independent items — which the
  eval harness and title sweeps naturally are. No hidden session
  state.
- **Handle durability:** a `BatchHandle` is `{provider, providerID,
  workload, submittedAt, items[]}` on disk; the poller is stateless
  over the ledger. Collection writes results beside the handle before
  invoking callbacks — crash between collect and callback replays
  safely.
- **Poll cadence:** provider-recommended intervals with backoff
  (batches complete in minutes to hours); the poller coalesces —
  N in-flight batches ≠ N timers.
- **The 24h window is a feature constraint:** workloads declare a
  deadline; anything that can't tolerate the provider's window never
  enters the lane (enforced at submit, not discovered at expiry).

## 4. Work items

- **BAT-1 — Capability + neutral types.** Interface, handle/ledger
  model, fallback policy plumbing. *Accept:* a fake batch provider
  round-trips submit→poll→collect with ledger persistence across a
  simulated restart.
- **BAT-2 — Anthropic + OpenAI adapters.** Wire formats, status
  mapping, idempotency, error taxonomy alignment with W9's classes.
  *Accept:* recorded-fixture conformance (PRV-1 kit extended with
  batch scenarios); live smoke documented.
- **BAT-3 — Poller on the existing scheduler.** Coalesced polling,
  backoff, result dispatch, crash re-attach. *Accept:* restart with
  two in-flight fixture batches re-attaches both; no resubmission.
- **BAT-4 — Eval harness adoption.** The W4 sweep runner gains a
  `--lane batch` mode. *Accept:* a fixture eval sweep completes via
  the fake provider at equivalent results to sync mode; cost report
  shows the lane tag.
- **BAT-5 — Auto-titles + dream adoption.** Deferred title sweeps;
  dream's independent stages marked batchable. *Accept:* titles for
  N untitled sessions arrive via one batch; dream behavior unchanged
  when no batch provider exists.
- **BAT-6 — Docs + changelog.** User-guide (en + zh-tw): what runs on
  the lane, expected delays, cost visibility; operator knob to
  disable the lane entirely.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Provider batch terms/formats change | adapters are thin; PRV-kit batch scenarios pin behavior; re-verify at pickup is in the header |
| Orphaned batches leak spend | durable ledger + re-attach + a doctor check listing in-flight batches older than the window |
| Batch lane creeps toward interactive use | the hard line is enforced in code (only `background`-routed, workload-marked calls admitted) and in review |
| Results arrive after the requesting context is gone (deleted session, disabled gardener) | callbacks are queue-based and tolerate missing consumers — results persist in the ledger; a sweep collects strays |

## 6. Open questions

1. Should the batch ledger live under the session catalog's storage
   (W7) or its own dir? (Leaning own dir — batches outlive sessions.)
2. Minimum batch size worth submitting (provider minimums vs
   overhead) — tune per provider in BAT-2.
3. GLM/DeepSeek batch surfaces — verify existence/terms at pickup and
   extend adapters if the discount is real.
