# PRD — Self-Healing Edits (Edit→LSP Diagnostics Sync) — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD — a wave-sized minor (claims its minor at planning
> per `CLAUDE.md` → Release workflow).
> **Roadmap source:** `CLAUDE.md` → conventions; closes a gap in the shipped
> LSP module (`pkg/tools/lsp`) + the `fs` edit/write tools.
> **Reference source:** none — evva-native (no `ref/src/` analog). Rides the
> 2026 "long-running autonomous workflows that self-correct" trend. Design
> **reuses the `fs.CheckpointSink` injection pattern** shipped in v1.8.2-beta.2.

---

## 1. TL;DR — what this phase actually is

evva already has most of a self-correction loop — it's just **not connected on
the write side.** The pieces that exist:

- A diagnostics pipeline: `DiagnosticRegistry` collects/dedupes/caps LSP
  diagnostics (`pkg/tools/lsp/diagnostics.go`), `Manager.DrainDiagnostics()`
  hands them out (`manager.go:78`), `FormatDiagnosticsReminder` renders them as
  a `<system-reminder>` block (`diagnostics.go:199`).
- A drain point: the agent drains pending diagnostics between turns
  (`internal/agent/loop.go:189-191` → `agent.go:1629 drainLSPDiagnostics` →
  `:1638` inject). The comment is the tell: *"Diagnostics are passive (not
  solicited) so they arrive between"* turns.

The hole is the word **passive**. For a language server to *publish*
diagnostics for a file, the client must tell it the file changed
(`textDocument/didOpen` / `didChange`) — or the server must be filesystem-
watching with registered watchers. **evva's `fs` edit/write tools do
neither.** `Manager.OpenFile` (didOpen, `manager.go:158`) exists but is never
called from the edit path; `Manager.NotifyFileChanged` (`:87`) only *clears*
stale diagnostics — it doesn't push the new content to trigger re-analysis;
there's **no `DidChange` method at all.** So after evva edits a file, whether
the model ever sees a resulting compile error is left to chance (a server that
happens to watch the filesystem), and on servers that don't, the agent edits
**blind** until a later `bash` build trips over it.

**This phase connects the write side.** After an `edit`/`write` mutation, the
fs tool notifies the LSP layer (didOpen-then-didChange the touched file, full
sync), so the server re-analyzes and the **already-existing** drain delivers
real, fresh diagnostics for what the agent just wrote. The wiring is the
**exact `CheckpointSink` pattern** the fs tools gained last release — a narrow
consumer-side interface, a nil-safe `WithLSPSync`, installed by the runtime.

Two tiers:

1. **Core (passive, made reliable):** edits push `didChange`; the existing
   between-turns drain now has real content to deliver. Low cost, on whenever
   LSP is active.
2. **Tightening (synchronous, opt-in):** after an edit, briefly wait for
   diagnostics on *that file* and fold them into the **edit's own tool
   result**, so the model sees its error on the same turn it made it — true
   edit→fix self-healing. Gated behind a config knob (latency cost).

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 The diagnostics pipeline (drain side — done)

- `DiagnosticRegistry` (`diagnostics.go:28`) — collect, dedupe by identity key
  (`diagKey`, `:144`), per-file + total caps (`:72,:86`), LRU dedup
  (`diagnosticKeySet`, `:160`). **No external deps** (`container/list`+map).
- `Manager.wireDiagnosticsHandler` (`manager.go:66`) already routes server
  `publishDiagnostics` → `diagRegistry.Register` (`:72`). The producer plumbing
  is live; it just has nothing to produce until the file is synced.
- `DrainDiagnostics()` (`:78`) + `FormatDiagnosticsReminder` (`:199`) +
  `drainLSPDiagnostics` (`agent.go:1629`) + the loop drain (`loop.go:189`) —
  the **entire delivery path is built and wired.** This phase adds the trigger,
  not the delivery.

### 2.2 The `CheckpointSink` injection pattern (the template)

The fs tools gained exactly this shape in v1.8.2-beta.2 — copy it verbatim:

- `pkg/tools/fs/checkpoint.go` — `CheckpointSink` interface declared
  **consumer-side** so `fs` imports neither `internal/checkpoint` nor `lsp`.
  Nil sink = feature off, zero hot-path cost (`checkpoint.go:12`).
