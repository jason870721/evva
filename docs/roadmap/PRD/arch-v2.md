# PRD — v2.0: SDK Freeze & Runtime Hardening (the ARC track) — Concept Draft

> **Audience:** senior engineers implementing this wave — and every
> operator/agent touching `pkg/` before it, because §3's riders land
> early on other waves.
> **Status:** proposed — **long-range concept draft** (NOT audited; each
> ARC item needs its own audit at pickup, and several deliberately ride
> earlier waves).
> **Target release:** TBD — tentative slot **W19 / v2.0** per
> [../long-range.md](../long-range.md), the roadmap's terminal wave. The
> only planned major bump: per CLAUDE.md's versioning rules, breaking
> the `pkg/` surface is "deliberate and rare" — this is the deliberate
> one, and §9.2 of the long-range doc centralizes ALL breaking desires
> here.
> **Roadmap source:** 2026-07-06 long-range planning pass +
> `docs/roadmap/evva-sdk/` (the v1.0 "harden to stable" effort this
> completes) + debts documented in `docs/architecture.md` itself.
> **Reference source:** none — evva-native.

---

## 1. TL;DR

v2.0 is a **promise, not a feature**: after it, downstream embedders get
semver discipline on `pkg/` — the SDK v2 effort made the surface *good*;
this wave makes it *committed*. The wave has two halves:

1. **The freeze:** an API audit of every `pkg/` package against the
   stability tiers (`docs/contributing/sdk-stability.md`), promoting or
   deleting everything Experimental, removing deprecated shims, and
   spending the accumulated breaking-change budget in one release with
   one migration guide.
2. **The hardening (ARC-1..10):** the known structural debts, several of
   which *ride earlier waves* (long-range §5) so W19 itself is mostly
   finishing: typed events, a middleware turn pipeline, the claude/glm
   engine dedupe, typed tool metadata, layered config, an observability
   export, and a session-store backend seam.

Guiding rule for every ARC item: **the second consumer proves the
abstraction.** Each refactor below is justified by a concrete second
consumer that already exists or ships earlier in this roadmap — none is
speculative architecture.

## 2. The ARC items

- **ARC-1 — Typed, versioned events.** `pkg/event` payloads move from
  convention to declared types with a schema version; external
  consumers (MCP-server clients, ACP bridges, the web console, webhook
  hooks) stop reverse-engineering shapes. *Second consumer:* W11's
  protocol bridges. *Rides:* W11.
- **ARC-2 — Middleware turn pipeline.** The agent loop's cross-cutting
  passes (hooks, redaction W3, metering/routing W9, context engine W5,
  eval capture W4) become ordered, composable middleware around two
  join points (per-turn, per-tool-call) instead of inline special
  cases. *Second consumer:* by W19 there are five. *Rides:* shape
  established at W9 (`llm.Client` decorators), generalized here.
- **ARC-3 — claude/glm engine dedupe.** `pkg/llm/glm` is a documented
  "self-contained copy of the claude engine with Bearer auth" — fold
  into one engine with an auth/endpoint strategy. Cheapest debt in the
  codebase; also de-risks every future Anthropic-compatible provider.
  *Rides:* any wave touching `pkg/llm` (W9 is natural).
- **ARC-4 — Typed tool-result metadata.** `tools.Result.Metadata` is
  opaque and UI-type-asserted; replace with a registered payload-type
  pattern so UIs (now four: bubbletea, lp, web, ACP) render from
  declared types. *Rides:* W10 (image payloads force the issue).
- **ARC-5 — Layered config.** Global → project (`.evva/`) → session
  precedence with one documented resolution order; ends per-wave knob
  sprawl ambiguity (every wave in this roadmap adds knobs). *Rides:*
  earliest wave that needs project-scoped config (gardener's
  `.evva/gardener.yml` and workflows' `.evva/workflows/` are the
  forcing functions — W16/W17 at the latest).
