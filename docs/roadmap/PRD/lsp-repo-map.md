# PRD — LSP-backed Repo Map — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD — a wave-sized minor (claims its minor at planning
> per `CLAUDE.md` → Release workflow).
> **Roadmap source:** `CLAUDE.md` → conventions (*minimize external
> dependencies*); builds on the shipped LSP module (`pkg/tools/lsp`).
> **Reference source:** none — evva-native (no `ref/src/` analog). Rides the
> 2026 "context engineering" trend; the canonical prior art is Aider's
> tree-sitter repo map, **re-grounded on evva's existing LSP layer so it needs
> no tree-sitter dependency.**

---

## 1. TL;DR — what this phase actually is

On a cold session, evva has **no model of the codebase's shape**. It opens
with file reads, grep, and glob (the sysprompt even codifies this ladder:
*"1-direct file reads 2-grep 3-glob 4-lsp"*, `fragments.go:129`) — which means
the first several turns of any non-trivial task are spent rediscovering where
things live. The proven fix is a **repo map**: a compact, ranked overview of
the codebase's symbols (packages, types, top-level functions + signatures)
injected as context, so the model starts with the map instead of drawing it
every time.

evva is unusually well-positioned to build this **without a new dependency**.
It already ships a full LSP module (`pkg/tools/lsp`) with a live `Manager`,
lazy per-language server startup (`manager.go:110 EnsureServerStarted`), and a
`workspace/symbol` query already wired end-to-end
(`tool.go:191 executeWorkspaceSymbol`, `formatters.go:159
formatWorkspaceSymbols`, `protocol.MethodWorkspaceSymbol`). Today that power is
**reactive** — the model must *ask* via `lsp_request`. A repo map makes it
**proactive**: the same `workspace/symbol` + `document_symbols` queries,
ranked and budgeted, composed once and handed to the model up front.

This is also the exact gap evva's own LSP feasibility review flagged: that
`workspace/symbol` / `documentSymbol` return large payloads needing
*"truncation, ranking, pagination, token budgeting, semantic filtering"*
(`docs/roadmap/design/lsp-feedback.md`, ChatGPT feedback §4). The repo map
*is* that missing budgeting layer, applied to a useful end.

**Concretely:**

1. `internal/repomap` — builds a ranked, token-bounded symbol map from the
   LSP `Manager` (workspace/symbol sweep + per-file document_symbols),
   degrading to a glob+heuristic outline when no language server is present.
2. A `PromptContext.RepoMap` field + injection into `buildMainPrompt`, behind
   a config flag (default off; opt-in like `enable_checkpoints`).
3. A `repo_map` tool so the model can **zoom** — request the map for a
   specific package/path at higher detail mid-session.

The map is read-only LSP queries (no document-sync hazards), it's the **Main
agent only**, and when no server is configured it falls back gracefully rather
than failing.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 LSP Manager + symbol queries — `pkg/tools/lsp`

- `Manager` (`manager.go`) — constructed at agent start
  (`internal/agent/agent.go:413 lsp.NewManager(lspCfg.Servers, rootURI, lgr)`),
  with lazy server startup (`EnsureServerStarted`, `:110`), language detection
  (`languageForFile`, `:256`; `discovery.go`), and `Servers()` (`:240`).
  The repo map asks the Manager for symbols; it starts **no** servers the
  Manager wouldn't already start.
- `workspace/symbol` is already a first-class operation: `tool.go:135`
  dispatch, `executeWorkspaceSymbol` (`:191`) building
  `protocol.WorkspaceSymbolParams`, and `formatWorkspaceSymbols`
  (`formatters.go:159`) rendering results. The repo map **reuses these** for a
  broad sweep (empty/wildcard query → top symbols).
- `document_symbols` (`tool.go:179`) gives the per-file outline (types +
  members + signatures) the map uses for its second, detail tier.
- `LoadConfig(workdir, appHome)` (`agent.go:409`) already resolves which
  servers exist for this repo. No server config → no map → glob fallback.

