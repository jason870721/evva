# PRD — Tree-sitter Code Intelligence (structure without a language server) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references — and settle §6's dependency
> question — before implementation).
> **Target release:** TBD — batch-2 wave **W28**, suggested horizon H3
> per [../long-range.md](../long-range.md) §3b.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> evva's code intelligence (LSP module, repo-map) is first-class where
> a language server is installed and configured — and absent everywhere
> else. Aider's repo map proved years ago that tree-sitter-grade
> structure alone (defs/refs per file, no server) materially improves
> agent code work; every polyglot repo evva meets has tails of YAML,
> SQL, shell, or a language whose LSP the operator never set up.
> **Reference source:** none in `ref/src` — evva-native; prior art:
> aider's tree-sitter repo map, tree-sitter query ecosystem.

---

## 1. TL;DR

A **server-less structure tier** under the existing code-intel surfaces:
parse files with tree-sitter grammars, extract definitions/references/
imports via the grammar's standard `tags.scm` queries, and feed the
same consumers LSP feeds today — so the shipped `repo_map` degrades
gracefully from "LSP-backed" to "syntax-backed" instead of to nothing,
and a new `code_outline` tool gives per-file structure everywhere.

Three consumers, in value order:

1. **repo-map fallback:** repos (or subtrees) without a configured LSP
   still get a structural map — defs per file, fan-in ranking — marked
   `source: syntax` so nobody mistakes it for semantic truth.
2. **`code_outline`:** instant per-file skeleton (symbols, signatures,
   spans) the model can request instead of reading 2,000 lines to find
   one function — pairs with the context engine's read-dedup economics.
3. **Syntax-aware chunking:** anything that slices files (context
   pruning boundaries, future semantic indexing per EX-15) can cut at
   node boundaries instead of line counts.

The hard question is the dependency (§6): tree-sitter's Go bindings are
cgo. The PRD's recommendation: **vendor the C core + the top ~12
grammars behind a build tag**, keeping the pure-Go build (and Windows
cross-compilation, a shipped v1.7 promise) fully functional with the
feature compiled out — plus a documented fallback tier of regex-based
outline extractors for the pure-Go build. Operator decides at pickup.

## 2. Goals / non-goals

### Goals

- `internal/codeintel/syntax` (name per audit): grammar registry,
  parse-with-timeout, tags-query extraction producing a neutral
  `Symbol{kind, name, span, container}` model shared with (or mapped
  from) whatever the repo-map already uses — one symbol vocabulary,
  two sources.
- Language coverage v1: Go, TypeScript/JavaScript, Python, Rust, Java,
  C/C++, Ruby, PHP, Bash, YAML, JSON, SQL — chosen by grammar maturity
  and tags.scm availability.
- `repo_map` integration: per-file source selection (LSP when live,
  syntax otherwise), visible provenance in the map output, no behavior
  change where LSP already serves.
- `code_outline {path, depth?}` tool: skeleton with signatures and
  line spans, size-capped, honest about source tier.
- Incremental economics: parse results cached by `(path, mtime, size)`
  — the same key discipline as the read-dedup cache (W5); cold-parse a
  repo of 5k files in seconds, warm lookups free.
- The pure-Go story: build-tag exclusion + regex-fallback outlines for
  the six languages where regex is defensible (Go, Python, JS/TS, Java,
  Ruby, Bash) — degraded but never absent.

### Non-goals (this wave)

- References/rename/type info — definitions, outlines, and imports
  only; semantic operations remain LSP's exclusive lane (the module
  must never pretend otherwise).
- A query language for users; grammars and queries are vendored and
  curated, not user-extensible in v1.
- Embedding-based semantic code search (EX-15's lane; this wave's
  chunker is its future input).
- Editing through syntax trees (structural edit tools) — interesting,
  separate, unproven need.

## 3. Design sketch

- **Grammar packaging:** vendored grammar C sources compiled with the
  binary under the `treesitter` build tag; no runtime downloads, no
  dynamic loading (release workflow implications audited — cross-compile
  matrix must stay green; cgo + windows/arm64 is the risk to
  prototype FIRST, before committing the wave).
- **Neutral symbol model:** the audit maps the repo-map's current
  internal types; syntax extraction targets that model so the map's
  ranking/rendering is source-agnostic. Where LSP and syntax disagree
  (they will), LSP wins per file — selection is per-file, not global.
- **Timeout discipline:** tree-sitter is fast but adversarial files
  exist; per-file parse budget (~50ms) with skip-and-log, never a
  hung map build.
- **Provenance honesty:** every consumer surface (map header, outline
  footer) states the tier: `semantic (gopls)` vs `syntax (tree-sitter)`
  vs `pattern (fallback)` — the model should trust accordingly, and the
  prompt guidance says so.

## 4. Work items

- **TSI-1 — Cross-compile spike (ticket zero).** cgo + vendored
  grammars across the release matrix (incl. Windows targets). *Accept:*
  release-workflow dry-run builds all platforms with the tag on; a
  written go/no-go informs whether TSI-2+ proceed or the wave re-scopes
  to fallback-tier-only.
- **TSI-2 — Core + registry.** Parse, tags extraction, symbol model,
  cache, timeouts. *Accept:* fixture files in all 12 languages extract
  correct symbols; cache hit rate measurable; hostile fixture times out
  cleanly.
- **TSI-3 — Regex fallback tier.** Six-language outline extractors,
  shared symbol model, tier labeling. *Accept:* pure-Go build passes
  the same fixture assertions at documented reduced fidelity.
- **TSI-4 — repo-map integration.** Per-file source selection,
  provenance surfacing, ranking parity. *Accept:* a mixed repo (Go with
  gopls + Python without LSP) yields one map with both tiers labeled;
  LSP-only behavior byte-identical when everything has a server.
- **TSI-5 — `code_outline` tool.** Schema, caps, tier honesty.
  *Accept:* outline of a 2k-line fixture returns skeleton under the
  cap with correct spans.
- **TSI-6 — Docs + changelog.** User-guide (en + zh-tw): tiers, build
  variants, language table, "when to still install an LSP".

## 5. Risks

| Risk | Mitigation |
|---|---|
| cgo breaks the cross-compile matrix or Windows promise | TSI-1 is ticket zero and a formal go/no-go; the fallback tier means even "no-go" ships value |
| Binary size growth from vendored grammars | measure per grammar in TSI-1; the 12-language list is a budget, not a floor — cut to fit |
| Two sources of truth confuse consumers | one symbol model, per-file selection, mandatory provenance labels |
| Grammar/query drift upstream | vendored + pinned; updates are deliberate PRs with fixture re-runs |

## 6. Open questions

1. **The dependency decision (operator call):** vendored cgo behind a
   build tag (recommended) vs pure-Go-only regex tier (no-go outcome)
   vs a WASM runtime for grammars (heaviest, most portable — probably
   overkill).
2. Should `code_outline` merge into `read` (an `outline: true`
   parameter) instead of being a separate tool? Leaning separate —
   different mental verb, and read's contract is already rich.
3. Imports/dependency edges in the map (tags.scm supports it
   unevenly) — v1 or fast-follow?
