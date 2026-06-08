# PRD — Typed Memory Directory — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD (proposed `v1.6+` candidate; the roadmap's
> v1.6 "open slot" explicitly names a *"`/dream` / background-consolidation
> memory phase"* — this is that phase, scoped to the foreground pieces).
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.6 (open slot)*.
> **Reference source:** `ref/src/memdir/` (`memdir.ts`, `memoryTypes.ts`,
> `memoryScan.ts`, `findRelevantMemories.ts`, `memoryAge.ts`, `paths.ts`).

> **Refinement note (this revision).** This plan was rewritten to three
> explicit directives, all of which override the earlier draft:
>
> 1. **No backwards compatibility.** The old fixed-section memory model is
>    *deleted*, not migrated. No migration task, no `*.migrated.bak`, no
>    section-parser kept "for the migration window." A clean break.
> 2. **One global memory store.** evva's two-scope split — cross-project
>    `USER_PROFILE.md` + per-repo `projects/<key>/MEMORY.md` — collapses into
>    a **single global** directory `<appHome>/memory/`. No user/project
>    scope concept survives.
> 3. **Align with `ref/`.** Adopt ref's actual write surface: the model
>    writes typed `.md` files **directly with the `write`/`edit` tools** and
>    maintains `MEMORY.md` itself, exactly as `ref/src/memdir/memdir.ts:
>    buildMemoryLines` instructs. There is **no bespoke `remember` tool** —
>    the earlier draft's central recommendation is reversed (see §5.1). A
>    permission **write carve-out** (port of `paths.ts:isAutoMemPath`) makes
>    writes into the memory dir auto-allow, the way ref's filesystem carve-out
>    does.
>
> **Ref-fidelity pass (this revision).** Three places where the prior draft
> claimed ref-alignment it didn't actually have are now corrected and labelled
> as deliberate evva divergences where they are one:
>
> - **(a)** The static-prompt index is flagged as a **divergence from ref's
>   reloadable index channel** (ref injects `MEMORY.md` via the CLAUDE.md /
>   user-context path, `context.ts:172`, not the static system prompt). evva
>   freezes it in the static prompt for a byte-stable prefix; the freshness gap
>   is covered by recall scanning the live dir (§5.3, §6, A4).
> - **(b)** The recall selector defaults to a **Sonnet-tier** model as ref does
>   (`findRelevantMemories.ts:99` → `getDefaultSonnetModel`); a Haiku-class
>   model is demoted to an **opt-in cost lever**, not the default (Task 3, §5.5).
> - **(c)** `alreadySurfaced` is **derived from the transcript** (so compaction
>   resets it, ref `attachments.ts:2251`) and folds in ref's `readFileState`
>   guard (files the model already read this session), rather than living as
>   persistent agent state (§5.3, Task 5).

---

## 1. TL;DR — what this phase actually is

evva today has a **fixed-section, two-store** auto-memory model:

- `<appHome>/USER_PROFILE.md` — cross-project user notes; sections
  `Preferences`, `Working style`, `Recurring topics` (`memdir/section.go:24`).
- `<appHome>/projects/<key>/MEMORY.md` — per-repo notes; sections
  `Project facts`, `Decisions`, `Open issues`, `References` (`section.go:34`).
- (`<workdir>/EVVA.md` — user-authored repo conventions, **not** auto-memory.
  Out of scope here; left exactly as-is — see §7.)

The two auto stores are written by `update_user_profile` /
`update_project_memory` (`internal/tools/memory/memory.go`), each merging a
`{sections: map}` payload into a *closed* heading set via
`memdir.MergeSections`. `USER_PROFILE.md` is injected verbatim into the
system prompt; the project `MEMORY.md` injects as a one-line-per-section
`IndexSummary`.

This is safe and cache-stable, but it is a *notes file*, not a memory
**store**. Its limits are structural:

- **No individual memories.** A "memory" is a paragraph inside a fixed
  section, not an addressable unit. You can't age one, link one, or retrieve
  one.
- **No relevance retrieval.** Everything is always in the prompt or nowhere.
  There is no "surface the 3 memories relevant to *this* query" path — so the
  prompt either carries dead weight or misses context.
- **No staleness signal.** A `Decisions` entry citing `foo.go:42` looks as
  authoritative on day 200 as on day 0. ref added freshness caveats precisely
  because users reported stale `file:line` claims asserted as fact
  (`memoryAge.ts:33`).
- **A closed taxonomy + a redundant scope split.** Three user headings + four
  project headings, hand-declared in Go. ref's taxonomy is four *types*
  (`user`, `feedback`, `project`, `reference`) that each describe *many*
  files, in **one** directory.

This phase ports ref's **typed memory directory**, with the three directives
above applied: a **single global** scope `<appHome>/memory/` becomes a
*directory* of individual `.md` files (one memory per file, frontmatter
`name` / `description` / `type`) plus a `MEMORY.md` **index** that is the only
always-injected artifact. The model **writes the files itself** with the
standard `write`/`edit` tools (ref-aligned); individual memories are pulled
into a turn **on demand** by an LLM relevance side-query
(`findRelevantMemories`), wrapped with **age-based freshness caveats** when
stale.

Concretely, the deliverable is:

1. **A typed-file read layer** in `internal/memdir`: frontmatter parse, a
   `MemoryHeader` scan, `MemoryType` taxonomy, age helpers, global-dir path
   + index read — direct ports of `memoryScan.ts` / `memoryTypes.ts` /
   `memoryAge.ts` / `paths.ts` / `memdir.ts`.
2. **A relevance retriever** (`findRelevantMemories`) that runs a cheap LLM
   side-query over the scanned headers and returns the ≤5 most relevant
   memory paths — ported from `findRelevantMemories.ts`, using evva's
   existing `llm.Client`.
3. **A write surface = the standard file tools.** No new tool. The model
   creates/updates one typed `.md` file and maintains `MEMORY.md` with
   `write`/`edit`, guided by the ported prompt block (§5.1, Task 7). A
   permission **write carve-out** auto-allows writes confined to the memory
   dir (port of `paths.ts:isAutoMemPath`).
4. **Wiring**: the `MEMORY.md` index injects statically into the system
   prompt at session start (cache-stable, like `ProjectMemoryIndex` today —
   a **deliberate divergence from ref**, which injects the index via its
   reloadable CLAUDE.md/user-context channel; see §5.3); relevant individual
   memories inject **per-turn as a `<system-reminder>` message** from the agent
   loop, never into the static prompt (cache preservation — §5.3).
5. **A clean deletion** of the old model: the two `update_*` tools,
   `section.go`, the profile/project write helpers, and the `USER_PROFILE` /
   per-project-`MEMORY` constants all go. **No migration** — old files are
   left untouched on disk and simply no longer read (§5.7).

This is **not** the `/dream` background-consolidation agent, team memory, or
past-session search. Those stay out (see §7) — this phase delivers the
foreground store they would later build on.

---

## 2. Inventory — what exists today and its fate

### 2.1 `internal/memdir/` (private) — the current memory layer

