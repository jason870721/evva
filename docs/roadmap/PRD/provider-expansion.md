# PRD — Provider Expansion (Gemini, enterprise gateways, conformance kit) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; re-verify each provider's current API surface at pickup —
> provider APIs move faster than anything else this roadmap touches).
> **Target release:** TBD — batch-2 wave **W26**, suggested horizon H3
> per [../long-range.md](../long-range.md) §3b. Natural companion to
> W9 (model routing) — more providers make chains meaningful.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> evva's five providers (Anthropic, DeepSeek, GLM, OpenAI, Ollama)
> cover the 2025 landscape; the 2026 gaps are Gemini (a top-tier
> model family with no evva path), the enterprise gateways (Bedrock,
> Vertex — how corporate operators are *required* to buy Claude), and
> the aggregators (OpenRouter — one key, the long tail). Each new
> provider today is a hand-rolled package with no shared test
> discipline — the wave's second half fixes that with a conformance
> kit.
> **Reference source:** none — evva-native (each provider's official
> API docs are the reference).

---

## 1. TL;DR

Two halves, deliberately shipped together:

1. **The conformance kit** (`pkg/llm/llmtest`): a reusable test suite
   any `llm.Client` implementation runs against — streaming chunk
   ordering, tool-call round-trips, multi-block messages, image blocks
   (post-W10), usage accounting, error taxonomy (the W9 retry classes),
   cancellation behavior, cache-marker tolerance. Recorded-fixture
   based (no live keys in CI), with an optional live smoke mode.
   Existing five providers adopt it first — which will surface real
   latent divergences (the audit should expect findings, and the wave
   budgets for fixing them).
2. **New providers on top of the kit:**
   - **Gemini** — evaluate at pickup: native `generateContent` client
     vs Google's OpenAI-compatibility endpoint riding the existing
     openai package with a config preset. Recommendation: try the
     compat preset first (near-zero code), promote to a native package
     only if compat gaps (thinking, caching, tool fidelity) prove
     material.
   - **AWS Bedrock** (Anthropic models via SigV4) — a transport/auth
     variant of the claude engine, not a new engine (and a forcing
     function for ARC-3's engine-dedupe, which should land with or
     before this).
   - **Google Vertex** (Anthropic models via OAuth/ADC) — same
     pattern as Bedrock.
   - **OpenRouter** — OpenAI-surface aggregator; mostly a preset +
     model-listing integration; valuable as the long-tail escape hatch
     and a W9 chain fallback.

Registry/config/docs treat "a preset of an existing engine" as a
first-class provider kind — most 2026+ providers are compatibility
surfaces, and evva should stop paying full-package cost for them.

## 2. Goals / non-goals

### Goals

- `llmtest` conformance suite + fixture recorder; all in-tree providers
  green (with documented, justified skips where a provider genuinely
  lacks a capability — skips are visible, not silent).
- Provider presets: named configurations binding an engine to an
  endpoint/auth/quirk-set (`gemini-openai-compat`, `openrouter`),
  selectable wherever providers are; presets carry capability flags
  (vision, caching, tool-call dialect) feeding `pkg/constant` and the
  W9 capability guards.
- Bedrock + Vertex auth transports for the claude engine: SigV4 and
  ADC/OAuth token flows — implemented with stdlib crypto/HTTP only if
  feasible (SigV4 is hand-rollable; ADC is a documented token file/
  metadata flow), else the dependency question escalates per policy.
- Rate-card entries + routing metadata for every new provider (cost
  tracking must not lag provider additions — `/cost` correctness is a
  ship gate).
- Contributor doc: "adding a provider" — kit-first workflow, preset vs
  package decision tree.

### Non-goals (this wave)

- Azure OpenAI (deployment-name routing quirks; follow demand),
  Mistral/Cohere native packages (OpenRouter covers evaluation-grade
  access; promote on demand).