- `EditTool.checkpoints` field + `WithCheckpoints(s)` (nil-safe)
  (`edit.go:45,55`); `capture()` called on the mutation hot path
  (`edit.go:64`). Same for `WriteTool` (`write.go:18,28,35`).
- Runtime wiring: `ToolState.SetCheckpointSink` / `CheckpointSink()`
  (`internal/toolset/toolset.go:60,253,260`), installed into the tools in
  `builtins.go:49,53` (`WithCheckpoints(ts.CheckpointSink())`), and the agent
  registers the concrete sink at `agent.go:364`
  (`a.toolState.SetCheckpointSink(mgr)`).

The LSP-sync sink is a **second instance of this exact pattern** — a parallel
interface, field, `With…`, ToolState getter/setter, builtins wiring, and agent
registration. Nothing structurally new.

### 2.3 Manager file-sync primitives (partially present)

- `OpenFile(ctx, path, content)` → `didOpen` (`manager.go:158`) — exists,
  unused from the edit path.
- `CloseFile` → `didClose` (`:190`); `NotifyFileChanged` clears diagnostics
  (`:87`); `EnsureServerStarted` lazy-starts the right server (`:110`);
  `ServerForFile` (`:96`); `languageForFile` (`:256`).
- **Missing:** a `DidChange(ctx, path, content)` that bumps a per-URI version
  and pushes `textDocument/didChange` (full sync). Task 1 adds it.

### 2.4 Config knob pattern — `pkg/config`

`EnableCheckpoints` + Get/Set (`config.go:176,371,380`) is the template for the
opt-in synchronous tier's knob (`LSPDiagnosticsOnEdit`).

---

## 3. Goal & acceptance criteria

**Goal:** after the agent edits a file, the LSP server is told, re-analyzes,
and the resulting diagnostics reliably reach the model — passively between
turns by default, or synchronously on the edit's own result when opted in — so
the agent catches its own compile/type errors instead of editing blind.

Ship is complete when **all** of these pass:

- **A1 — Edits sync to the server.** After a successful `edit` or `write` to a
  file an LSP server owns, the tool drives `didOpen` (first touch) /
  `didChange` (subsequent), full-sync, with a monotonically increasing per-URI
  version. Verified against the mock server asserting the `didChange`
  notification + payload.
- **A2 — Diagnostics then actually arrive.** With the sync in place, a
  server-published diagnostic for the edited file lands in the
  `DiagnosticRegistry` and is delivered by the existing between-turns drain
  (`loop.go:189`) as a `<system-reminder>` — where today (no sync) a
  non-watching server delivers nothing.
