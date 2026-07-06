# PRD — Model Routing & Failover (chains, role tiers, budgets, cache parity) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W9 / v1.19** per
> [../long-range.md](../long-range.md).
> **Roadmap source:** 2026-07-06 long-range planning pass. evva already has
> five providers behind one `llm.Client`, a per-model rate card
> (`pkg/constant`), a `/cost` overlay, and per-member cost metering in the
> swarm (RP-13) — all the *ingredients* of model intelligence with none of
> the *policy*: a 429 or provider outage stops the run, every role pays
> flagship prices, and only Anthropic requests are cache-optimized.
> **Reference source:** none — evva-native. (The multi-provider registry is
> the differentiator competing single-vendor harnesses can't match; this
> wave cashes it in.)

---

## 1. TL;DR

This wave turns the provider registry from a *picker* into a *router*:

1. **Failover chains.** `route: [glm/glm-4.7, deepseek/deepseek-v3,
   claude/sonnet]` — on retryable failure (429/5xx/timeout/connection),
   the wrapping client advances down the chain with the same request,
   emits a `model_failover` event, and keeps the session going. Chains are
   config-defined with sane defaults per persona.
2. **Role tiers.** Main agent, subagents (per `agent_type`), swarm members,
   and background passes (dream, compaction summaries, auto-titles) resolve
   models independently — the cheap mechanical work stops riding the
   flagship model by default.
3. **Budget rails.** Session/day budget knobs priced from the existing rate
   card: warn at threshold, then downgrade-or-pause policy (operator
   choice). Swarm reuses RP-13 metering as its enforcement input.
4. **Cache parity.** Anthropic caching shipped in v1.10; this wave adds the
   OpenAI-surface equivalent(s), measures effectiveness per provider, and
   surfaces cache-hit rates in `/cost` so routing decisions can see real
   unit economics.

Implementation shape matters for v2.0: failover/metering/budget are built
as **`llm.Client` decorators** (wrap any provider, compose in order) — this
wave establishes the middleware idiom the ARC track later standardizes.

## 2. Goals / non-goals

### Goals

- `pkg/llm/route` (or similar) decorator package: `Chain`, `Meter`,
  `Budget` wrappers over `Client`; provider-agnostic; streaming-safe
  (failover only before first token of a response; mid-stream failures
  surface as today).
- Config: named routes, per-role assignments (`main`, `subagent.<type>`,
  `swarm.member`, `background`), default chains that preserve today's
  behavior when unset (single-model route = current semantics).
- Retry taxonomy: per-provider classification of retryable vs terminal
  errors (429 w/ retry-after honored; auth errors terminal, never chained
  — failing over an invalid key to a paid fallback must not silently
  burn money without an explicit event + status-line notice).
- Budget: `budget_usd_session` / `budget_usd_day` knobs; ledger persisted
  beside the session catalog; enforcement = warn → policy action
  (`pause` | `downgrade-to-<route>` | `continue-warned`).
- `/model` and `/cost` overlays learn routes: active chain, current hop,
  failover history, cache-hit rate per provider.
- Capability guards: a chain hop that lacks a required capability (vision,
  tool count, context length) is skipped with an event, not attempted.

### Non-goals (this wave)

- Quality-based routing (choosing models by task difficulty) — EX-10/eval
  territory; this wave routes on *availability, role, and cost* only.
- Token-level cost arbitrage mid-conversation (switching providers between
  iterations of one turn is allowed only via failover, not optimization —
  transcript portability across providers has real edge cases; see §5).
- Local-model auto-fallback when offline (belongs to EX-12 evva-lite).

## 3. Design sketch

- **Decorator order:** `Budget(Meter(Chain(providers...)))` — budget sees
  metered truth; chain hides hop-switching beneath metering so costs
  attribute to the model that actually served.
- **Transcript portability:** the session history is provider-neutral
  (`llm.Message`), which is what makes chains possible at all; the audit
  pass must verify the known sharp edges — thinking blocks, provider-
  specific tool-call id formats, cache markers — and the Chain wrapper
  strips/adapts per hop (each provider client already owns its request
  building; the chain only re-targets).
- **Day ledger:** tiny jsonl of `{date, usd}` per scope; read at decorator
  construction, appended on close — no daemon, no lock contention beyond
  file-append.
- **Events:** `model_failover`, `budget_threshold`, `budget_action` join
  the event vocabulary; TUI status line shows a subtle hop indicator when
  the active model ≠ route head.

## 4. Work items

- **RTE-1 — Retry taxonomy.** Per-provider error classification table +
  tests using recorded failure fixtures. *Accept:* 429/5xx/timeouts
  classified retryable, auth/schema terminal, per provider.
- **RTE-2 — `Chain` decorator.** Advance-on-retryable, retry-after
  honoring, capability guards, pre-first-token rule, failover events.
  *Accept:* fixture chain survives a dead first hop transparently;
  mid-stream failure does NOT silently retry; auth failure stops with a
  loud event.
- **RTE-3 — `Meter` + `/cost` upgrade.** Per-hop attribution, cache-hit
  rates per provider, failover history in the overlay. *Accept:* a
  failed-over session shows both models with separate spend rows.
- **RTE-4 — `Budget` decorator + ledger.** Knobs, thresholds, actions,
  day-ledger persistence. *Accept:* crossing the session budget in a
  fixture triggers the configured action exactly once; day ledger
  accumulates across sessions.
- **RTE-5 — Role-tier resolution.** Config schema for per-role routes;
  background passes (compaction, titles, dream) resolve `background`.
  *Accept:* a session with roles configured shows different models in
  main vs subagent vs title-generation paths (recording fakes).
- **RTE-6 — Cache parity pass.** OpenAI-surface caching (and DeepSeek's
  automatic cache accounting) wired + measured; GLM checked against its
  Anthropic-compatible surface. *Accept:* cache metrics populate for at
  least two non-Anthropic providers.
- **RTE-7 — Swarm integration.** Member routes in the manifest; RP-13
  metering feeds Budget; leader sees member budget events. *Accept:*
  a member hitting its budget pauses per policy and mails the leader.
- **RTE-8 — Docs + changelog.** User-guide (en + zh-tw): route config,
  the auth-error-never-chains rule, budget policies, cache metrics.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Cross-provider transcript incompatibilities (thinking blocks, tool-id formats) | audit catalogs the edges; Chain adapts per hop; failover integration tests replay real multi-block histories through every provider's request builder |
| Silent spend on fallback hops | failover is evented + status-line visible; budget decorator is outside the chain; auth errors never chain |
| Behavior drift when a cheaper model serves a role | role tiers default to today's single-model behavior; eval harness (W4) fixtures gate the shipped defaults |
| Retry storms against a degraded provider | retry-after honored, capped attempts per hop, chain advances instead of hammering |

## 6. Open questions

1. Config surface: per-persona routes in `meta.yml` vs global routes
   referenced by name? Leaning global named routes + per-persona reference.
2. Should failover mid-*turn* (between iterations) re-head to the route's
   primary once it recovers, or stay on the fallback for session
   consistency? Leaning sticky-until-session-end with a status hint.
3. Is `downgrade-to-<route>` safe as a budget action for coding sessions,
   or should pause be the only default? Operator call at wave pickup.