| File | What it provides | Fate in this phase |
| --- | --- | --- |
| `memdir.go` | `Load(workdir, appHome, loadProjectMemory) Snapshot`; `Snapshot{WorkdirMemory, UserProfile, ProjectMemory, ProjectMemoryIndex, Warnings}`; `readMemFile` (64 KiB cap) | **Rewrite** — `Snapshot` keeps `WorkdirMemory` (EVVA.md) + `Warnings`, drops `UserProfile`/`ProjectMemory`, gains `MemoryIndex` + `MemoryDir`. `Load` reads `EVVA.md` and the global `memory/MEMORY.md` index |
| `section.go` | `UserProfileSections`, `ProjectMemorySections`, `MergeSections`, `IndexSummary`, the H2 parser | **Delete** — no sections, no migration, nothing reads it |
| `write.go` | `UserProfilePath`, `ProjectMemoryPath`, `Write{UserProfile,ProjectMemory}`, `ReadProjectMemory`, `EnsureProjectMemoryDir`, `writeAtomic` | **Delete** the profile/project helpers (memory writes now flow through the fs `write`/`edit` tools). **Keep `writeAtomic`** only if a Go-side writer still needs it — it does **not** here, so delete it too unless reused |
| `projectkey.go` | `ProjectKey(absPath)` — stable per-repo slug | **Keep verbatim** — still used by **session storage** (`internal/agent/persist.go:33`, `agent.go:1272`, `:1309`). It is **no longer used for the memory path** (memory is global now) — that is the only change in its role |

`ProjectKey` survives because session storage keys on it; do **not** delete it
when ripping out the per-project memory path. The base `memdir` package stays
**stdlib-only** (`memdir.go:15`) and **not imported by `sysprompt`** by design.
The relevance retriever breaks stdlib-only (it needs `llm.Client`), so it lives
in a **sub-package** `internal/memdir/recall` — see Task 3.

### 2.2 `internal/tools/memory/` — the current write tools

`update_user_profile` + `update_project_memory` (`memory.go`), plus the
`MemoryDiff` metadata struct (`memory.go:40`).

**All of this is deleted.** There is no replacement tool — the model writes
memory files with the standard `write`/`edit` tools (§5.1). `MemoryDiff` goes
with it: a memory write now renders as an ordinary file-write diff via the fs
tool's existing renderer (a dedicated "memory updated" affordance is a
nice-to-have follow-up, §7). `memory.Names()` (`memory.go:29`), wired into the
Main profile's `activeTools` at `profiles.go:147-148`, returns nothing — drop
the append and the package.

### 2.3 Memory load + prompt injection seam

- **Load:** the agent loads `memdir.Load(...)` once at session start
  (`internal/agent/agent.go`, ~`:348`; resume path ~`:1107`).
  `WithMemorySnapshot` (`internal/agent/options.go:230`) and
  `ResolveMainProfileAutoMem` (`profiles.go:264-265`) are the bootstrap/SDK
  entry points. Load happens **once per session** — the steady-state system
  prompt is therefore byte-stable within a session (key to §5.3).
- **Thread into prompt:** `profiles.go:173-176` copies snapshot fields into
  `PromptContext`; the disk-persona path mirrors it at `profiles.go:284-288`
  (gated by `def.OmitMemory`).
- **Render:** `internal/agent/sysprompt/main_agent.go:61-64` calls, in order,
  `memorySection("Project memory (from EVVA.md)", …)`,
  `memorySection("User profile (from USER_PROFILE.md)", …)`,
  `autoMemoryGuidanceSection(ctx)` (`:208`),
  `projectMemoryIndexSection(ctx)` (`:274`).
- **Constraint:** `sysprompt` imports only stdlib (`sysprompt.go:9`) and
  receives *strings*, never does I/O or LLM calls. So **all** new logic (scan,
  recall, index read, ensure-dir) happens in the caller (agent loop / profile
  builder); the prompt package only gains/loses a couple of string fields.
  Respect this one-way arrow.

### 2.4 The agent loop — where per-turn retrieval lives

`internal/agent/agent.go` holds the loaded snapshot and the live `llm.Client`.
The relevance side-query (one cheap completion per user turn) runs here,
*before* the main completion, and injects its result as a per-turn message.
This is the same place ref calls `findRelevantMemories`. Locate the
per-user-turn entry point (the function that takes a new user message and
assembles the request) and add the recall hook there — see Task 5. **Do not**
put it in `sysprompt` (no I/O) or in `Load` (no query available at load time).

### 2.5 The permission gate — where the write carve-out lives

`pkg/permission/decision.go:Decide` (`:47`), called from
`internal/agent/state_machine.go:464`, is the single tool-call gate. It already
has the **exact pattern** we need: `isPlanFileWrite(call, workdir)` (`:179`)
auto-allows `write`/`edit` confined to `<workdir>/.evva/plans/` via
`IsPlanFilePath` (`types.go:37`, an `filepath.Rel` containment check). The
memory carve-out is a direct sibling: `IsAutoMemPath(memDir, absPath)` +
an `isAutoMemWrite(call, memDir)` branch in `Decide` that auto-allows
`write`/`edit` confined to the global memory dir. This is the port of
`ref/src/memdir/paths.ts:isAutoMemPath` + the filesystem write carve-out it
gates. See Task 4.

### 2.6 `pkg/skill` — the disk-markdown loader template

`pkg/skill/registry.go` + `skill.go` is evva's existing "load Markdown files
from a directory into a registry" implementation. The memory scanner is the
same shape — *walk a dir, read headers, build a list* — but reads **YAML
frontmatter** instead of a title line (`registry.go:14-18` parses a `# name`
line, not frontmatter). Model the directory-walk ergonomics on `pkg/skill`;
the frontmatter parser is **new** (Task 1).

### 2.7 Reference (`ref/src/memdir/`)

| File | What it does | Port? |
| --- | --- | --- |
| `memdir.ts` | `buildMemoryLines` (the system-prompt memory block: types + what-not-to-save + how-to-save two-step + when-to-access + before-recommending + memory-vs-plans/todos); `truncateEntrypointContent` (200-line / 25 KB index cap); `ensureMemoryDirExists` | **Yes** — port `buildMemoryLines` as the prompt block (Task 7), `truncateEntrypointContent` for index injection (Task 2), `ensureMemoryDirExists` (Task 5). Drop KAIROS daily-log + TEAMMEM branches |
| `memoryTypes.ts` | `MEMORY_TYPES` + `parseMemoryType`; the `TYPES_SECTION_INDIVIDUAL` / `WHAT_NOT_TO_SAVE` / `WHEN_TO_ACCESS` / `TRUSTING_RECALL` / `MEMORY_FRONTMATTER_EXAMPLE` prompt blocks | **Yes** — port the type enum + parser (Task 1) and the **INDIVIDUAL** prompt blocks verbatim (Task 7). Use `TYPES_SECTION_INDIVIDUAL`, **not** `_COMBINED` — evva has no team scope |
| `memoryScan.ts` | `scanMemoryFiles(dir)` → `MemoryHeader[]` (frontmatter, mtime, sorted newest-first, cap 200, excludes `MEMORY.md`, never errors); `formatMemoryManifest` | **Yes** — direct port to Go (Task 1) |
| `findRelevantMemories.ts` | side-query to a **Sonnet-tier** model (`getDefaultSonnetModel`, ref `:99`) selecting ≤5 relevant files by name+description; excludes `MEMORY.md`, already-surfaced, **and files already read this session** (`readFileState`); returns `[]` on any failure | **Yes** — port using `llm.Client`, Sonnet-class default (Task 3) |
| `memoryAge.ts` | `memoryAgeDays`, `memoryAge`, `memoryFreshnessText`, `memoryFreshnessNote` | **Yes** — trivial, port verbatim (Task 1) |
| `paths.ts` | dir resolution, `isAutoMemoryEnabled`, `validateMemoryPath`, `isAutoMemPath` write carve-out; env/CCR/growthbook/team branches | **Partial** — port `isAutoMemPath` (→ permission carve-out, Task 4) + the path-safety intent; evva's gate is already `cfg.GetEnableAutoMemory()`; drop env/CCR/growthbook/team branches. **Divergence:** ref keys the dir per git-root; evva goes **pure global** per directive 2 (§5.2) |
| `teamMemPaths.ts`, `teamMemPrompts.ts` | team-directory layout + prompts | **No** — team memory is out of scope (§7) |