- Embedding/vision parity for every new provider on day one —
  capability flags declare the truth; parity fills in per the W6/W10
  waves' schedules.
- Provider marketplace/plugin loading (external provider registration
  exists architecturally via the registry; packaging third-party
  providers is out of scope).
- Fine-tuning/batch endpoints (batch is W27's lane).

## 3. Design sketch

- **Kit shape:** table-driven scenarios over a `Client` + a fixture
  transport (record/replay at the HTTP layer, one fixture dir per
  provider). Scenarios assert on the neutral `llm` types (Message/
  Chunk/Usage), never on wire bytes — wire fixtures pin the wire.
  Live-smoke mode runs the same scenarios against real keys, manually
  triggered (a `make provider-smoke P=gemini` affair, never CI-default).
- **Preset mechanism:** a preset is data — engine name + base URL +
  auth style + quirk flags + capability flags + rate-card key. Audit
  decides where presets live (likely `pkg/llm/builtins` beside the
  factory registrations) and how config references them.
- **Engine-variant auth:** Bedrock/Vertex wrap the claude engine's
  transport with request-signing middlewares — which is exactly the
  ARC-2/W9 decorator shape; if ARC-3 (engine dedupe) hasn't landed,
  this wave forces the question and should absorb it rather than
  copying the engine a third time.
- **Capability truth:** every preset/provider declares its flags;
  `/model`, routing guards, and vision/CI waves consume them. Unknown
  = false, loudly.

## 4. Work items

- **PRV-1 — Conformance kit.** Scenarios, fixture transport, recorder
  tooling. *Accept:* kit runs green against the anthropic + openai
  packages from recorded fixtures; scenario list reviewed against the
  W9 retry taxonomy.
- **PRV-2 — Existing-provider adoption.** All five in-tree providers
  under the kit; divergences fixed or documented-skipped. *Accept:*
  CI runs the kit for every provider; the skip report is empty or
  justified line-by-line.
- **PRV-3 — Preset mechanism.** Data model, config surface, capability
  flags, rate-card linkage. *Accept:* a preset provider appears in
  model selection with correct flags and costs with zero new Go
  packages.
- **PRV-4 — Gemini.** Compat-preset evaluation with kit + live smoke;
  go/no-go on native package documented with evidence. *Accept:* kit
  green via compat; a written decision memo on native promotion.
- **PRV-5 — Bedrock + Vertex.** Auth transports over the (deduped)
  claude engine, region/project config, kit fixtures. *Accept:* kit
  green from fixtures; live smoke documented for both; ARC-3 status
  resolved either way.
- **PRV-6 — OpenRouter preset.** Preset + model-list handling +
  routing integration notes. *Accept:* kit green; a W9 chain with an
  OpenRouter fallback works in fixture.
- **PRV-7 — Docs + changelog.** User-guide (en + zh-tw) per provider
  setup; the contributor "adding a provider" guide.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Provider API drift between planning and pickup | header warning + PRV-4/5 start with a re-verify step; fixtures pin what we tested against |
| Kit adoption reveals painful divergences in shipped providers | that's the point — budgeted in PRV-2; divergences are bugs users already have, just invisible |
| SigV4/ADC hand-rolling hides subtle auth bugs | golden request-signing test vectors (AWS publishes them); ADC follows Google's documented flows with token-file fixtures |
| Preset sprawl (every endpoint becomes a preset PR) | presets require kit fixtures + rate-card + capability review — the bar is the test suite, not the YAML |

## 6. Open questions

1. Kit placement: exported `pkg/llm/llmtest` (embedders test their own
   clients — SDK value) vs internal? Leaning exported, Experimental
   tier until v2.0.
2. Gemini native triggers: which concrete compat gaps justify the
   package (thinking blocks? context caching? grounding)? Define the
   checklist in PRV-4 before testing, not after.
3. Bedrock/Vertex model-id mapping (provider-prefixed ids vs evva's
   constant table) — extend `pkg/constant` or preset-level aliasing?