### 2.2 System-prompt composition — `internal/agent/sysprompt`

`buildMainPrompt(ctx)` (`main_agent.go:50`) is an ordered `joinSections(…)` of
fragments rendered from a `PromptContext` (the same seam the memory, skills,
and output-style PRDs use). The repo map arrives as **one already-rendered
string field** on `PromptContext`; `sysprompt` imports only stdlib, so all
the LSP/IO work happens in the caller (`profiles.go`), not in `sysprompt`.
Same one-way arrow as memory/skills.

### 2.3 Config knob pattern — `pkg/config`

`EnableCheckpoints bool` + `CheckpointMaxPerSession int`
(`config.go:176-177`) with `GetEnableCheckpoints()` / `SetEnableCheckpoints()`
(`:371,:380`) is the exact template: an opt-in bool + a bound, typed
Get/Set under the lock, persisted, surfaced in the `/config` overlay. The repo
map adds `EnableRepoMap` + `RepoMapTokenBudget` the same way.

### 2.4 Daemon-managed server lifecycle — `pkg/tools/daemon`

LSP servers are already daemons (`manager.go:251 SetDaemonState`,
`:146 daemonState.Register`). The repo map triggers no new lifecycle — a
server it touches is one the `lsp_request` tool would have started anyway.

### 2.5 The diagnostics registry's budgeting pattern

`diagnostics.go` already demonstrates evva's house approach to bounding LSP
output for the context window: per-item caps, total caps, LRU dedup, all on
`container/list` + map with **no external deps** (`:160`). The repo map's
ranking/truncation mirrors this discipline (caps + a budget, stdlib only).

---

## 3. Goal & acceptance criteria

**Goal:** when enabled, a session opens with a compact, ranked, token-bounded
map of the codebase's symbols, built from the LSP layer (or a glob fallback),
and the model can zoom into any package on demand — so it stops re-deriving
structure every cold start.

Ship is complete when **all** of these pass:

- **A1 — Off by default, free when off.** With `EnableRepoMap=false` (default),
  the Main system prompt is **byte-identical** to today's (snapshot test).
  Zero LSP calls, zero cost.
- **A2 — Map builds from LSP.** With a configured server (e.g. gopls), enabling
  the map yields a structured overview: per-package, the top-ranked types and
  top-level functions **with signatures**, sourced from `workspace/symbol` +
  `document_symbols`.
- **A3 — Ranked.** Symbols are ordered by a centrality/importance heuristic
  (reference count via `find_references`, or kind + breadth as a cheaper
  proxy) so the budget spends on load-bearing symbols, not alphabetical noise.
- **A4 — Token-budgeted.** The map never exceeds `RepoMapTokenBudget`
  (default e.g. ~2k tokens). Over budget → lower-ranked symbols are dropped
  with a truncation marker (e.g. `… +37 more in pkg/foo`), never a hard cut
  mid-symbol.
- **A5 — Graceful fallback.** No language server configured/available → the
  map degrades to a glob-derived outline (top-level dirs + exported-symbol
  grep heuristic) and says so, rather than erroring or emitting nothing.
- **A6 — Injected once, re-resolved at profile build.** The map is composed at
  `mainProfile` build time and attached to `PromptContext`; switching
  profile/model rebuilds it (same machinery as memory/skills). No live prompt
  mutation.
- **A7 — `repo_map` zoom tool.** The model can call `repo_map({path:
  "internal/agent"})` to get that subtree at higher detail than the session-
  open overview (more members, full signatures), within its own budget.
- **A8 — Main agent only.** Subagents (Explore/Plan/General) get **no** map —
  they run cold for narrow tasks (mirror the output-style §5.4 rule). The
  `repo_map` tool is on the Main profile only.
- **A9 — Bounded build time.** Map construction is time-boxed (a few seconds,
  cancellable via context) so a cold gopls index on a huge repo doesn't stall
  session start — partial map + "indexing…" note beats a hung prompt
  (the lazy-start + cancellation concerns from `lsp-feedback.md`).