- **A3 — Zero cost when off / no LSP.** No LSP server configured for the repo
  (or the language) → the sink is a no-op; the edit hot path is byte-for-byte
  unchanged (mirror `CheckpointSink`'s nil-safety). Snapshot/perf guard.
- **A4 — Best-effort, never blocks the edit.** The notification is fire-and-
  forget by contract (like `CaptureBefore`): it must never block, error, or
  fail an `edit`/`write`. A dead/slow server degrades to "no diagnostics," not
  a failed edit.
- **A5 — Stale diagnostics invalidated.** On `didChange`, prior diagnostics for
  that URI are dropped (`DiagnosticRegistry.ClearFile`, `diagnostics.go:118`)
  so the model never sees a ghost error against a line the edit already fixed.
- **A6 — Synchronous tier (opt-in).** With `LSPDiagnosticsOnEdit=true`
  (default **false**), after the mutation the tool waits up to a short, bounded
  window for diagnostics on *that file* and appends them to the **edit's own
  `tools.Result`** — same-turn self-healing. The wait is context-cancellable
  and capped (e.g. ≤750ms); on timeout the result is the normal edit summary
  (diagnostics still arrive next turn via A2).
- **A7 — Synchronous tier never hangs.** The bounded wait cannot stall a
  session: deadline + cancellation, asserted by a test with a server that
  never replies.
- **A8 — Main + subagents that edit.** Unlike the repo map / output styles
  (Main-only), this is **correctness for any agent that writes code** — it
  applies wherever the `fs` edit/write tools run with an LSP manager present
  (Main and code-writing subagents). It follows the tool, not the persona.
- **A9 — Bounded output.** Synchronous diagnostics reuse the registry's
  existing per-file/total caps (`diagnostics.go:72,86`) so a flood of errors
  can't blow the edit result's size.
- **A10 — Tests.** `didChange` emission + versioning (mock server); diagnostics
  delivery round-trip (edit → register → drain); ghost-invalidation (A5);
  no-LSP no-op (A3); never-blocks (A4); synchronous wait + timeout (A6/A7).
- **A11 — Docs + version + changelog.** User-guide (en + zh-tw) note; the
  `LSPDiagnosticsOnEdit` knob; `CHANGELOG.md`; `pkg/version/version.go`.

---

## 4. Work breakdown (ordered)

### Task 1 — `Manager.DidChange` (full-sync + versioning)

`pkg/tools/lsp/manager.go`:

```go
// DidChange tells the server responsible for filePath that its content is now
// `content` (full document sync), bumping a per-URI version. Lazy-opens the
// file (didOpen) on first touch. Clears stale diagnostics for the URI so the
// next publish is authoritative. Best-effort: returns quickly, errors logged.
func (m *Manager) DidChange(ctx context.Context, filePath, content string) error
```

- Track `version int32` per URI (map under the manager mutex, or `atomic`
  per-server). First touch → `OpenFile` (didOpen, v1); thereafter
  `textDocument/didChange` with `{version, contentChanges:[{text: content}]}`
  (full sync — the fs tool already holds the whole new file; **no incremental
  diffs**, §5.2).
- Call `diagRegistry.ClearFile(uri)` (A5) before/with the change so the prior
  errors don't linger.
- No server for the file's language → return nil (no-op). Reuse
  `EnsureServerStarted` / `ServerForFile`.

### Task 2 — `fs.LSPSyncSink` interface + tool wiring

`pkg/tools/fs/lspsync.go` (new — mirror `checkpoint.go`):

```go
// LSPSyncSink is notified after the edit/write tools mutate a file, so the
// runtime's LSP layer can re-analyze it. Declared consumer-side so pkg/tools/fs
// imports neither pkg/tools/lsp nor internal/*. Nil sink disables it — zero
// hot-path cost when LSP is off.
type LSPSyncSink interface {
    // NotifyEdited reports that absPath now holds newContent. Best-effort by
    // contract: must never block or error the edit (it dispatches async).
    NotifyEdited(absPath, newContent string)
}
```

- `EditTool`/`WriteTool`: add an `lspSync LSPSyncSink` field +
  `WithLSPSync(s)` (nil-safe), exactly like `WithCheckpoints`
  (`edit.go:45,55`; `write.go:18,28`).
- Call site: **after** the successful mutation (contrast `CaptureBefore`, which
  is pre-mutation), with the bytes just written. One nil-checked line in each
  tool's success path.

### Task 3 — Runtime wiring (ToolState + agent)

- `internal/toolset/toolset.go`: `lspSyncSink fs.LSPSyncSink` field +
  `SetLSPSyncSink` / `LSPSyncSink()` (mirror `checkpointSink`, `:60,253,260`).
- `internal/toolset/builtins.go`: `…WithLSPSync(ts.LSPSyncSink())` on the
  `fs.NewEdit` / `fs.NewWrite` factories (`:49,53`).
- `internal/agent/agent.go`: where the LSP `Manager` is built (`:413`) and the
  checkpoint sink is registered (`:364`), register an **adapter** as the LSP
  sink: a tiny type wrapping `*lsp.Manager` whose `NotifyEdited(path, content)`
  dispatches `mgr.DidChange` on a short-lived goroutine (A4 — never block the
  edit). Install only when the manager exists.

### Task 4 — Synchronous tier (opt-in, A6/A7)

- `pkg/config`: `LSPDiagnosticsOnEdit bool` (default false) + Get/Set + overlay,
  mirroring `EnableCheckpoints`.
- When on, the edit/write `Execute` — after `NotifyEdited` — polls the manager
  for diagnostics on the just-edited URI under a bounded, cancellable deadline
  (≤750ms), then appends a `FormatDiagnosticsReminder`-rendered block to the
  tool `Result.Content`. This needs the tool to reach the manager for a
  *scoped* drain; add `Manager.DiagnosticsForFile(uri)` (a filtered peek that
  does **not** consume the between-turns queue, or consumes only that URI's
  entries) so the synchronous read and the passive drain don't double-deliver.
- Timeout → return the plain edit summary (A7); the diagnostics still arrive
  next turn via the passive path (A2). The synchronous tier is an
  **accelerator**, not a separate source of truth.

### Task 5 — Docs + version + changelog

- `docs/user-guide/{en,zh-tw}/user-guide.md` — "Self-healing edits": how
  edits now feed LSP diagnostics back, the passive default vs. the
  `LSPDiagnosticsOnEdit` synchronous knob, and the no-server behavior.
- `CHANGELOG.md` `### Added`/`### Fixed` (it's partly a correctness fix — the
  diagnostics pipeline was half-wired); `pkg/version/version.go`.

---

## 5. Design decisions & risks

### 5.1 — Reuse the CheckpointSink pattern exactly

The single most important decision is *not* inventing a new integration shape.
The fs tools just gained a clean, reviewed, nil-safe sink pattern for
checkpointing; the LSP-sync sink is a verbatim second instance (interface +
field + `With…` + ToolState getter/setter + builtins wiring + agent
registration). This keeps `pkg/tools/fs` free of an `lsp` import (the interface
is consumer-side, like `CheckpointSink`), keeps the cost zero when off, and
gives reviewers a pattern they already approved. **A divergent design here is
the main avoidable risk.**

### 5.2 — Full sync, not incremental (the document-drift answer)

evva's own LSP review hammered document-state drift and ghost diagnostics as
the #1 LSP integration hazard (`lsp-feedback.md`, ChatGPT §1, Gemini). The
clean answer for an *agent*: **full document sync.** The `edit`/`write` tool
already holds the entire new file content — push the whole thing with a bumped
version every time. No incremental range math, no drift between the server's
model and disk, no class of off-by-one range bugs. It's marginally more bytes
per notification and completely removes the hardest failure mode. (Servers
advertise sync capability; if one only supports incremental, fall back to
didClose+didOpen, still full content.)

### 5.3 — Best-effort, asynchronous by default

The core tier must never make an edit slower or failable (A4) — the agent's
write path is hot. So `NotifyEdited` dispatches the `didChange` on a goroutine
and returns immediately, exactly mirroring `CaptureBefore`'s "must never block
or error the edit" contract. Diagnostics arriving a few hundred ms later, on
the next turn's drain, is the correct default. Only the **opt-in** synchronous
tier (Task 4) trades latency for same-turn feedback, and even it is hard-
bounded (A7).

### 5.4 — Ghost-diagnostic invalidation is mandatory

Without A5, this feature would *create* the ghost-diagnostic problem it's meant
to avoid: edit fixes line 10, but the registry still holds the old "error on
line 10" until the server re-publishes. `ClearFile` on every `didChange`
(`diagnostics.go:118` already exists) is the fix — wire it into `DidChange` so
invalidation and re-sync are atomic from the model's view.

### 5.5 — Follows the tool, not the persona

Unlike the repo map and output styles (Main-only, user-facing), correct
diagnostics are valuable to **any** agent that writes code — including
code-writing subagents. So the sink is installed wherever the fs edit/write
tools are built with an LSP manager in scope, not gated to the Main persona
(A8). The cost guard is the same nil-safety: a subagent without an LSP manager
gets a nil sink and pays nothing.

### 5.6 — `bash`-driven edits stay out of scope (honest limitation)

Like checkpointing (which documents that `bash`-driven file changes aren't
captured), this loop only fires for the `fs` `edit`/`write` tools. A file
rewritten by a `bash` `sed`/`go fmt` won't `didChange`. Document it as a known
boundary — the same boundary the checkpoint feature already drew — rather than
trying to intercept arbitrary shell writes. (A future filesystem-watch
registration could close it, but that's a separate, larger LSP-client effort.)

---

## 6. Out of scope

- **Incremental document sync** (§5.2) — full sync only.
- **Filesystem-watch / `didChangeWatchedFiles` registration** to catch
  `bash`-driven edits (§5.6) — separate effort.
- **Auto-fixing** the diagnostics — this surfaces errors to the model; the
  model decides whether/how to fix. No code-action/quick-fix automation.
- **Build/test command integration** (run `go build` after edit) — a different,
  heavier feedback source; the `bash` tool already covers it on demand. LSP
  diagnostics are the cheap, incremental tier; a post-edit build hook is a
  possible fast-follow, not this PRD.
- **Diagnostics for read-only sessions / non-code files** — only fired by
  edit/write on LSP-owned files.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 1:** `Manager.DidChange` — didOpen-on-first-touch, didChange +
      per-URI version, full sync, `ClearFile` on change (A1/A5), against the
      mock server.
- [ ] **Task 2:** `fs.LSPSyncSink` (consumer-side) + `WithLSPSync` nil-safe on
      Edit/Write; called post-mutation with new content; no `lsp` import in
      `pkg/tools/fs`.
- [ ] **Task 3:** ToolState getter/setter + builtins wiring + agent registers
      the manager adapter (async dispatch); no-manager → nil sink (A3).
- [ ] **A2:** edit → mock server publishes → between-turns drain delivers the
      `<system-reminder>` (round-trip test).
- [ ] **A4:** dead/slow server → edit still succeeds, never blocks (test).
- [ ] **Task 4 (if shipped):** `LSPDiagnosticsOnEdit` synchronous tier appends
      diagnostics to the edit result within the bound; never-replying server →
      timeout → plain summary (A6/A7); no double-delivery with the passive
      drain.
- [ ] `go build/vet/test ./...` green.
- [ ] **Manual (TTY, in this repo):** with gopls, `edit` a `.go` file to
      introduce a type error → next turn carries an LSP diagnostic
      `<system-reminder>` pointing at the line. Fix it → the ghost error does
      not reappear (A5). Toggle `LSPDiagnosticsOnEdit` → the error shows on the
      edit's own result.

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `pkg/tools/lsp/manager.go` | Edit — `DidChange` (full sync + version + ClearFile) + optional `DiagnosticsForFile` | Task 1,4 |
| `pkg/tools/fs/lspsync.go` | **New** — `LSPSyncSink` interface (consumer-side) | Task 2 |
| `pkg/tools/fs/edit.go` | Edit — `lspSync` field, `WithLSPSync`, post-mutation `NotifyEdited` | Task 2 |
| `pkg/tools/fs/write.go` | Edit — same | Task 2 |
| `pkg/tools/fs/*_test.go` | **New/Edit** — sink fired + nil-safe | Task 2,10 |
| `internal/toolset/toolset.go` | Edit — `lspSyncSink` + Set/Get (mirror checkpointSink) | Task 3 |
| `internal/toolset/builtins.go` | Edit — `WithLSPSync(ts.LSPSyncSink())` on edit/write factories | Task 3 |
| `internal/agent/agent.go` | Edit — manager→sink adapter, async dispatch, register near `:364`/`:413` | Task 3 |
| `pkg/config/config.go` | Edit — `LSPDiagnosticsOnEdit` + Get/Set | Task 4 |
| `pkg/ui/bubbletea/components/overlays/config.go` | Edit — list the knob | Task 4 |
| `pkg/tools/lsp/*_test.go` | **New/Edit** — DidChange + sync-tier tests | Task 1,4,10 |
| `pkg/version/version.go`, `CHANGELOG.md`, user-guide en/zh-tw | Edit | Task 5 |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — `Manager.DidChange` + versioning | ~120 | 2 h |
| Task 2 — `LSPSyncSink` + edit/write wiring | ~90 | 1.5 h |
| Task 3 — ToolState + builtins + agent adapter | ~80 | 1.5 h |
| Task 4 — synchronous tier (opt-in) | ~120 | 2.5 h |
| Task 5 — docs + changelog + version | ~50 | 45 min |
| Tests | ~240 | 3 h |

Total: ~700 LOC, ~11 h focused. The core tier (Tasks 1-3) is the smaller,
higher-certainty half (~290 LOC, ~5 h) — it reuses the CheckpointSink pattern
and the existing drain. The synchronous tier (Task 4) carries the only real
design subtlety (bounded wait + no double-delivery); ship Tasks 1-3 first if
scoping tight — they alone fix the half-wired pipeline.
