# PRD — Semantic Memory Recall (embedding index over the typed memory store)

> **Audience:** senior engineers implementing this wave.
> **Status:** ✅ **BUILT** — MEM-1..7 implemented 2026-08-01.
> **Audited:** 2026-08-01 at `dev @ e8f8089` + CTX (W5, since merged as `c02eb8e`). Audit pass per
> [../long-range.md](../long-range.md) §1 step 2 — **read §0 first.** The
> draft's stated premise turned out to be false, and one work item was
> specified backwards, so §1 below is preserved only as the historical
> concept text.
> **Target release:** **v1.18** (claimed in `CLAUDE.md` at pickup). The
> draft's tentative v1.16 slot was taken by EVAL while H1 closed.
> **Roadmap source:** 2026-07-06 long-range planning pass.
> **Reference source:** none — evva-native (builds on
> `internal/memdir` + the dream agent).

---

## 0. Audit corrections (2026-08-01)

This is the wave where the concept → build gate earned its keep hardest:
**the draft's central factual claim about how evva recalls memories was
wrong**, and building to it would have replaced a working component with a
worse one.

**1 — "recall = loading the index into the prompt" is false.**
`internal/memdir/recall/recall.go` ships a **per-turn LLM side-query**.
`FindRelevant` scans the memory headers, sends a manifest of names +
descriptions to a *dedicated cheap model* (`recallTarget` picks the cheap
tier within the already-credentialed provider), and that model selects up to
five, whose full bodies are injected as a `<system-reminder>`. Only
`MEMORY.md` loads statically. Recall has been model-driven semantic
selection since v1.4.

**2 — "blind to phrasing" is false, and the draft's own example disproves
it.** The draft argues `"the deploy pipeline"` would never match a memory
titled `"CI release flow"`. Matching those two is exactly what a language
model reading descriptions does well — it is the one retrieval problem that
needs *no* embedding. The vocabulary-mismatch motivation does not survive
contact with the shipped code.

**3 — MEM-6 is inverted, and unbuildable as written.** The draft says
cross-project knowledge is "siloed entirely: a lesson learned in repo A does
not exist in repo B", and proposes adding an opt-in global dir.
`internal/memdir/memdirpaths.go:10-13` documents the exact opposite:
*"evva diverges from ref's per-git-root keying — **one global store, no
project key**."* Every memory is already visible from every project; there
is no per-project store to bolt a global scope onto. (`ProjectKey` exists
but keys *session* storage, never memory — `internal/agent/persist.go:25`,
`pkg/agent/sessions.go:21`.) The real gap is the mirror image: memories
carry **no provenance**, so nothing records which project a lesson came
from and a search cannot be narrowed to the current one. MEM-6 is rebuilt
around that.

**4 — MEM-4 as specified would ship *less* than what exists.** The draft
proposes "recall-at-open: embed the *first* user prompt". Recall already
runs on **every user turn**, with an `alreadySurfaced` de-dup set derived
from the transcript rather than held as agent state — so compaction resets
it for free (`internal/agent/memory_recall.go:148-174`). Narrowing per-turn
recall to session-open would be a regression. MEM-4 is rebuilt as a
*pre-filter* for the existing selector instead of a replacement.

**5 — the real gap is that recall is PUSH-ONLY.** The agent decides what to
surface at turn start; the model cannot ask. If it realizes at iteration 7
that it needs the deploy notes, it must guess a filename and `read` it.
MEM-3 (`memory_search`) is therefore the wave's genuine headline — the draft
listed it third among equals.

**6 — the real cost problem is different from the stated one.** The
side-query is one extra LLM call *per user turn*, and the manifest grows
linearly with the store. At 500 memories that is latency and tokens on every
turn — a scaling and cost problem, not the relevance failure the draft
described. Embeddings still earn their place; they just pay for something
else than advertised.

**7 — no embedding support exists anywhere.** `Embed` appears nowhere under
`pkg/llm` or `internal/`. MEM-1 is genuinely greenfield, as drafted.

**8 — `internal/memdir` is stdlib-only by charter**, stated in its package
doc and restated in `recall`'s ("*it lives apart from the base
internal/memdir package because it needs llm.Client — internal/memdir is
stdlib-only by charter*"). The index's math and file IO may live in
`memdir`; anything holding an `Embedder` may not.

**9 — frontmatter is flat `key: value`, string values only, and
legacy-tolerant** (`frontmatter.go:28-61`): an unterminated or absent block
yields an empty map and the whole file as body rather than an error. Adding
a provenance key is therefore backward-compatible by construction — old
files parse to an empty value.

### What this changed