- **ARC-6 — Observability export.** Turn/tool/LLM-call spans + counters
  behind a small in-repo trace interface with an OTLP/HTTP exporter
  (hand-rolled JSON, no OTel SDK dependency — long-range §9.3 policy),
  off by default. *Second consumer:* swarm cost/doctor surfaces + any
  operator's Grafana. 
- **ARC-7 — Session-store backend seam.** Interface over session
  persistence (jsonl today; the seam permits sqlite/remote later
  without SDK breaks). *Rides:* W7 opens these files anyway.
- **ARC-8 — API audit + deprecation sweep.** Tier-by-tier review of
  every `pkg/` export: promote, deprecate-and-remove, or document.
  Includes the config-key renames queued all roadmap long (e.g. the
  dead `dangerouslyDisableSandbox` → honest name, per the sandbox
  PRD's open question). W19-proper.
- **ARC-9 — Performance pass.** Startup time, binary size, allocation
  hot paths (session append, event fan-out, TUI render) — measured
  against committed budgets in CI, not vibes. W19-proper.
- **ARC-10 — Migration guide + v2.0.0 release.** One document, every
  break enumerated with before/after; `cmd/evva` itself builds on
  `pkg/` only (the SDK-v2 north star made real — the binary as the
  reference embedder); release per the standard playbooks. W19-proper.

## 3. Riding plan (what lands before W19)

| ARC item | Rides wave | What W19 still owes |
|---|---|---|
| ARC-2 decorator shape | W9 | generalize to the turn pipeline |
| ARC-1 typed events | W11 | freeze the schema version |
| ARC-4 typed metadata | W10 | sweep remaining ad-hoc payloads |
| ARC-5 config layering | W16/W17 | freeze resolution order docs |
| ARC-7 store seam | W7 | freeze the interface |
| ARC-3 engine dedupe | opportunistic (W9 natural) | done if ridden |
| ARC-6 observability | standalone, any gap | exporter + docs |
| ARC-8/9/10 | — | the wave itself |

Rider PRs are held to a rule: **riders never break `pkg/`** — they add
seams behind existing signatures; only W19 spends breaks.

## 4. Work items & acceptance (wave-level)

Each ARC item gets its own audit-grade sub-plan at pickup; wave-level
acceptance:

- **Freeze:** `go.mod` major bumped; every `pkg/` export documented
  with a stability commitment; zero Experimental-tier packages remain
  (promoted or deleted); deprecation shims removed.
- **Compatibility proof:** `cmd/evva` and one out-of-tree reference
  embedder (a minimal example under `examples/`) build against v2.0.0
  with only migration-guide changes.
- **Behavioral proof:** the W4 eval-harness fixture suite passes
  unchanged across the v1.x → v2.0 boundary (the freeze changes
  surfaces, not behavior).
- **Performance proof:** ARC-9 budgets green in CI on all release
  platforms.
- **The guide:** every breaking change enumerated; each maps to a
  mechanical fix; nothing "see source".

## 5. Risks

| Risk | Mitigation |
|---|---|
| The freeze ships late because hardening drags | riders move the bulk early; W19 scope is ruthlessly "finish + audit + promise", and any ARC item can slip to v2.1 EXCEPT ARC-8/10 — the promise itself |
| Downstream breakage beyond the guide | the out-of-tree reference embedder is the canary; its migration diff IS the guide's test |
| Middleware pipeline over-abstracts the loop | the five-consumers rule: the pipeline only admits join points with ≥2 real consumers today, no speculative hooks |
| A mid-roadmap wave "needs" a break early | long-range §9.2 standing rule: escalate to the operator; the budget is spent once |

## 6. Open questions

1. Module path strategy for the major (`/v2` suffix per Go convention)
   — any reason to consider a rename at the same time? (Default: no,
   just `/v2`.)
2. Does the v2.0 release also promote the pack-manifest (W18) and
   workflow-spec (W16) schemas into the frozen surface, or do they
   version independently? Leaning: freeze agent-definition + pack
   manifest (they're one contract), let workflow-spec version alone.
3. LTS posture for the last v1.x: security-fix window length once v2.0
   ships? Operator call at promotion time.