- **A10 — Tests.** Off-by-default snapshot (A1); map build against the mock LSP
  server (`pkg/tools/lsp/mock_server_test.go`) asserting ranking + budget;
  glob fallback with no server; `repo_map` zoom; subagent exclusion.
- **A11 — Docs + version + changelog.** User-guide (en + zh-tw) "Repo map"
  section; `docs/extending.md` note; `CHANGELOG.md`; `pkg/version/version.go`.

---

## 4. Work breakdown (ordered)

### Task 1 — `internal/repomap` package

```
internal/repomap/
├── build.go     # Build(ctx, mgr, root, budget) (string, error) — the LSP path
├── rank.go      # ranking heuristic + budget enforcement
├── fallback.go  # glob+grep outline when no server
└── *_test.go
```

```go
// Source is the narrow seam onto the LSP layer — the subset of *lsp.Manager
// the map needs. Declared here so internal/repomap doesn't widen its coupling
// to the whole Manager and stays mockable in tests.
type Source interface {
    Servers() []string
    WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error)
    DocumentSymbols(ctx context.Context, path string) ([]Symbol, error)
}

// Build sweeps workspace/symbol, groups by package/dir, ranks, and renders a
// budget-bounded map. No servers → caller uses BuildFallback instead.
func Build(ctx context.Context, src Source, root string, budget int) (string, error)

// BuildFallback derives a coarse outline from globbing the tree + an
// exported-symbol grep heuristic — no LSP required (A5).
func BuildFallback(ctx context.Context, root string, budget int) (string, error)
```

`Manager` gains thin adapters (`WorkspaceSymbols`/`DocumentSymbols`) returning
a neutral `[]Symbol` so `repomap` never imports `protocol` directly — keeps
the dependency arrow `repomap → lsp` one-way and the LSP wire types contained.

### Task 2 — Ranking + budget (`rank.go`)

- **Rank:** start cheap — order by `(kind priority, then symbol breadth)`:
  types/interfaces > top-level funcs > methods; within a kind, more-referenced
  first **if** a reference count is affordable (one `find_references` per
  top-N symbol, time-boxed), else fall back to declaration order. Document the
  heuristic; it's tunable, not load-bearing.
- **Budget:** greedy fill by rank until `RepoMapTokenBudget` (estimate tokens
  with the existing estimator if one exists, else chars/4). Truncate at
  symbol boundaries with a `… +N more` marker per group (A4). Mirror
  `diagnostics.go`'s cap discipline — stdlib only.

### Task 3 — Config + injection

- `pkg/config/config.go`: `EnableRepoMap bool` + `RepoMapTokenBudget int`
  (default ~2000), `Get/Set` mirroring `EnableCheckpoints`
  (`config.go:176,371`); add to the `/config` overlay's field list.
- `internal/agent/sysprompt`: `PromptContext.RepoMap string` +
  `repoMapSection(ctx)` (empty → no-op, like memory). Inject after the memory
  sections in `buildMainPrompt` — it's project-shape context, peer to project
  memory.