| Item | Draft | Built |
|---|---|---|
| MEM-1 | Embedder + 2 backends | unchanged — greenfield as drafted |
| MEM-2 | sidecar index | unchanged |
| MEM-3 | `memory_search`, listed third | **the headline** — closes push-only recall |
| MEM-4 | replace recall with embed-at-open | **pre-filter** the existing per-turn selector; never replaces it |
| MEM-5 | dream re-embed | unchanged |
| MEM-6 | add an opt-in global scope | **inverted** — the store is already global; add provenance + narrowing |
| MEM-7 | docs | unchanged |

The one-line lesson for later waves: this draft was written from the
*roadmap's* model of evva, not from evva. Two of its three motivating
sentences were false at the time of writing, not merely stale.

---

## 1. TL;DR (historical concept text — see §0)

> Retained as written 2026-07-06. Findings 1–3 below are the ones the audit
> refuted; they are left in place so the correction has something to point at.

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

All seven are built. MEM-4 and MEM-6 were rebuilt around the audit; the rest
land as drafted.

- **MEM-1 — `Embedder` capability + backends.** ✅ `pkg/llm/embed.go` — an
  optional capability with its own `EmbedderRegistry`, deliberately **not** a
  method on `Client`: most providers expose embeddings on a different endpoint
  with a different model list, and Anthropic exposes none, so widening
  `Client` would force every implementation (including downstream ones) to
  carry a method it cannot honor. Backends: `pkg/llm/ollama/embed.go` (local,
  no key) and `pkg/llm/openai/embed.go` (the OpenAI-compatible wire shape, so
  it serves any provider mirroring it). The hosted factory refuses to build
  without a key — a credential-less hosted embedder can only ever fail, and
  failing at construction lets the caller fall back before a session starts.
- **MEM-2 — Sidecar index + incremental rebuild.** ✅
  `internal/memdir/embedindex.go`, inside the package's stdlib-only charter:
  it owns storage and staleness, never ranking. JSONL so a bad line costs one
  row; `bufio.Scanner` buffer raised because a 1536-float vector exceeds the
  64KB default and would silently truncate the index; atomic temp-file
  rename; staleness by content hash rather than mtime, and the hash covers the
  *truncated* embed text so it describes exactly what the model saw.
- **MEM-3 — `memory_search` tool.** ✅ `internal/tools/memory/`. **Promoted to
  the wave's headline** by the audit — recall is push-only, so this is the
  only way the model can ask for a memory mid-run. Active rather than
  deferred: needing a memory is a mid-reasoning impulse, and a `tool_search`
  round-trip is the friction that stops it happening. Keyword fallback labels
  itself in the output so the model can tell "nothing matched" from "matching
  is weak here".
- **MEM-4 — Pre-filter (was: recall-at-open).** ✅ **Rebuilt** — see audit
  finding 4. Per-turn recall already runs on every turn, so replacing it with
  a session-open embed would have shipped less. Instead `Searcher.Narrow`
  bounds the manifest the existing LLM selector reads once the store passes
  `prefilterAbove` (60), preserving its judgment while fixing the cost problem
  that is actually real. Fails **open**: any failure returns nil and the full
  manifest goes through, because failing closed would silently shrink the
  model's memory with nothing reporting it. Bails out entirely when the index
  lags the store, since a partial index would drop the uncovered memories
  invisibly.
- **MEM-5 — Dream integration.** ✅ Consolidation is the one thing that
  rewrites memory bodies wholesale, so `runDream` re-syncs inline before
  surfacing its summary.
- **MEM-6 — Provenance (was: global scope).** ✅ **Inverted** — see audit
  finding 3. The store is already global, so there is no global scope to add;
  the missing capability was knowing *where a memory came from*. New memories
  carry an `origin` frontmatter key (`memdir.OriginKey`), the prompt
  substitutes the value rather than asking the model to derive it (a guessed
  key would never match, and a wrong origin makes scoped search silently
  miss), and `memory_search` gains `scope:"project"`. Unattributed memories
  are **excluded** by that scope, not included: it is asked for when a general
  answer would mislead.
- **MEM-7 — Docs + changelog.** ✅ User guide in en + zh-tw covering the
  push/pull split, the two ranking tiers, the Ollama zero-key path, the vector
  cache's disposability, provenance, and the privacy note.

### Why `embedding_provider` defaults OFF

Both other recent gates — `redaction` and the context ladder — default ON,
so the asymmetry is worth stating. Those defaults make a session safer or
cheaper for free. This one either spends money on an API or, with the hosted
backend, sends memory bodies off the machine. Neither should happen because
somebody upgraded. Keyword search needs no setup and keeps the tool useful
until the operator opts in.

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
