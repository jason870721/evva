# PRD — Semantic Memory Recall (embedding index over the typed memory store) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W6 / v1.16** per
> [../long-range.md](../long-range.md).
> **Roadmap source:** 2026-07-06 long-range planning pass. evva's typed
> memory directory (v1.4) + auto-dream consolidation (v1.8) built the
> write/maintain half of a memory system; recall is still "load the index
> file and hope the relevant entry is obvious". Retrieval-augmented recall
> is standard in every 2026 agent memory design.
> **Reference source:** none — evva-native (builds on
> `internal/memdir` + the dream agent).

---

## 1. TL;DR

evva remembers (typed memory files: user / feedback / project / reference)
and consolidates (auto-dream merges/prunes when idle), but it recalls by
**loading the index into the prompt** — fine at 30 memories, useless at
500, and blind to phrasing ("the deploy pipeline" never matches a memory
titled "CI release flow"). Cross-project knowledge is siloed entirely: a
lesson learned in repo A does not exist in repo B.

This wave adds **semantic recall**: a local embedding index over memory
bodies, a `memory_search` tool the model can call mid-run, and
**recall-at-open** — the first user prompt is embedded and the top-k
relevant memories (per-project *and* opt-in global) are injected as a
session-open context block, replacing blind full-index loading once the
store crosses a size threshold. Dream re-embeds whatever it consolidates,
so the index never drifts from the store.

No vector database. Vectors live in a sidecar file per memory dir; cosine
over a few thousand float32 rows is microseconds in pure Go. The only new
capability needed is an **embedding client**, added as a small optional
interface next to `llm.Client` with two backends: Ollama local models and
provider embedding APIs.

## 2. Goals / non-goals

### Goals

- `pkg/llm` grows an optional `Embedder` capability (constructor-level, not
  a change to `Client`): `Embed(ctx, []string) ([][]float32, error)`.
  Backends: Ollama (local, default when available) and one hosted provider;
  registry-integrated like other provider factories.
- Sidecar index: `<memdir>/.index/embeddings.jsonl` — `{name, hash, model,
  dims, vec}` per memory; rebuilt incrementally (hash of body) on session
  open and after dream runs. Corrupt/missing index degrades to today's
  behavior, never blocks a session.
- `memory_search {query, scope: project|global|both, k}` tool — returns
  ranked snippets with names, so the model can pull full files with the
  existing read path.
- Recall-at-open: embed the first user prompt, inject top-k above-threshold
  matches as a compact block (name + description + score), bounded by the
  context engine's budget (W5 dependency).
- Global scope: an opt-in shared memory dir (the store auto-dream already
  maintains) becomes searchable from any project; provenance labels say
  which project a memory came from.

### Non-goals (this wave)

- Embedding session transcripts or code (memory files only; transcript
  recall is a possible EX spike later).
- A vector DB dependency, ANN structures, or quantization — linear scan is
  the design until real stores prove too large (measure first).
- Automatic memory *writing* changes — capture rules stay as shipped; this
  wave is retrieval only.
- Cross-machine sync.

## 3. Design sketch

- **Index lifecycle:** on session open, stat memory files → hash-diff vs
  sidecar → embed only changed/new (batched); deletions drop rows. Dream's
  consolidation hook calls the same incremental rebuild. Embedding model
  name is stored per-row: a model change triggers a lazy full re-embed.
- **Scoring:** cosine similarity, floor threshold (tuned on a fixture set)
  below which recall-at-open injects nothing — silence beats noise.
- **Fallback:** no Embedder configured → `memory_search` degrades to
  substring/keyword match over names+descriptions (still useful, zero
  setup), and recall-at-open keeps today's index-load behavior. The feature
  never becomes a setup wall.
- **Injection format:** one compact block, each hit as
  `[[name]] (score, scope) — description`; the model reads full bodies via
  the normal file path only when needed. This keeps recall cheap and the
  memory files the single source of truth.

## 4. Work items

- **MEM-1 — `Embedder` capability + backends.** Interface, Ollama backend,
  one hosted backend, registry wiring, config knob for model choice.
  *Accept:* both backends round-trip a batch; absence of config yields a
  nil Embedder and no errors anywhere.
- **MEM-2 — Sidecar index + incremental rebuild.** Hash-diff, batching,
  corruption tolerance. *Accept:* touching one memory re-embeds exactly
  one row; deleting a memory drops its row; a truncated index file
  self-heals by full rebuild.
- **MEM-3 — `memory_search` tool.** Schema, ranked output, keyword
  fallback. *Accept:* paraphrased queries hit the right memory in a
  20-memory fixture where substring search fails; no-Embedder mode returns
  keyword results with a note.
- **MEM-4 — Recall-at-open.** First-prompt embedding, threshold, compact
  injection block, context-budget respect. *Accept:* relevant fixture
  memory appears in the session-open block; irrelevant prompt injects
  nothing.
- **MEM-5 — Dream integration.** Consolidation triggers incremental
  re-embed; dream's merge/prune keeps index consistent. *Accept:* a dream
  run that merges two memories leaves the index with exactly the surviving
  rows.
- **MEM-6 — Global scope.** Opt-in global dir search + provenance labels +
  config. *Accept:* a memory written in project A surfaces in project B
  under `scope: global` with its origin labeled.
- **MEM-7 — Docs + changelog.** User-guide (en + zh-tw): setup (Ollama
  zero-config path), scopes, fallback behavior, privacy note (bodies leave
  the machine only if a hosted Embedder is chosen).

## 5. Risks

| Risk | Mitigation |
|---|---|
| Hosted embedding silently exfiltrates memory bodies | default backend preference is local Ollama; hosted requires explicit config; docs state it plainly; secret-redaction (W3) already scrubbed what entered memory |
| Recall injects stale/wrong memories and skews the run | score floor + small k + descriptions-only injection; memories remain background context per the established recall contract |
| Index staleness after out-of-band edits (user hand-edits a memory file) | hash-diff on session open catches it; dream catches it between sessions |
| Embedding latency on session open | incremental + batched; worst-case cold rebuild is async — session starts with keyword fallback and upgrades when ready |

## 6. Open questions

1. Which hosted Embedder ships first (the OpenAI-compatible surface covers
   DeepSeek too — one backend may cover several registries)?
2. Should `memory_search` also rank `[[link]]`-graph neighbors of hits
   (one hop) to exploit the existing link convention? Cheap and tempting —
   audit decides if link parsing already exists in `internal/memdir`.
3. Threshold/k defaults — derive from a fixture corpus before shipping, not
   guessed constants.