- `internal/agent/profiles.go` `mainProfile`: when `cfg.GetEnableRepoMap()`,
  build the map (LSP `Source` from the agent's `Manager`, or fallback) under a
  short context deadline (A9), log + attach to `ctx.RepoMap`. The disk-persona
  path (`mainProfileFromDiskAgent`) gets the same treatment.

### Task 4 — `repo_map` zoom tool

```
pkg/tools/lsp/repomap_tool.go   # or internal/tools — see §5.5
```

- `pkg/tools/name.go`: `REPO_MAP ToolName = "repo_map"`.
- Input: `{ "path": "<dir or package>", "detail": "overview"|"full" }`.
  `overview` = ranked signatures (session-open density); `full` = include
  members/fields, larger budget.
- `Execute` calls the same `repomap.Build` scoped to `path` (a
  `document_symbols` sweep of that subtree). Read-only, default-allow,
  concurrency-safe.
- Register on the **Main** profile only (`profiles.go`); exclude from
  `subagentProfile` (A8).

### Task 5 — Time-box + fallback wiring (A9/A5)

The one real operational seam. `Build` takes a `ctx` with a deadline
(`profiles.go` sets it). On timeout: return whatever was assembled +
an `(indexing — partial map)` note. No server for the repo's languages →
skip the LSP path entirely and call `BuildFallback`. Both paths must be safe
to call when the LSP module is disabled (no panic, empty/fallback map).

### Task 6 — Docs + version + changelog

- `docs/user-guide/{en,zh-tw}/user-guide.md` — "Repo map": what it is, the
  `EnableRepoMap`/`RepoMapTokenBudget` knobs, the `repo_map` zoom tool, the
  no-server fallback, and that it's Main-agent-only.
- `docs/extending.md` — note the new context surface beside memory/skills.
- `CHANGELOG.md` `### Added`; `pkg/version/version.go`.

---

## 5. Design decisions & risks

### 5.1 — LSP, not tree-sitter (the whole dependency argument)

Aider's repo map uses tree-sitter + a PageRank over the call graph. evva
**must not** pull in tree-sitter — the minimize-deps rule (`CLAUDE.md`) is
explicit, and evva already paid for an LSP module that answers the same
question (`workspace/symbol` *is* "what symbols exist," `find_references` *is*
the call-graph edge). Building the map on LSP reuses a sunk investment and
adds **zero** dependencies. The cost is that the map is only as good as the
available language server — which is exactly why A5's glob fallback exists for
unsupported languages.

### 5.2 — Off by default; free when off

Like `enable_checkpoints`, the map is opt-in. A1 is non-negotiable: with the
flag off, not one LSP call fires and the prompt is byte-identical. This keeps
the feature invisible until wanted and protects users on repos with no/slow
language servers from a startup tax.

### 5.3 — Ranking is tunable, not load-bearing

The first rank heuristic can be crude (kind + breadth) and still beat
alphabetical. Reference-count ranking is better but costs `find_references`
calls — time-box it to the top-N symbols, and degrade to the cheap order under
the deadline. Don't block the ship on a perfect ranker; ship the cheap one,
leave the function swappable, iterate.

### 5.4 — Cold-index realism (the operational risk)

evva's own LSP review hammered this: gopls/rust-analyzer can take seconds-to-
minutes to index a large repo, and queries during indexing return partial or
empty results (`lsp-feedback.md`, all three reviewers). The map **must** be
time-boxed (A9) and treat partial results as success-with-a-note. It must
never make session start wait on a cold index. This — not the rendering — is
the feature's real engineering.

### 5.5 — `pkg/tools/lsp` vs `internal/repomap`

Split: the **builder** is `internal/repomap` (evva-runtime composition logic,
tunable heuristics — internal). The **`repo_map` tool** can live in
`pkg/tools/lsp` (it's a thin LSP-backed tool, downstream-valuable, sits beside
`lsp_request`) — **recommended**, or `internal/tools` if the team prefers to
prove it first. The `Manager` adapters (`WorkspaceSymbols`/`DocumentSymbols`)
are public on `pkg/tools/lsp.Manager`. Note the choice in the PR.

### 5.6 — Staleness within a session

The map is a session-open snapshot; the codebase changes as the agent edits.
For v1, **don't** auto-refresh — the model reads files as it works and the map
is an orientation aid, not a source of truth. `repo_map` zoom (A7) is the
manual refresh for a subtree the agent has been changing. Auto-refresh on a
debounce is a deliberate fast-follow, not v1 scope.

---

## 6. Out of scope

- **Tree-sitter / any new parser dependency** (§5.1) — LSP + glob fallback
  only.
- **Auto-refresh on edit** (§5.6) — session-open snapshot + manual `repo_map`
  zoom for v1.
- **Call-graph visualization / PageRank** — start with kind+reference ranking;
  a graph rank is a later refinement.
- **Repo map for subagents** (A8) — Main agent only.
- **Cross-repo / monorepo multi-root maps** — single root (`rootURI`), like
  the rest of the LSP module today; multi-root is its own LSP-wide concern
  (`lsp-feedback.md` open question).

---

## 7. Verification checklist (PR gate)

- [ ] **Task 1:** `repomap.Build` sweeps workspace/symbol + document_symbols
      via the `Source` seam; `Manager` adapters return neutral `[]Symbol`.
- [ ] **Task 2:** ranking orders by kind/reference; budget truncates at symbol
      boundaries with a `… +N more` marker (A3/A4).
- [ ] **Task 3:** `EnableRepoMap`/`RepoMapTokenBudget` config + Get/Set +
      `/config` overlay; off-by-default snapshot byte-identical (A1).
- [ ] **Task 4:** `repo_map` zoom tool, Main-only, read-only/allow (A7/A8).
- [ ] **Task 5:** time-box returns partial+note on deadline (A9); no-server →
      glob fallback (A5); safe when LSP disabled.
- [ ] **A2:** map build against the mock LSP server asserts package grouping +
      signatures.
- [ ] **A8:** subagent prompts carry no map; `repo_map` absent from subagent
      profiles.
- [ ] `go build/vet/test ./...` green.
- [ ] **Manual (TTY, in this repo):** `EnableRepoMap=true` → session opens with
      a gopls-sourced map of `pkg/`/`internal/`; `repo_map({path:
      "internal/agent"})` zooms in. Disable gopls config → reopen → glob
      fallback map with the "no language server" note.

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `internal/repomap/build.go` | **New** — LSP-sourced builder | Task 1 |
| `internal/repomap/rank.go` | **New** — ranking + budget | Task 2 |
| `internal/repomap/fallback.go` | **New** — glob outline | Task 1/5 |
| `internal/repomap/*_test.go` | **New** | Task 1,2,10 |
| `pkg/tools/lsp/manager.go` | Edit — `WorkspaceSymbols`/`DocumentSymbols` adapters | Task 1 |
| `pkg/config/config.go` | Edit — `EnableRepoMap` + `RepoMapTokenBudget` + Get/Set | Task 3 |
| `pkg/ui/bubbletea/components/overlays/config.go` | Edit — list the new fields | Task 3 |
| `internal/agent/sysprompt/sysprompt.go` | Edit — `PromptContext.RepoMap` | Task 3 |
| `internal/agent/sysprompt/main_agent.go` | Edit — `repoMapSection` injection | Task 3 |
| `internal/agent/profiles.go` | Edit — build+attach map (Main + disk persona), time-boxed | Task 3,5 |
| `pkg/tools/name.go` | Edit — `REPO_MAP` constant | Task 4 |
| `pkg/tools/lsp/repomap_tool.go` | **New** — `repo_map` zoom tool | Task 4 |
| `internal/toolset/builtins.go` | Edit — `repo_map` factory | Task 4 |
| `pkg/permission` defaults | Edit — `repo_map → allow` | Task 4 |
| `pkg/version/version.go`, `CHANGELOG.md`, user-guide en/zh-tw, `docs/extending.md` | Edit | Task 6 |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — package + LSP builder + Manager adapters | ~260 | 3 h |
| Task 2 — ranking + budget | ~140 | 2.5 h |
| Task 3 — config + sysprompt injection + profile wiring | ~120 | 2 h |
| Task 4 — repo_map zoom tool | ~120 | 1.5 h |
| Task 5 — time-box + fallback wiring | ~80 | 1.5 h |
| Task 6 — docs + changelog + version | ~70 | 1 h |
| Tests | ~280 | 3 h |

Total: ~1,050 LOC, ~14-15 h focused. The rendering and config are routine
(evva has three load-Markdown/inject-context precedents); the real work is the
ranking heuristic (Task 2) and the cold-index time-boxing (Task 5) — budget
extra there, per evva's own LSP review.