---

## 3. Goal & acceptance criteria

**Goal:** auto-memory becomes a **single global directory** of typed,
individually addressable `.md` files with a stable `MEMORY.md` index. The
index is always in the prompt; individual memories are surfaced on demand by a
relevance side-query and carry freshness caveats when stale. **The model
writes memory files itself** with the standard `write`/`edit` tools, guided by
the ported prompt block, with writes into the memory dir auto-allowed by a
permission carve-out. The old two-store, fixed-section model and its tools are
deleted; no migration.

Ship is complete when **all** of these pass:

- **A1 — Single global directory.** Auto-memory lives at exactly
  `<appHome>/memory/` with index `<appHome>/memory/MEMORY.md`. There is no
  `USER_PROFILE.md` read/write path and no `projects/<key>/memory` path. The
  dir is created at session start when auto-memory is on (port of
  `ensureMemoryDirExists`), so the prompt's "this directory already exists"
  claim is true.
- **A2 — Frontmatter round-trips.** A memory file with
  `---\nname: …\ndescription: …\ntype: …\n---\n<body>` parses via the scanner
  to the same `name`, `description`, `type`. Unknown/missing `type` degrades
  to `""`/none without error (legacy-tolerant, mirroring `parseMemoryType`);
  malformed frontmatter yields an empty map + full content as body, never an
  error.
- **A3 — Scan.** `ScanMemoryFiles(dir)` walks recursively for `*.md`, returns
  headers sorted newest-first, **excludes `MEMORY.md`**, caps at 200, and
  never errors on a malformed/unreadable file (it's skipped). A missing dir or
  a dir with no `.md` files returns an empty slice.
- **A4 — Index injected, bodies not.** The system prompt contains the
  `MEMORY.md` index body (truncated to the 200-line / 25 KB caps with the
  named-cap warning — evva injects the index into the *static* system prompt, a
  deliberate divergence from ref's reloadable channel, §5.3) and **does not**
  contain any individual memory file body, anywhere. Verified by a
  prompt-snapshot test asserting evva's channel (not ref's).
- **A5 — Relevance recall.** Given a populated memory dir and a user query,
  `FindRelevant(ctx, client, model, query, dir, recentTools, alreadySurfaced)`
  returns ≤5 headers whose name/description the side-query judged relevant;
  the `alreadySurfaced` set (prior recall reminders + files already read this
  session) is excluded *before* selection; `MEMORY.md` is never returned;
  selected names are filtered against the valid filename set — keyed on the
  recursive **relative path** (no hallucinated paths); an empty/irrelevant set
  returns `nil` (not an error). The selector defaults to a Sonnet-class model
  (ref parity).
- **A6 — Recall injected per-turn.** When recall returns memories, their
  bodies are injected into the turn as a single `<system-reminder>`-wrapped
  message (not the static prompt), each prefixed with its freshness note when
  >1 day old (A7). **The static system-prompt bytes are unchanged by recall**
  (cache preserved).
- **A7 — Freshness caveats.** `FreshnessText(mtime)` returns `""` for memories
  ≤1 day old and the "Verify against current code before asserting as fact"
  caveat with the correct day count for older ones (`memoryAge.ts:33` parity).
- **A8 — Write carve-out.** With auto-memory on, a `write`/`edit` whose
  `file_path` resolves **inside** `<appHome>/memory/` auto-allows (no prompt)
  in default and accept-edits modes. A `write`/`edit` that escapes the dir
  (`..`, sibling path, absolute elsewhere) does **not** get the carve-out and
  falls through to the normal gate. Plan mode still denies (the carve-out is a
  default/accept-edits affordance, not a plan-mode one — mirror
  `isPlanFileWrite`'s placement).
- **A9 — Auto-memory gate.** With `cfg.GetEnableAutoMemory() == false`: no
  memory dir is created, no scan/recall runs, the prompt carries no memory
  guidance section and no index, and the write carve-out is inactive
  (writes into a memory path get no special treatment).
- **A10 — Old model deleted.** `update_user_profile`, `update_project_memory`,
  `MemoryDiff`, `section.go`, the profile/project write helpers, and the
  `UPDATE_USER_PROFILE` / `UPDATE_PROJECT_MEMORY` tool-name constants are gone.
  `go build ./...` is green with no dangling references. Old on-disk
  `USER_PROFILE.md` / `projects/<key>/MEMORY.md` files are **not** read,
  written, or deleted by evva (left for the user to remove manually).
- **A11 — Prompt content.** The system-prompt memory block teaches: the four
  types + when to save each; the `write`/`edit` file-write + `MEMORY.md`
  two-step; the **index vs. file** model; verify-before-citing; "use sparingly
  / persists across sessions"; the path of the global dir. Ported from
  `buildMemoryLines` + `TYPES_SECTION_INDIVIDUAL` (INDIVIDUAL variant).
- **A12 — Tests.** Unit coverage for frontmatter parse, scan ordering/cap/
  exclusion, type parse, age helpers, index read + truncation, and the
  carve-out path check (`IsAutoMemPath`). A recall test with a **fake
  `llm.Client`** asserts the manifest prompt shape and path-filtering (no live
  model; a forced client error returns `nil`).
- **A13 — Docs + version + changelog.** Update `docs/extending.md` memory
  notes; user-guide en + zh-tw memory section; `CHANGELOG.md` block
  (Added/Changed/Removed); `pkg/version.Version` bump; the
  `cmd/evva/main.go:78` startup notice (currently names `USER_PROFILE.md` /
  `projects/<key>/MEMORY.md`).

---

## 4. Work breakdown (ordered)

### Task 1 — Frontmatter + types + age + scan + global paths (`internal/memdir`)

New files in the base package (still stdlib-only):

```
internal/memdir/
├── frontmatter.go   # ParseFrontmatter(content) (map[string]string, body)
├── memtype.go       # MemoryType, MemoryTypes, ParseMemoryType
├── age.go           # AgeDays, Age, FreshnessText, FreshnessNote (port memoryAge.ts)
├── scan.go          # MemoryHeader, ScanMemoryFiles, FormatManifest (port memoryScan.ts)
└── memdirpaths.go   # MemoryDir(appHome), MemoryIndexPath(appHome), EnsureMemoryDir, IsInMemoryDir
```

**`frontmatter.go`** — a minimal frontmatter reader: if the file starts with a
`---` line, read until the next `---`, parse flat `key: value` lines (string
values only; tolerate a nested `metadata:` block by flattening `type:` if
present, but the ported `MEMORY_FRONTMATTER_EXAMPLE` uses flat `type:` —
support flat first). Return the remaining body. **No YAML dependency** — a
~40-line hand parser keeps `memdir`'s stdlib-only charter. Reject nothing:
malformed frontmatter yields an empty map + full content as body (A2).

**`memtype.go`** — port `memoryTypes.ts:14-31`:

```go
type MemoryType string

const (
    TypeUser      MemoryType = "user"
    TypeFeedback  MemoryType = "feedback"
    TypeProject   MemoryType = "project"
    TypeReference MemoryType = "reference"
)

var MemoryTypes = []MemoryType{TypeUser, TypeFeedback, TypeProject, TypeReference}

// ParseMemoryType returns ("", false) for unknown/missing — files without a
// type: keep working (parseMemoryType parity).
func ParseMemoryType(raw string) (MemoryType, bool) { … }
```

**`age.go`** — verbatim port of `memoryAge.ts` (clamp negatives to 0; "today"
/ "yesterday" / "N days ago"; freshness caveat empty for ≤1 day). Pure
functions; copy the wording of `memoryFreshnessText` exactly — it was tuned
against real stale-memory incidents.

**`scan.go`** — port `memoryScan.ts`:

```go
type MemoryHeader struct {
    Filename    string       // path relative to the memory dir
    Path        string       // absolute
    ModTime     time.Time
    Description string
    Type        MemoryType   // "" when absent
}

const MaxMemoryFiles = 200
const FrontmatterMaxLines = 30

// ScanMemoryFiles walks dir recursively for *.md (excluding MEMORY.md), reads
// the first FrontmatterMaxLines of each for frontmatter, returns headers
// sorted newest-first, capped at MaxMemoryFiles. Never errors: an unreadable/
// malformed file is skipped. Missing dir → empty slice.
func ScanMemoryFiles(dir string) []MemoryHeader { … }

// FormatManifest renders "- [type] filename (RFC3339): description" lines, one
// per header (formatMemoryManifest parity).
func FormatManifest(hs []MemoryHeader) string { … }
```

Read only the header window (cap the read at `FrontmatterMaxLines`, reuse the
`io.LimitReader` discipline from `readMemFile`); never slurp full bodies during
scan.

> **`Filename` is the recursive relative path.** `ScanMemoryFiles` walks the
> dir recursively, so a nested file's `Filename` is e.g. `sub/foo.md` — and
> that *same* relative-path string is the key in the manifest, in the recall
> selector's valid-filename set, and in the `alreadySurfaced` de-dup. Use one
> key everywhere; it is the real hallucinated-path safety net (Task 3). Don't
> mix `filepath.Base` in one place and the relative path in another.

**`memdirpaths.go`** — the single global dir + safety:

```go
const MemoryDirName   = "memory"
const MemoryIndexFile = "MEMORY.md"

// MemoryDir returns <appHome>/memory (the one global store). No project key.
func MemoryDir(appHome string) string

// MemoryIndexPath returns <appHome>/memory/MEMORY.md.
func MemoryIndexPath(appHome string) string

// EnsureMemoryDir mkdir -p's the memory dir. Idempotent; called once at
// session start so the model can write without checking existence
// (ensureMemoryDirExists parity). Never fatal — log + continue on failure.
func EnsureMemoryDir(appHome string) error

// IsInMemoryDir reports whether absPath is confined within MemoryDir(appHome)
// (filepath.Rel containment, same shape as permission.IsPlanFilePath). Used by
// the permission carve-out. Empty appHome / non-confined path → false.
func IsInMemoryDir(appHome, absPath string) bool
```

> **Note:** there is no `Slug` helper and no Go-side memory writer — the model
> names and writes files itself (§5.1). `memdirpaths.go` is read/locate/ensure
> only.

### Task 2 — Index read + truncation (`internal/memdir`)

`MEMORY.md` is the always-injected index, **maintained by the model** (it adds
a `- [Title](file.md) — hook` line when it writes a memory, per the prompt).
Go only **reads** it. Add to `internal/memdir/index.go`:

```go
const MaxIndexLines = 200
const MaxIndexBytes = 25_000

// ReadIndex returns the MEMORY.md body for the memory dir ("" if absent),
// then truncated to the line+byte caps with a named-cap warning appended.
// Port of truncateEntrypointContent (memdir.ts:57).
func ReadIndex(appHome string) (body string, warning string)
```

> **No `UpsertIndexLine` / `RebuildIndex`.** The earlier draft had Go maintain
> the index; in the ref-aligned design the **model** maintains `MEMORY.md`
> with `edit`/`write` as part of its two-step save. Go writing the index would
> fight the model for ownership of the same file. Keep Go read-only here.

> **Why an index at all, when we also scan?** The index is *static and small*
> — it injects into the system prompt at session start cheaply and tells the
> model *what it knows* without paying per-file cost (exactly what
> `ProjectMemoryIndex` does today). The scan + recall path is *dynamic* — it
> pulls the **bodies** of the few relevant files into a single turn.
> Index = "table of contents always visible"; recall = "open the 3 relevant
> chapters for this question."

### Task 3 — Relevance retriever (`internal/memdir/recall`)

New **sub-package** (depends on `llm.Client`, so it can't live in the
stdlib-only base package):

```
internal/memdir/recall/
├── recall.go       # FindRelevant(...) — port findRelevantMemories.ts
└── recall_test.go  # fake llm.Client; manifest-shape + filtering tests
```

```go
// FindRelevant scans the memory dir, asks a Sonnet-class side-query model to
// select the memories whose name/description are clearly useful for `query`
// (≤5), and returns
// their headers (path + mtime), newest-first. MEMORY.md is never a candidate
// (already in the prompt). `alreadySurfaced` filters out paths the caller has
// already put in context — both prior recall reminders AND files the model
// read directly this session (ref's readFileState guard) — so the 5-slot
// budget spends on genuinely fresh files.
//
// Never errors out of band: a model failure / context cancel / parse failure
// returns nil, so a recall hiccup degrades to "no extra memories this turn"
// (findRelevantMemories.ts catch parity).
func FindRelevant(
    ctx context.Context,
    client llm.Client,
    model constant.Model,
    query string,
    dir string,
    recentTools []string,
    alreadySurfaced map[string]bool,
) []memdir.MemoryHeader
```

Port `SELECT_MEMORIES_SYSTEM_PROMPT` from `findRelevantMemories.ts:18`
verbatim (it encodes hard-won discipline — "be selective", "don't re-surface
usage docs for tools already in use, DO surface gotchas"). Send the
`FormatManifest` output (plus a `Recently used tools:` line when non-empty) as
the user message. Constrain the model to a small JSON object
`{"selected_memories": ["file.md", …]}`; **filter the result against the valid
filename set** before returning (the model can hallucinate names — this is the
real safety net).

**Model choice:** ref uses a **Sonnet-tier** model
(`findRelevantMemories.ts:99` → `getDefaultSonnetModel`), and the selector's
discipline is tuned for that judgment — "be selective and discerning" plus the
subtle "surface gotchas but *not* usage docs for tools already in use" rule. A
weaker model over- or under-selects exactly where it matters, so **default
`cfg.MemoryRecallModel` to evva's Sonnet-class constant** (falling back to
`DefaultModel` when unset). A Haiku-class value is an explicit **opt-in cost
lever**, not the default. Cap `max_tokens` at ~256. One call per user turn.

> **Provider portability:** ref uses Anthropic structured `output_format`. Not
> every evva provider supports JSON-schema-constrained output. Make the
> retriever tolerant: request JSON in the prompt, parse defensively, and on
> any parse failure return `nil`. The `selected ⊆ validFilenames` filter is the
> safety net regardless of provider.

### Task 4 — Permission write carve-out (`pkg/permission` + agent wiring)

The model writes memory files with `write`/`edit`. Without help, every such
write hits the default-mode "ask". Port `paths.ts:isAutoMemPath` as a gate
carve-out, modeled exactly on the existing plan-file carve-out:

1. **`internal/memdir/memdirpaths.go:IsInMemoryDir`** (Task 1) — the
   containment check.
2. **`pkg/permission/decision.go`** — add an `isAutoMemWrite(call, memDir)`
   branch. `Decide` gains a `memDir string` parameter (sibling to `workdir`).
   Placement: in the **default/accept-edits** auto-allow region (after the
   read-only safelist, alongside the accept-edits write branch), **not** in the
   plan-mode block — plan mode must keep denying writes (A8). Pattern:

   ```go
   if memDir != "" && isAutoMemWrite(call, memDir) {
       return Decision{Behavior: BehaviorAllow, Reason: "auto-memory dir write"}
   }
   ```

   `isAutoMemWrite` mirrors `isPlanFileWrite` (`decision.go:179`): only
   `write`/`edit`, parse `file_path`, return `memdir.IsInMemoryDir`-equivalent.
   To keep `pkg/permission` free of an `internal/memdir` import, either pass
   the resolved `memDir` and reuse a local `filepath.Rel` containment helper
   (the package already has `IsPlanFilePath`'s logic to factor) **or** add a
   `permission.IsAutoMemPath(memDir, absPath)` next to `IsPlanFilePath` in
   `types.go`. Prefer the latter — it keeps both carve-out checks
   single-sourced in `permission`.
3. **`internal/agent/state_machine.go:464`** — thread the memory dir into the
   `Decide` call (empty string when auto-memory is off, so the carve-out is
   inert — A9). Resolve it once (`memdir.MemoryDir(cfg.AppHome)` when
   `cfg.GetEnableAutoMemory()`).

> **Security (port of `paths.ts:validateMemoryPath` intent):** the carve-out
> fires only for paths **provably confined** to the memory dir via
> `filepath.Rel` (rejects `..`, siblings, absolute-elsewhere). It does **not**
> grant the model write access anywhere else. The memory dir is a single fixed
> location under `appHome`, not user-configurable in this phase (drop ref's
> `autoMemoryDirectory` setting branch), which removes the malicious-repo
> attack surface ref's comment warns about.

### Task 5 — Wire load, ensure-dir, index injection, and per-turn recall

**5.1 Snapshot.** Rewrite `memdir.Snapshot` (`memdir.go:46`):

```go
type Snapshot struct {
    WorkdirMemory string   // <workdir>/EVVA.md (user-authored; unchanged)
    MemoryIndex   string   // <appHome>/memory/MEMORY.md body (truncated)
    MemoryDir     string   // absolute <appHome>/memory when auto-memory on + dir exists; "" otherwise
    Warnings      []string
}
```

`Load(workdir, appHome string, autoMemory bool)`: read `EVVA.md` always; when
`autoMemory`, call `EnsureMemoryDir`, set `MemoryDir`, and `ReadIndex` into
`MemoryIndex`. Drop the `UserProfile` / `ProjectMemory` / `ProjectMemoryIndex`
fields and reads.

**5.2 Prompt injection (static, cache-stable).** In `profiles.go:173-176` (and
the disk-persona mirror `:284-288`): keep `ctx.WorkdirMemory`; replace the
`UserProfile` / `ProjectMemoryIndex` copies with `ctx.MemoryIndex =
mem.MemoryIndex`. In `sysprompt/main_agent.go`:

- Delete the `memorySection("User profile …", ctx.UserProfile)` call
  (`:62`).
- Keep `memorySection("Project memory (from EVVA.md)", ctx.WorkdirMemory)`
  (`:61`) — EVVA.md is unchanged.
- Rewrite `autoMemoryGuidanceSection` (`:208`) to the ported typed-memory block
  (Task 7).
- Replace `projectMemoryIndexSection` (`:274`) with a `memoryIndexSection` that
  renders `ctx.MemoryIndex` under a `# Memory index (from <APP_HOME>/memory/MEMORY.md)`
  header, gated on `ctx.EnableAutoMemory`.

Update `sysprompt.go`'s `PromptContext` (`:68`) field accordingly
(`ProjectMemoryIndex` → `MemoryIndex`). **No new I/O in sysprompt** — it still
just formats strings.

**5.3 Per-turn recall (dynamic).** In the agent loop's per-user-turn assembly
(§2.4), when `cfg.GetEnableAutoMemory()` and `memSnap.MemoryDir != ""`:

1. build the `alreadySurfaced` set by **scanning the live transcript** for
   prior recall reminders (ref derives it from messages, not agent state —
   `attachments.ts:collectSurfacedMemories`, `:2251`), and fold in paths the
   model already opened via the read tool this session (ref's `readFileState`
   guard, `attachments.ts:2233`);
2. call `recall.FindRelevant(ctx, a.client, recallModel, userMsg,
   memSnap.MemoryDir, recentTools, alreadySurfaced)`;
3. for each returned header, read its body and prefix `FreshnessNote(mtime)`
   when stale (A7);
4. inject the concatenation as **one** `<system-reminder>`-wrapped message
   appended to the turn (mirror how evva already injects per-turn reminders).

> **Derive `alreadySurfaced`, don't store it.** The earlier draft kept a
> persistent `a.surfacedMemories` set on the agent. Don't — **compaction would
> not reset it**: after a compact the recalled bodies are gone from context,
> but a stateful set would still suppress re-surfacing them, starving the model
> of memories it can no longer see. ref scans the transcript precisely so
> compact resets the de-dup for free (`attachments.ts:2247-2249`). If you cache
> it for speed, clear it on the compaction hook.

> **Cache invariant (critical):** recalled bodies go in a **message**, never in
> the system prompt. The system prompt (with the static index, frozen at
> session start) must be byte-identical turn to turn so the provider
> prefix-cache keeps hitting. A4 + A6 lock this in. This is the single most
> important design constraint in the phase — getting it wrong silently doubles
> token cost. (Within a session, a memory the model writes mid-session is
> visible to it immediately — it authored the file and can read it back — but
> its `MEMORY.md` index line only appears in the *static* prompt next session.
> That index-line staleness is covered by recall, which scans the **live** dir
> and can re-surface the just-written file the same session (§6); it is the
> price of cache stability — §5.3.)

### Task 6 — Delete the old model

- **`internal/tools/memory/`** — delete the package (both tools, `MemoryDiff`,
  helpers).
- **`internal/agent/profiles.go:147-148`** — drop the `memory.Names()` append
  and the `internal/tools/memory` import.
- **`pkg/tools/name.go`** — delete `UPDATE_USER_PROFILE` /
  `UPDATE_PROJECT_MEMORY` constants and their `Names()`/catalog entries.
- **`pkg/toolset/tags.go`** — drop their tag rows (add none — no new tool).
- **`internal/memdir/section.go`** — delete.
- **`internal/memdir/write.go`** — delete the profile/project helpers (keep
  nothing unless `writeAtomic` gains a new consumer; it does not — `grep -rn
  writeAtomic` to confirm no other importer before removing).
- Any TUI renderer keyed on `memory.MemoryDiff` — remove (memory writes now
  render as fs file diffs).

Verify subagents never had these tools (they didn't) and don't gain the
carve-out beyond what the Main loop wires.

### Task 7 — Prompt content (the typed-memory guidance)

Rewrite `autoMemoryGuidanceSection` (`main_agent.go:208`) by porting
`ref/src/memdir/memdir.ts:buildMemoryLines` (the **non-skipIndex** two-step
variant) composed from `ref/src/memdir/memoryTypes.ts`:

- intro ("You have a persistent, file-based memory system at `<memoryDir>`.
  This directory already exists — write to it directly with the Write tool…");
- `TYPES_SECTION_INDIVIDUAL` (the four `<type>` blocks — **INDIVIDUAL**, no
  `<scope>` tags);
- `WHAT_NOT_TO_SAVE_SECTION`;
- the two-step **How to save** (Step 1: write the `.md` file with the
  `MEMORY_FRONTMATTER_EXAMPLE`; Step 2: add the `- [Title](file.md) — hook`
  line to `MEMORY.md`);
- `WHEN_TO_ACCESS_SECTION` (incl. the drift caveat + the "ignore memory"
  bullet);
- `TRUSTING_RECALL_SECTION` ("Before recommending from memory");
- the "Memory and other forms of persistence" (plan/todo vs memory) block.

This is the highest-leverage prose in the phase — the model's entire memory
behavior derives from it. Port the wording verbatim where reasonable
(CLAUDE.md convention); substitute evva's tool names (`write`/`edit`,
`grep`/`read`) and the literal `<appHome>/memory/` path. Gate the whole block
on `ctx.EnableAutoMemory`.

### Task 8 — Docs + version + changelog

- `docs/extending.md` — rewrite the memory description (single global dir,
  typed files, types, recall, the write carve-out).
- `docs/user-guide/en/user-guide.md` + `zh-tw` — memory section: where files
  live (`<appHome>/memory/`), how relevance recall surfaces them, the freshness
  caveat, that the old two-file model is gone.
- `CHANGELOG.md` — `### Added` (typed global memory dir, relevance recall,
  freshness caveats, write carve-out), `### Changed` (model writes memory files
  directly via write/edit), `### Removed` (`update_user_profile`,
  `update_project_memory`, `USER_PROFILE.md` + per-project `MEMORY.md`,
  fixed-section model — **no migration**).
- `pkg/version/version.go` — bump.
- `cmd/evva/main.go:78` — update the startup notice (drop `USER_PROFILE.md` /
  `projects/<key>/MEMORY.md`; point at `<appHome>/memory/`).

---

## 5. Design decisions & risks (read before coding)

### 5.1 — Write surface = the standard file tools (reversed from the earlier draft)

The earlier draft recommended a bespoke `remember` tool. **This revision
reverses that** per directive 3 (align with `ref/`). ref's main agent **writes
memory files with the standard file tools**, guided by the long prompt block
(`memdir.ts:buildMemoryLines`); there is no memory tool. evva adopts the same:

- **Fidelity to ref.** The prompt block, the two-step save, the index
  ownership, the recall contract — all assume the model owns the files. Porting
  the prompt but bolting a structured tool underneath it would diverge from the
  trained-behavior shape the project explicitly chases (CLAUDE.md → Vision:
  "ports tool descriptions verbatim … so the model sees prompts close to what
  it was trained on").
- **Flexibility.** The model can freely create, update, restructure, split, and
  delete memories — and maintain the index — with tools it already has. No
  bespoke schema to evolve as the taxonomy changes.
- **Simplicity.** No new tool package, no `MemoryDiff`, no schema, no Go-side
  slug/index writer. The net code is *smaller* than the old two-tool model.

**The tradeoff** is the safety the structured tool would have centralized:
malformed frontmatter, a clobbered index, or a write escaping the dir. Each is
mitigated without a tool:

- *Escape:* the permission carve-out (Task 4) is confinement-checked
  (`filepath.Rel`); a write outside the dir simply doesn't get auto-allowed.
- *Malformed frontmatter / forgotten index:* the scanner is legacy-tolerant
  (A2/A3 — a bad file is skipped, never fatal), and the prompt block is the
  same eval-tuned text ref ships. Index drift is self-healing next time the
  model edits `MEMORY.md`.

If a future phase wants the structured-write ergonomics back, it can add a
`remember` tool *over* this layer without changing the file format — but that
is a deliberate later choice, not the v1 default.

### 5.2 — One global store (collapse the two scopes; diverge from ref's per-project key)

Directive 2: a **single global** directory `<appHome>/memory/`, no user/project
split. This is a double divergence, both intentional:

- **From evva today:** the cross-project `USER_PROFILE.md` and the per-repo
  `projects/<key>/MEMORY.md` merge into one store. The `scope` concept, the
  closed section sets, and `ProjectKey`-for-memory all disappear (`ProjectKey`
  itself stays for session storage — §2.1).
- **From ref:** ref keys its memory dir on the git root
  (`paths.ts:getAutoMemPath` → `<base>/projects/<sanitized-git-root>/memory/`).
  evva goes **pure global** instead, per the directive.

**Tradeoff:** with a global store, a memory written in repo A is a candidate
for recall in repo B. For a single-user terminal agent this is usually
*desirable* — user preferences, feedback, and external references are not
repo-bound — and the relevance side-query filters by query anyway, so
irrelevant cross-repo memories don't get surfaced. `project`-type memories that
genuinely only matter in one repo are the weak case; the `description` field
(which the selector reads) should name the repo when that matters, and the
selector's "be selective" discipline handles the rest. If per-repo isolation is
later required, reintroducing a project-keyed dir is additive (a second dir fed
to the same recall) and need not break the global one.

### 5.3 — Cache discipline, and the divergence from ref it forces

**The divergence (label it, don't bury it).** ref keeps its *static*
system-prompt memory section purely behavioral (`buildMemoryLines`, no index
body — see the comment at `memdir.ts:196-197`) and injects the `MEMORY.md`
index through its **reloadable CLAUDE.md / user-context channel**
(`context.ts:172` → `getMemoryFiles`), alongside recalled bodies as
`relevant_memories` attachments (`attachments.ts:2241`). Both the index and the
recalled bodies therefore ride a channel ref can refresh — it clears the cache
on compaction and eager-reloads. **evva diverges:** it freezes the index in the
*static* system prompt at session start, modeled on today's `ProjectMemoryIndex`.
This is the same honest trade as §5.2 — we gain a byte-stable prompt prefix (a
simpler cache story, no per-turn index re-read) and pay with a same-session-stale
index. Recall closes the freshness gap because `FindRelevant` scans the **live**
dir, so a file written earlier in the session is already a recall candidate (§6).

**Why freeze, then.** The static prompt (identity + index) must be byte-stable
across turns; recalled bodies ride in a per-turn message. The index is frozen at
session start (Load runs once), so the prompt prefix never changes mid-session.
Putting recalled bodies — or a re-read index — into the system prompt would
re-cost the entire prefix every turn. A4/A6 lock this in. If a reviewer sees
recalled memory text or a per-turn index re-read flowing into `PromptContext`,
that's the bug.

### 5.4 — Recall must never break a turn

`FindRelevant` returns `nil` on *any* failure (model error, cancel, parse
failure, empty dir). A flaky side-query degrades to "no bonus context this
turn" — it never errors the user's actual request. This mirrors the ref `catch`
that returns `[]`. Tested with a fake client that errors (A12).

### 5.5 — The side-query has a cost

One extra completion per user turn. Mitigations: `max_tokens ≤ 256`, skip
entirely when the memory dir is empty (common for new users), and the
`alreadySurfaced` filter so we don't re-pay for re-selecting the same files.
The selector defaults to a Sonnet-class model for selection quality (Task 3);
a Haiku-class `cfg.MemoryRecallModel` is the opt-in cost lever for users who
accept rougher selection. Ship a config toggle `enable_memory_recall` (default
on when auto-memory is on) so cost-sensitive users can keep the index but drop
the per-turn query entirely — four lines and a real escape hatch.

### 5.6 — Frontmatter parser: hand-rolled, not a YAML dep

`internal/memdir` is stdlib-only by charter (`memdir.go:15`). Memory
frontmatter is flat `key: value`. A ~40-line parser keeps the charter; a YAML
dep would be the camel's nose. Revisit only if a future memory needs structured
frontmatter.

### 5.7 — No backwards compatibility (clean break)

Directive 1. There is **no migration** of `USER_PROFILE.md` /
`projects/<key>/MEMORY.md` into the new layout. Those files are simply no longer
read or written; evva leaves them on disk untouched (deleting user data
silently would be worse than orphaning it). The taxonomy mapping a migration
would have needed (sections → types) is judgment-laden and not worth the code
for a young tool with few users; a user who wants their old notes can paste the
relevant bits into a new memory and let the model file them. The CHANGELOG's
`### Removed` block documents the break so it isn't a surprise.

### 5.8 — The write carve-out is the trust boundary

Because the model writes files freely, the carve-out (Task 4) is what keeps
that bounded. It must be a strict containment check (`filepath.Rel`, reject
`..`/siblings/absolute-elsewhere), fire only for `write`/`edit`, and stay out
of plan mode. It auto-allows writes *only* inside the one fixed memory dir; the
memory dir is not user-configurable this phase (drops ref's
`autoMemoryDirectory` setting and its malicious-repo attack surface). Tests
A8/A12 cover the confinement and the negative cases.

### 5.9 — Why this isn't a `pkg/` package

Memory is evva-runtime-specific (knows `*config.Config`, `appHome`, evva's
prompt seams). It stays under `internal/`. The recall sub-package depends on
`llm.Client` (a public seam) but is itself internal — downstream SDK hosts get
the agent's memory behavior for free without importing the package.

---

## 6. What "done" feels like (worked example)

1. User, in any repo: *"remember that we never mock the DB in integration
   tests — we got burned by a mocked migration last quarter."*
2. The model, following the prompt block, writes
   `<appHome>/memory/no-db-mocks-in-integration-tests.md`:

   ```markdown
   ---
   name: no-db-mocks-in-integration-tests
   description: Integration tests must hit a real DB, not mocks — prior incident: mocked migration passed but prod broke
   type: feedback
   ---

   Integration tests must hit a real database, not mocks.
   **Why:** a mocked migration passed CI but broke prod last quarter.
   **How to apply:** when adding/altering integration tests, wire a real (test) DB.
   ```

   …then `edit`s `<appHome>/memory/MEMORY.md` to add
   `- [no-db-mocks-in-integration-tests](no-db-mocks-in-integration-tests.md) — Integration tests must hit a real DB …`.
   Both writes auto-allow (carve-out, A8) — no permission prompt.
3. Next session, user: *"add a test for the new migration."* The static prompt
   shows the index line. `FindRelevant` reads the manifest, the side-query
   picks the no-db-mocks file as relevant, its body is injected as a
   `<system-reminder>` for that turn — so the model writes a real-DB test
   without being reminded.
4. 60 days later the same recall fires, but the body now carries: *"This memory
   is 60 days old … verify against current code before asserting as fact."*

> **Same-session freshness.** The index line written in step 2 does **not**
> appear in *that* session's static prompt — the index is frozen at session
> start (§5.3); it shows up in the static prompt only next session. The gap is
> covered by recall: `FindRelevant` scans the **live** dir, so a file written
> earlier in the same session is already a candidate — the model can be
> reminded of a memory it just wrote, even though its index line isn't yet
> visible. (ref avoids the gap differently — it reloads the index via the
> user-context channel; evva trades that for a byte-stable prefix — §5.3.)

---

## 7. Out of scope (revisit later)

- **`/dream` / background consolidation** — the turn-end `extractMemories`
  agent and periodic summarization (`ref` `autoDream`, `KAIROS` daily-log
  mode). This phase is the foreground store; consolidation is a separate phase
  that builds on it.
- **Team memory** — `ref`'s `teamMemPaths`/`teamMemPrompts` and the
  private/team `<scope>` split. evva has no team runtime (CLAUDE.md → Out of
  scope → Teams). Port only the **INDIVIDUAL** taxonomy.
- **Per-project / scoped memory dirs** — this phase ships one global store
  (§5.2). A project-keyed dir is an additive follow-up if isolation is needed.
- **A `remember` tool / structured write surface** — reversed in this revision
  (§5.1). Could return as an optional layer over the file format later.
- **A dedicated "memory updated" TUI affordance** — memory writes render as
  ordinary fs file diffs for now (§2.2). A `MemoryUpdateNotification`-style
  surface (ref) is a nice-to-have follow-up.
- **Past-session search** — searching prior conversation transcripts as memory.
  Different data source; not in scope.
- **`/remember` and `/dream` slash commands** — manual save/consolidate
  commands are a follow-up.
- **Embedding/vector recall** — the relevance path is an LLM side-query over
  descriptions (ref's design), not a vector index. Adequate at ≤200 files.
- **User-configurable memory directory** — ref's `autoMemoryDirectory` setting.
  Dropped to keep the carve-out's trust boundary simple (§5.8).
- **Migrating `EVVA.md` or the old auto files** — EVVA.md stays as-is; the old
  auto files are orphaned, not migrated (§5.7).

---

## 8. Verification checklist (PR gate)

- [ ] **Task 1:** `frontmatter.go`, `memtype.go`, `age.go`, `scan.go`,
      `memdirpaths.go` compile; `internal/memdir` still imports only stdlib
      (`go list -deps` shows no new third-party dep).
- [ ] **Task 2:** `ReadIndex` returns truncated content with the named-cap
      warning past 200 lines / 25 KB; no Go-side index writer exists.
- [ ] **Task 3:** `recall` is a sub-package; `FindRelevant` returns `nil` on a
      forced client error and on an empty dir; manifest matches
      `formatMemoryManifest` shape; `selected ⊆ valid filenames` (keyed on the
      recursive relative path); `MEMORY.md` never selected; the recall model
      defaults to a Sonnet-class constant (Haiku only when explicitly set).
- [ ] **Task 4:** `write`/`edit` inside `<appHome>/memory/` auto-allows in
      default + accept-edits (A8); `..`/sibling/absolute-elsewhere does not;
      plan mode still denies; carve-out inert when auto-memory off (A9);
      containment logic single-sourced with `IsPlanFilePath`.
- [ ] **Task 5:** prompt-snapshot test shows the index present, bodies absent
      (A4); recall injects a single system-reminder with freshness notes
      (A6/A7); **system-prompt bytes unchanged by recall**; Load runs once;
      `alreadySurfaced` is derived from the transcript (+ read-file guard) and
      resets on compaction, not held as persistent agent state.
- [ ] **Task 6:** `update_user_profile` / `update_project_memory` /
      `MemoryDiff` / `section.go` / profile+project write helpers / the two
      tool-name constants are gone; `go build ./...` green; Main `activeTools`
      no longer appends them; subagents unaffected.
- [ ] **Task 7:** the prompt block teaches the four types (INDIVIDUAL variant),
      the two-step file+index save, the index/file model, and
      verify-before-citing; uses evva tool names + the literal memory path.
- [ ] **A9:** auto-memory off → no dir created, no scan/recall, no prompt
      section, no index, carve-out inert.
- [ ] **A10:** old on-disk files are neither read, written, nor deleted.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` green.
- [ ] **Docs:** extending + user-guide en/zh-tw + CHANGELOG (Added/Changed/
      Removed, no-migration noted) + version bump + `main.go` startup notice.
- [ ] **Manual (TTY):** ask the agent to remember something; confirm it writes
      a typed `.md` + an index line **without a permission prompt**; start a
      fresh session; ask a related question; confirm the recall system-reminder
      appears in the transcript/logs and the model uses it; backdate a file's
      mtime and confirm the freshness caveat renders; confirm a `write` just
      outside the memory dir still prompts.

---

## 9. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `internal/memdir/frontmatter.go` | **New** — flat frontmatter parser | Task 1 |
| `internal/memdir/memtype.go` | **New** — `MemoryType` + parse | Task 1 |
| `internal/memdir/age.go` | **New** — age/freshness (port `memoryAge.ts`) | Task 1 |
| `internal/memdir/scan.go` | **New** — `ScanMemoryFiles` + manifest | Task 1 |
| `internal/memdir/memdirpaths.go` | **New** — global dir path + ensure-dir + `IsInMemoryDir` | Task 1 |
| `internal/memdir/index.go` | **New** — `ReadIndex` + truncation (port `truncateEntrypointContent`) | Task 2 |
| `internal/memdir/recall/recall.go` | **New** — `FindRelevant` side-query | Task 3 |
| `internal/memdir/memdir.go` | Rewrite — new `Snapshot` (drop UserProfile/ProjectMemory; add MemoryIndex/MemoryDir); `Load` ensures dir + reads index | Task 5.1 |
| `internal/memdir/section.go` | **Delete** | Task 6 |
| `internal/memdir/write.go` | **Delete** profile/project helpers (+ `writeAtomic` unless reused) | Task 6 |
| `internal/memdir/projectkey.go` | **Keep** — still used by session storage; no longer keys memory | §2.1 |
| `internal/tools/memory/` | **Delete** package (both tools + `MemoryDiff`) | Task 6 |
| `pkg/tools/name.go` | Edit — delete `UPDATE_USER_PROFILE` / `UPDATE_PROJECT_MEMORY` | Task 6 |
| `pkg/toolset/tags.go` | Edit — drop their tag rows (no new tool) | Task 6 |
| `pkg/permission/types.go` | Edit — add `IsAutoMemPath(memDir, absPath)` beside `IsPlanFilePath` | Task 4 |
| `pkg/permission/decision.go` | Edit — `memDir` param + `isAutoMemWrite` carve-out branch | Task 4 |
| `internal/agent/state_machine.go` | Edit — thread `memDir` into `Decide` | Task 4 |
| `internal/agent/profiles.go` | Edit — drop `memory.Names()` append + import; thread `MemoryIndex` (Main + disk-persona) | Task 5.2, 6 |
| `internal/agent/agent.go` | Edit — `Load` signature; per-turn recall hook; `alreadySurfaced` derived from transcript + read-file guard (not persistent state) | Task 5.1, 5.3 |
| `internal/agent/sysprompt/sysprompt.go` | Edit — `PromptContext`: drop UserProfile/ProjectMemoryIndex, add `MemoryIndex` | Task 5.2 |
| `internal/agent/sysprompt/main_agent.go` | Edit — drop user-profile section; rewrite `autoMemoryGuidanceSection`; replace `projectMemoryIndexSection` with `memoryIndexSection` | Task 5.2, 7 |
| `pkg/config/config.go` | Edit (optional) — `MemoryRecallModel` (Sonnet-class default; Haiku opt-in), `enable_memory_recall` | Task 3, §5.5 |
| `cmd/evva/main.go` | Edit — startup notice text | Task 8 |
| `pkg/version/version.go` | Edit — bump | Task 8 |
| `CHANGELOG.md` | Edit — Added/Changed/Removed (no migration) | Task 8 |
| `docs/extending.md`, `docs/user-guide/{en,zh-tw}/user-guide.md` | Edit | Task 8 |

---

## 10. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — frontmatter/types/age/scan/paths | ~300 | 3 h |
| Task 2 — index read + truncation | ~80 | 1 h |
| Task 3 — relevance retriever + fake-client tests | ~200 | 3 h |
| Task 4 — permission write carve-out + wiring | ~120 | 2 h |
| Task 5 — load + injection + per-turn recall hook | ~150 | 3 h |
| Task 6 — delete old model (tools, section.go, helpers, constants) | ~ −600 (net delete) | 1.5 h |
| Task 7 — typed-memory prompt content (port) | ~150 prose | 2 h |
| Task 8 — docs + changelog + version + notice | ~90 | 1 h |
| Tests across the above | ~400 | 3.5 h |

Net new code is **smaller** than the earlier draft (no `remember` tool, no
index writer, no migration) and Task 6 removes more than it adds. The single
biggest risk is **Task 5.3 cache discipline**; the single biggest prose lever
is **Task 7**; the trust boundary that makes Write-direct safe is **Task 4**.
