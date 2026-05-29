# PRD — Typed Memory Directory — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD (proposed `v1.6+` candidate; the roadmap's
> v1.6 "open slot" explicitly names a *"`/dream` / background-consolidation
> memory phase"* — this is that phase, scoped to the foreground pieces).
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.6 (open slot)*.
> **Reference source:** `ref/src/memdir/` (`memoryTypes.ts`,
> `memoryScan.ts`, `findRelevantMemories.ts`, `memoryAge.ts`, `paths.ts`).

---

## 1. TL;DR — what this phase actually is

evva today has a **fixed-section, fixed-file** memory model. Three files,
each a flat list of pre-declared H2 headings:

- `<workdir>/EVVA.md` — user-authored repo conventions (not auto-memory).
- `<appHome>/USER_PROFILE.md` — sections `Preferences`, `Working style`,
  `Recurring topics` (`internal/memdir/section.go:24`).
- `<appHome>/projects/<key>/MEMORY.md` — sections `Project facts`,
  `Decisions`, `Open issues`, `References` (`section.go:34`).

The two auto files are written by `update_user_profile` /
`update_project_memory` (`internal/tools/memory/memory.go`), each of which
merges a `{sections: map}` payload into the *closed* heading set via
`memdir.MergeSections`. The **entire** memory body is injected into the
system prompt every turn (`USER_PROFILE.md` verbatim, project `MEMORY.md`
as a one-line-per-section `IndexSummary`).

This is safe and cache-stable, but it is a *notes file*, not a memory
**store**. Its limits, all structural:

- **No individual memories.** A "memory" is a paragraph inside a section,
  not an addressable unit. You can't age one, link one, or retrieve one.
- **No relevance retrieval.** Everything is always in the prompt or
  nowhere. There is no "surface the 3 memories relevant to *this* query"
  path — so the prompt either carries dead weight or misses context.
- **No staleness signal.** A `Decisions` entry citing `foo.go:42` looks as
  authoritative on day 200 as on day 0. ref added freshness caveats
  precisely because users reported stale `file:line` claims being asserted
  as fact (`memoryAge.ts:34`).
- **A closed taxonomy.** Four project headings + three user headings,
  hand-declared in Go. ref's taxonomy is four *types* (`user`, `feedback`,
  `project`, `reference`) that each describe *many* files.

This phase ports ref's **typed memory directory**: each scope becomes a
*directory* of individual `.md` files (one memory per file, frontmatter
`name` / `description` / `type`) plus a `MEMORY.md` **index** that is the
only always-injected artifact. Individual memories are pulled in **on
demand** by an LLM relevance side-query (`findRelevantMemories`), wrapped
with **age-based freshness caveats** when stale.

Concretely, the deliverable is:

1. **A typed-file layer** in `internal/memdir`: frontmatter parse, a
   `MemoryHeader` scan, `MemoryType` taxonomy, age helpers — direct ports
   of `memoryScan.ts` / `memoryTypes.ts` / `memoryAge.ts`.
2. **A relevance retriever** (`findRelevantMemories`) that runs a cheap LLM
   side-query over the scanned headers and returns the ≤5 most relevant
   memory paths — ported from `findRelevantMemories.ts`, using evva's
   existing `llm.Client`.
3. **A write surface**: replace the two section-merge tools with a single
   `remember` tool that creates/updates **one typed memory file** and
   maintains the `MEMORY.md` index atomically (the structured, safe
   analog of ref's "model writes files directly" approach — see §5.1).
4. **Wiring**: the `MEMORY.md` index injects statically (cache-stable, like
   `ProjectMemoryIndex` today); relevant individual memories inject
   **per-turn as a `<system-reminder>` message** from the agent loop, never
   into the static prompt (cache preservation — §5.3).
5. **A migration**: one-shot fold of existing `USER_PROFILE.md` /
   project `MEMORY.md` sections into seed typed files so no one loses notes.

This is **not** the `/dream` background-consolidation agent, team memory,
or past-session search. Those stay out (see §7) — this phase delivers the
foreground store they would later build on.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 `internal/memdir/` (private) — the current memory layer

| File | What it provides | Fate in this phase |
| --- | --- | --- |
| `memdir.go` | `Load(workdir, appHome, loadProjectMemory) Snapshot`; `Snapshot{WorkdirMemory, UserProfile, ProjectMemory, ProjectMemoryIndex, Warnings}`; `readMemFile` (64 KiB cap) | **Extend** — `Snapshot` gains index + scanned-header fields; `Load` also reads the two `MEMORY.md` indexes |
| `section.go` | `UserProfileSections`, `ProjectMemorySections`, `MergeSections`, `IndexSummary`, the H2 section parser | **Keep for migration only** — the section parser reads the *old* files during the one-shot migration (Task 7); new writes don't use it |
| `write.go` | `UserProfilePath`, `ProjectMemoryPath`, `WriteUserProfile`, `WriteProjectMemory`, `EnsureProjectMemoryDir`, `writeAtomic` | **Reuse** `writeAtomic`; **add** directory-path helpers |
| `projectkey.go` | `ProjectKey(absPath)` — stable per-repo slug | **Reuse verbatim** — keys the project memory dir |

`writeAtomic` (`write.go:81`) is the atomic temp-then-rename writer; every
new memory write reuses it. `ProjectKey` (`projectkey.go:26`) already gives
us the per-project directory name. Do **not** add a second key scheme.

The package is **stdlib-only and not imported by `sysprompt`** by design
(`memdir.go:15`). The relevance retriever breaks the stdlib-only rule (it
needs `llm.Client`), so it lives in a **sub-package** `internal/memdir/recall`
to keep the base package dependency-light — see Task 3.

### 2.2 `internal/tools/memory/` — the current write tools

`update_user_profile` and `update_project_memory` (`memory.go`). Both:

- gate on `cfg.GetEnableAutoMemory()` (`memory.go:101`, `:200`);
- take `{sections: map[string]string}`, validate against the closed
  heading set, call `memdir.MergeSections`, write via
  `memdir.Write{UserProfile,ProjectMemory}`;
- emit a `MemoryDiff` metadata blob the TUI renders (`memory.go:40`);
- are surfaced via `memory.Names()` (`memory.go:29`), appended to the Main
  profile's `activeTools` only when auto-memory is on
  (`internal/agent/profiles.go:147-149`).

**These two tools are replaced by one `remember` tool** (Task 4). Keep
`MemoryDiff` — the TUI renderer keys off it and the new tool reuses it.

### 2.3 Memory load + prompt injection seam

- **Load:** `internal/agent/agent.go:348` —
  `a.memSnap = memdir.Load(a.workdir, a.cfg.AppHome, a.cfg.GetEnableAutoMemory())`;
  resume path at `agent.go:1107`. `WithMemorySnapshot` option exists
  (`internal/agent/options.go:230`). `ResolveMainProfileAutoMem`
  (`profiles.go:264`) auto-loads for SDK/bootstrap callers.
- **Thread into prompt:** `profiles.go:173-176` copies snapshot fields into
  `PromptContext` (`.WorkdirMemory`, `.UserProfile`, `.ProjectMemoryIndex`,
  `.EnableAutoMemory`); the disk-persona path mirrors it at
  `profiles.go:285-288` (gated by `def.OmitMemory`).
- **Render:** `internal/agent/sysprompt/main_agent.go:50 buildMainPrompt`
  calls, in order, `memorySection("Project memory (from EVVA.md)", …)`,
  `memorySection("User profile …", …)`, `autoMemoryGuidanceSection(ctx)`
  (`main_agent.go:208`), `projectMemoryIndexSection(ctx)` (`:274`).
- **Constraint:** `sysprompt` imports only stdlib (`sysprompt.go:9`). It
  receives *strings*, never does I/O or LLM calls. So **all** new logic
  (scan, recall) happens in the caller (agent loop / profile builder); the
  prompt package only gains a couple of string fields. This is the same
  one-way arrow the package doc already documents — respect it.

### 2.4 The agent loop — where per-turn retrieval lives

`internal/agent/agent.go` holds `a.memSnap` and the live `llm.Client`. The
relevance side-query (one cheap completion per user turn) must run here,
*before* the main completion, and inject its result as a per-turn message.
This is the same place ref calls `findRelevantMemories` (its query loop).
Locate the per-user-turn entry point (the function that takes a new user
message and assembles the request) and add the recall hook there — see
Task 5. **Do not** put it in `sysprompt` (no I/O there) or in `Load` (no
query available at load time).

### 2.5 `pkg/skill` — the disk-markdown loader template

`pkg/skill/registry.go` + `skill.go` is evva's existing "load Markdown
files from a directory into a registry" implementation
(`LoadRegistry` reads AppHome then WorkDir, parses a title line, lazy-loads
bodies). The memory scanner is the same shape — *walk a dir, read headers,
build a list* — but reads **YAML frontmatter** instead of a title line.
evva's skill loader parses `# name description` (first line), **not**
frontmatter (`registry.go:14-18`), so the frontmatter parser is **new**
(Task 1). Model the directory-walk + precedence ergonomics on `pkg/skill`;
don't copy its title-line parsing.

### 2.6 Reference (`ref/src/memdir/`)

| File | What it does | Port? |
| --- | --- | --- |
| `memoryTypes.ts` | `MEMORY_TYPES = [user, feedback, project, reference]`; `parseMemoryType`; the big `TYPES_SECTION_*` prompt blocks | **Yes** — port the type enum + parser; port the **private** `TYPES_SECTION` prompt (drop the `<scope>` team/private qualifiers — evva has no teams) |
| `memoryScan.ts` | `scanMemoryFiles(dir)` → `MemoryHeader[]` (frontmatter, mtime, sorted, cap 200); `formatMemoryManifest` | **Yes** — direct port to Go |
| `findRelevantMemories.ts` | side-query to a cheap model selecting ≤5 relevant files by name+description; excludes `MEMORY.md` + already-surfaced | **Yes** — port, using `llm.Client` for the side-query |
| `memoryAge.ts` | `memoryAgeDays`, `memoryAge`, `memoryFreshnessText`, `memoryFreshnessNote` | **Yes** — trivial, port verbatim |
| `paths.ts` | dir resolution, `isAutoMemoryEnabled`, path-safety validation; **team/CCR/growthbook branches** | **Partial** — port the dir-layout + path-safety idea; evva's gate is already `cfg.GetEnableAutoMemory()`; drop env/CCR/growthbook/team branches |

The big `TYPES_SECTION_COMBINED` in `memoryTypes.ts` carries `<scope>`
private/team annotations. **Port the `private`-only variant** — strip every
team/scope qualifier. evva has no team memory (out of scope, §7).

---

## 3. Goal & acceptance criteria

**Goal:** memory becomes a per-scope **directory of typed, individually
addressable `.md` files** with a stable `MEMORY.md` index. The index is
always in the prompt; individual memories are surfaced on demand by a
relevance side-query and carry freshness caveats when stale. The model
writes one memory at a time through a `remember` tool that also maintains
the index. Existing users' notes are migrated, not lost.

Ship is complete when **all** of these pass:

- **A1 — Directory layout.** On first write under auto-memory, evva creates
  `<appHome>/memory/MEMORY.md` (user scope) and
  `<appHome>/projects/<key>/memory/MEMORY.md` (project scope) and writes
  the new memory as an individual `.md` file beside the index.
- **A2 — Frontmatter round-trips.** A memory file written by `remember`
  parses back via the scanner to the same `name`, `description`, `type`.
  Unknown/missing `type` degrades to `nil` without error (legacy-tolerant,
  mirroring `parseMemoryType`).
- **A3 — Scan.** `ScanMemoryFiles(dir)` returns headers sorted newest-first,
  excludes `MEMORY.md`, caps at 200, and never errors on a malformed file
  (it's skipped). A directory with no `.md` files returns an empty slice.
- **A4 — Index injected, bodies not.** The system prompt contains the
  `MEMORY.md` index body for each scope and **does not** contain any
  individual memory file body. Verified by a prompt-snapshot test.
- **A5 — Relevance recall.** Given a populated memory dir and a user query,
  `FindRelevant(ctx, client, query, dirs)` returns ≤5 paths whose
  name/description the side-query judged relevant; `MEMORY.md` is never
  returned; an empty/irrelevant set returns `[]` (not an error).
- **A6 — Recall injected per-turn.** When recall returns memories, their
  bodies are injected into the turn as a single `<system-reminder>`-wrapped
  message (not the static prompt), each prefixed with its freshness note
  when >1 day old (A7). The static system prompt bytes are **unchanged**
  by recall (cache preserved).
- **A7 — Freshness caveats.** `MemoryFreshnessText(mtime)` returns "" for
  memories ≤1 day old and the "Verify against current code before
  asserting as fact" caveat with the correct day count for older ones
  (`memoryAge.ts:33` parity).
- **A8 — `remember` write.** `remember({name, description, type, body,
  scope})` creates `<scope-dir>/<slug(name)>.md` with correct frontmatter,
  appends/updates the matching `MEMORY.md` index line, and returns a
  `MemoryDiff`. A second call with the same `name` updates in place (no
  duplicate file, index line replaced).
- **A9 — Type validation.** `type` ∉ {user, feedback, project, reference}
  is rejected with a clean error naming the allowed set. `scope` ∉ {user,
  project} likewise.
- **A10 — Auto-memory gate.** With `cfg.GetEnableAutoMemory() == false`,
  `remember` returns the disabled-error (parity with current tools at
  `memory.go:101`), no scan/recall runs, and the prompt carries no memory
  guidance section.
- **A11 — Index integrity.** Deleting a memory file and re-running the
  index-rebuild helper produces a `MEMORY.md` with no dangling line for the
  removed file; an orphan index line (file missing) is dropped on rebuild.
- **A12 — Migration.** On first boot after upgrade, an existing
  `USER_PROFILE.md` / project `MEMORY.md` with content is converted into
  seed typed files (one per non-empty section) under the new dirs, the old
  file is renamed to `*.migrated.bak`, and the session boots with the new
  layout. A no-op when the old files are absent or empty.
- **A13 — Path safety.** `remember` rejects a `name` that escapes the scope
  dir (slashes, `..`, absolute, null byte); the slug is confined to the
  scope directory (port of `validateMemoryPath` intent, `paths.ts`).
- **A14 — Tests.** Unit coverage for frontmatter parse, scan ordering/cap,
  type/scope validation, slug safety, age helpers, index merge/rebuild, and
  the migration. A recall test with a **fake `llm.Client`** asserts the
  manifest prompt shape and the path-filtering (no live model).
- **A15 — Docs + version + changelog.** Update `docs/extending.md` memory
  notes; user-guide en + zh-tw memory section; `CHANGELOG.md` block;
  `pkg/version.Version` bump.

---

## 4. Work breakdown (ordered)

### Task 0 — Decide & record the write-surface shape (read §5.1 first)

Before code: confirm the **`remember`-tool** approach (one structured tool
writes one typed file + maintains the index) over the **file-tool**
approach (model uses `write`/`edit` freehand, guided by a prompt block).
§5.1 recommends `remember`. This task is a 10-minute alignment gate, not
code — but it determines Tasks 4 and 6, so settle it first and note the
choice at the top of the PR description.

### Task 1 — Frontmatter + types + age (`internal/memdir`)

New files in the base package (still stdlib-only):

```
internal/memdir/
├── frontmatter.go   # ParseFrontmatter(content) (map[string]string, body, error)
├── memtype.go       # MemoryType, MEMORY_TYPES, ParseMemoryType
├── age.go           # AgeDays, Age, FreshnessText, FreshnessNote (port memoryAge.ts)
├── scan.go          # MemoryHeader, ScanMemoryFiles, FormatManifest (port memoryScan.ts)
└── memdirpaths.go   # UserMemoryDir(appHome), ProjectMemoryDir(appHome, workdir), slug + safety
```

**`frontmatter.go`** — a minimal YAML-ish frontmatter reader: if the file
starts with a `---` line, read until the next `---`, parse `key: value`
lines (string values only; `metadata.type` flattened to `type`), return the
remaining body. Do **not** pull in a YAML dependency — memory frontmatter is
flat `key: value`, and a 40-line hand parser avoids a new dep (consistent
with `memdir`'s stdlib-only charter). Reject nothing; malformed frontmatter
yields an empty map + full content as body (legacy-tolerant, A2).

**`memtype.go`** — port `memoryTypes.ts:15-31`:

```go
type MemoryType string

const (
    TypeUser      MemoryType = "user"
    TypeFeedback  MemoryType = "feedback"
    TypeProject   MemoryType = "project"
    TypeReference MemoryType = "reference"
)

var MemoryTypes = []MemoryType{TypeUser, TypeFeedback, TypeProject, TypeReference}

// ParseMemoryType returns ("", false) for unknown/missing — legacy files
// without a type: keep working (memoryTypes.ts:parseMemoryType parity).
func ParseMemoryType(raw string) (MemoryType, bool) { … }
```

**`age.go`** — verbatim port of `memoryAge.ts` (clamp negatives to 0; "today"
/ "yesterday" / "N days ago"; freshness caveat empty for ≤1 day). These are
pure functions; copy the wording of `memoryFreshnessText` exactly — it was
tuned against real stale-memory incidents.

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

// ScanMemoryFiles walks dir recursively for *.md (excluding MEMORY.md),
// reads the first FrontmatterMaxLines of each for frontmatter, returns
// headers sorted newest-first, capped at MaxMemoryFiles. Never errors:
// an unreadable/ malformed file is skipped. Missing dir → empty slice.
func ScanMemoryFiles(dir string) []MemoryHeader { … }

// FormatManifest renders "- [type] filename (RFC3339): description" lines,
// one per header (memoryScan.ts:formatMemoryManifest parity).
func FormatManifest(hs []MemoryHeader) string { … }
```

Read only the header window (cap the read at `FrontmatterMaxLines`, reuse
the `io.LimitReader` discipline from `readMemFile`); never slurp full bodies
during scan.

**`memdirpaths.go`** — directory resolution + slug + safety:

```go
const MemoryDirName = "memory"
const MemoryIndexFile = "MEMORY.md"

func UserMemoryDir(appHome string) string          // <appHome>/memory
func ProjectMemoryDir(appHome, workdir string) string // <appHome>/projects/<key>/memory

// Slug turns a memory name into a safe basename: lowercase, spaces→-,
// drop anything outside [a-z0-9-], collapse repeats, cap length. Rejects
// (returns "", error) names that are empty after slugging. The slug is
// ALWAYS confined to the scope dir — callers join slug to the scope dir;
// a name with "/" or ".." slugs to a flat safe token (A13).
func Slug(name string) (string, error)
```

### Task 2 — Index read + maintenance (`internal/memdir`)

`MEMORY.md` is the always-injected index. It is a human/model-readable list
of one-line pointers, exactly the shape this very runtime uses (see the
"auto memory" block in evva's own Main prompt today):

```
- [Title](file.md) — one-line hook
```

Add to `internal/memdir/index.go`:

```go
// ReadIndex returns the MEMORY.md body for a memory dir ("" if absent),
// capped like readMemFile.
func ReadIndex(memoryDir string) (string, string)

// UpsertIndexLine ensures MEMORY.md has exactly one line pointing at
// `file` with the given title + hook, replacing any existing line for
// that file. Order preserved; new lines appended under the index header.
func UpsertIndexLine(memoryDir, file, title, hook string) error

// RebuildIndex regenerates MEMORY.md from a fresh ScanMemoryFiles, dropping
// orphan lines (file gone) and adding missing ones (A11). Used by the
// rebuild path + migration; NOT on the hot write path.
func RebuildIndex(memoryDir string) error
```

`UpsertIndexLine` is the hot path (one line edit per `remember`);
`RebuildIndex` is the occasional reconciler. Both write via `writeAtomic`.

> **Why an index at all, when we also scan?** The index is *static and
> small* — it injects into the system prompt every turn cheaply and tells
> the model *what it knows* without paying per-file cost (this is exactly
> what `ProjectMemoryIndex`/`IndexSummary` does today, `section.go:91`).
> The scan + recall path is *dynamic* — it pulls the **bodies** of the few
> relevant files into a single turn. Index = "table of contents always
> visible"; recall = "open the 3 relevant chapters for this question."

### Task 3 — Relevance retriever (`internal/memdir/recall`)

New **sub-package** (depends on `llm.Client`, so it cannot live in the
stdlib-only base package):

```
internal/memdir/recall/
├── recall.go       # FindRelevant(...) — port findRelevantMemories.ts
└── recall_test.go  # fake llm.Client; manifest-shape + filtering tests
```

```go
// FindRelevant scans the given memory dirs, asks a cheap model to select
// the memories whose name/description are clearly useful for `query`
// (≤5), and returns their absolute paths + mtimes, newest-first. MEMORY.md
// is never a candidate (already in the prompt). `alreadySurfaced` filters
// paths shown in prior turns so the 5-slot budget spends on fresh files.
//
// Never errors out of band: a model failure / context cancel returns nil,
// so a recall hiccup degrades to "no extra memories this turn", never a
// broken turn (findRelevantMemories.ts catch parity).
func FindRelevant(
    ctx context.Context,
    client llm.Client,
    model constant.Model,
    query string,
    dirs []string,
    recentTools []string,
    alreadySurfaced map[string]bool,
) []memdir.MemoryHeader
```

Port `SELECT_MEMORIES_SYSTEM_PROMPT` from `findRelevantMemories.ts:18`
verbatim (it encodes hard-won selection discipline — "be selective", "don't
re-surface usage docs for tools already in use, DO surface gotchas"). Send
the `FormatManifest` output as the user message. Constrain the model to a
small JSON object `{"selected": ["file.md", …]}`; filter the result against
the valid filename set before returning (the model can hallucinate names).

**Model choice:** use a cheap/fast tier, not the main model. evva's config
exposes `DefaultModel`; add an optional `cfg.MemoryRecallModel` (falls back
to a Haiku-class constant, or to `DefaultModel` when unset). Cap
`max_tokens` at ~256. One call per user turn — keep it cheap.

> **Provider portability:** ref uses Anthropic structured `output_format`.
> Not every evva provider supports JSON-schema-constrained output. Make the
> retriever tolerant: request JSON in the prompt, parse defensively, and on
> any parse failure return `nil` (degrade, don't crash). The
> `selected ⊆ validFilenames` filter is the real safety net.

### Task 4 — The `remember` write tool (`internal/tools/memory`)

Replace `update_user_profile` + `update_project_memory` with one tool.

**`pkg/tools/name.go`:** add `REMEMBER ToolName = "remember"`. Keep the two
old constants only if migration/back-compat needs them; otherwise remove
(and drop their `Names()` entries). The PR should delete dead constants
rather than leave them dangling (CLAUDE.md: no back-compat shims).

**Schema:**

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["name", "description", "type", "body", "scope"],
  "properties": {
    "name":        {"type": "string", "description": "Short kebab-case-able title; becomes the filename + index title"},
    "description": {"type": "string", "description": "One-line summary used to judge relevance in future turns — be specific"},
    "type":        {"type": "string", "enum": ["user", "feedback", "project", "reference"]},
    "scope":       {"type": "string", "enum": ["user", "project"], "description": "user = cross-project (<appHome>/memory); project = this repo only"},
    "body":        {"type": "string", "description": "The memory content. For feedback/project, lead with the rule/fact then Why: and How to apply: lines"}
  }
}
```

**Execute:** gate on `GetEnableAutoMemory()` (A10) → resolve scope dir
(`UserMemoryDir` / `ProjectMemoryDir`) → `Slug(name)` (A13) → compose
frontmatter + body → `writeAtomic(<dir>/<slug>.md, …)` → `UpsertIndexLine`
→ return `tools.Result{Content: "remembered <name> (<type>, <scope>)",
Metadata: &MemoryDiff{…}}`. Reuse the existing `MemoryDiff` struct
(`memory.go:40`) so the TUI renderer is unchanged.

The tool **description** is the model's instruction surface. Port the
*intent* of evva's current excellent tool descriptions
(`memory.go:48-63`, `:143-157`) — "use sparingly", "persist only what's
true next session", "verify file:line before saving a memory that cites
code" — recast for one-file-per-memory. Pull the type taxonomy + when-to-
save guidance from the ported `TYPES_SECTION` (Task 8). This description is
the analog of the big "auto memory" block in evva's own Main prompt today.

### Task 5 — Wire load, index injection, and per-turn recall

**5.1 Snapshot fields.** Extend `memdir.Snapshot` (`memdir.go:46`):

```go
UserMemoryIndex    string           // <appHome>/memory/MEMORY.md body
ProjectMemoryIndex string           // <appHome>/projects/<key>/memory/MEMORY.md body (REPLACES old per-section IndexSummary)
MemoryDirs         []string         // absolute scope dirs that exist — handed to the recall path
```

`Load` (`memdir.go:60`) reads both indexes (`ReadIndex`) and records the
scope dirs. Keep `WorkdirMemory` (`EVVA.md`) and `UserProfile` for the
migration window, but the steady-state prompt uses the indexes.

**5.2 Prompt injection (static, cache-stable).**
`profiles.go:173-176` already copies snapshot → `PromptContext`. Point
`ctx.ProjectMemoryIndex` at the new `ProjectMemoryIndex`, add
`ctx.UserMemoryIndex`. In `sysprompt`, `projectMemoryIndexSection`
(`main_agent.go:274`) renders the project index; add a sibling
`userMemoryIndexSection`. `autoMemoryGuidanceSection` (`main_agent.go:208`)
gets the rewritten typed-memory guidance (Task 8). **No new I/O in
sysprompt** — it still just formats strings.

**5.3 Per-turn recall (dynamic).** In the agent loop's per-user-turn
assembly (§2.4), when `GetEnableAutoMemory()` and `len(memSnap.MemoryDirs) > 0`:

1. call `recall.FindRelevant(ctx, a.client, recallModel, userMsg, dirs,
   recentTools, a.surfacedMemories)`;
2. for each returned header, read its body and prefix
   `FreshnessNote(mtime)` when stale (A7);
3. inject the concatenation as **one** `<system-reminder>`-wrapped message
   appended to the turn (mirror how evva already injects per-turn reminders
   — e.g. the plan-mode attachment path referenced at `main_agent.go:46`);
4. add the surfaced paths to `a.surfacedMemories` so later turns don't
   re-pick them (the `alreadySurfaced` budget guard).

> **Cache invariant (critical):** recalled bodies go in a **message**, never
> in the system prompt. The system prompt (with the static index) must be
> byte-identical turn to turn so the provider prefix-cache keeps hitting.
> A4 + A6 are the tests that lock this in. This is the single most
> important design constraint in the phase — getting it wrong silently
> doubles token cost.

### Task 6 — Activate the tool; deactivate the old ones

`profiles.go:147-149` currently appends `memory.Names()` when auto-memory is
on. Update `memory.Names()` to return `[]tools.ToolName{tools.REMEMBER}`.
The gate and call site are unchanged — just the tool set behind them. Add
the `pkg/toolset/tags.go` row for `remember`. Subagents don't get it
(same posture as today; verify Explore/Plan/General defs don't pull it).

### Task 7 — Migration

`internal/memdir/migrate.go`:

```go
// MigrateLegacyMemory converts old fixed-section files to typed files the
// first time it runs. Idempotent: a sentinel (the renamed *.migrated.bak,
// or absence of the old file) means "already done / nothing to do".
//   USER_PROFILE.md  sections → user-scope typed files (type=user/feedback)
//   project MEMORY.md sections → project-scope typed files (type=project/reference)
// Each non-empty section becomes one memory file; the heading seeds name +
// description. Old file renamed to <name>.migrated.bak. Indexes rebuilt.
func MigrateLegacyMemory(cfg *config.Config, workdir string) (migrated int, warnings []string)
```

Call it once at boot, right before `memdir.Load` in the bootstrap path
(`cmd/evva` and `ResolveMainProfileAutoMem`). Use the **existing**
`parseSections` (`section.go:169`) to read the old files — that's the one
remaining use of the old parser. Map sections → types with a small table
(`Preferences`/`Working style` → `user`; `Decisions` → `feedback` or
`project`; `References` → `reference`; etc.). Heading + first line seed the
`description`. Log a one-line summary; never block boot on a migration
failure (warn + continue, matching `Load`'s never-fail contract).

### Task 8 — Prompt content (the typed-memory guidance)

Rewrite `autoMemoryGuidanceSection` (`main_agent.go:208`) from the
"two section-merge tools" framing to the typed-directory framing, porting
the **private** `TYPES_SECTION` from `memoryTypes.ts` (drop team/scope
qualifiers). It must teach: the four types + when to save each; the
`remember` tool shape; the **index vs. file** model; the
verify-before-citing discipline; "use sparingly / persists across
sessions". This is the highest-leverage prose in the phase — the model's
entire memory behavior derives from it. Keep it tight; cross-reference the
tool description rather than duplicating the schema.

### Task 9 — Docs + version

- `docs/extending.md` — update the memory description (dir layout, types).
- `docs/user-guide/en/user-guide.md` + `zh-tw` — memory section: where
  files live, how relevance recall surfaces them, the freshness caveat.
- `CHANGELOG.md` — `### Added` (typed memory dir, `remember`, relevance
  recall, freshness caveats), `### Changed` (USER_PROFILE/MEMORY → typed
  files; auto-migrated), `### Removed` (`update_user_profile`,
  `update_project_memory`).
- `pkg/version/version.go` — bump.

---

## 5. Design decisions & risks (read before coding)

### 5.1 — `remember` tool vs. free-form file writes (the central call)

ref's main agent **writes memory files with the standard file tools**,
guided by a long system-prompt block (the "auto memory" section). evva's
current design deliberately went the other way — *constrained* tools so
"the model cannot accidentally create new section names or clobber
unrelated content" (`memory.go:9-12`).

**Recommendation: keep evva's structured-write philosophy — ship a
`remember` tool** that writes exactly one memory file and maintains the
index atomically. Rationale:

- **Safety**: free-form `write`/`edit` into the memory dir lets the model
  clobber the index, write malformed frontmatter, or escape the dir. The
  tool centralizes slug-safety (A13), frontmatter shape (A2), and index
  upkeep (A8/A11) in one validated path.
- **Index consistency**: with file-tools, keeping `MEMORY.md` in sync is a
  second manual step the model often forgets; `remember` does it in the
  same call.
- **Consistency with evva**: evva already chose structured memory tools and
  a structured `config` tool over "edit the YAML yourself". `remember`
  matches that house style.

**The tradeoff** is fidelity: ref's approach is more flexible (the model can
restructure memories freely) and needs no bespoke tool. If a future phase
wants ref-exact behavior, it can add the memory dir to the model's
read-allowlist and lean on file-tools — but that is a **deliberate** later
choice, not the v1 default. This is an open design call with no strong
external constraint; per house practice I'm making the call (structured
`remember`) and recording the alternative rather than deferring it.

### 5.2 — Two scopes, two directories (not one)

ref (private) uses a single `~/.claude/memory/`. evva already has a
meaningful **user vs. project** split (cross-project preferences vs.
per-repo facts), keyed by `ProjectKey`. Keep both as **separate
directories** with **separate indexes**, both fed into recall. This
preserves per-repo isolation (a memory about repo A never leaks into repo
B's prompt) while still letting one relevance query span both. The `scope`
field on `remember` (A8) is how the model picks; the type taxonomy is
orthogonal to scope (a `feedback` memory can be user- or project-scoped).

### 5.3 — Cache discipline is non-negotiable (see Task 5.3)

The static prompt (identity + index) must be byte-stable across turns;
recalled bodies ride in a per-turn message. Putting recalled bodies in the
system prompt would re-cost the entire prefix every turn. A4/A6 lock this
in. If a reviewer sees recalled memory text flowing into `PromptContext`,
that's the bug.

### 5.4 — Recall must never break a turn

`FindRelevant` returns `nil` on *any* failure (model error, cancel, parse
failure, empty dir). A flaky side-query degrades to "no bonus context this
turn" — it never errors the user's actual request. This mirrors the ref
`catch` that returns `[]`. Tested with a fake client that errors (A14).

### 5.5 — The side-query has a cost

One extra completion per user turn. Mitigations: cheap model tier (§Task 3),
`max_tokens ≤ 256`, skip entirely when the memory dirs are empty (common
for new users), and the `alreadySurfaced` filter so we don't re-pay for
re-selecting the same files. Consider a config toggle
`enable_memory_recall` (default on when auto-memory is on) so cost-sensitive
users can keep the index but drop the per-turn query. **Recommended:** ship
the toggle; it's four lines and a real escape hatch.

### 5.6 — Frontmatter parser: hand-rolled, not a YAML dep

`internal/memdir` is stdlib-only by charter (`memdir.go:15`). Memory
frontmatter is flat `key: value` (plus the nested `metadata.type` we
flatten). A ~40-line parser keeps the charter; a YAML dep would be the
camel's nose. If a future memory needs structured frontmatter, revisit then.

### 5.7 — Migration is one-way and conservative

We rename old files to `*.migrated.bak` rather than deleting — a user who
dislikes the new layout still has their notes. Migration never merges or
summarizes (no LLM call); it's a mechanical section→file fan-out so it's
deterministic and testable (A12). If a section is empty, it's skipped (no
empty memory files).

### 5.8 — Why this isn't a `pkg/` package

Memory is evva-runtime-specific (knows `*config.Config`, `ProjectKey`,
evva's prompt seams). It stays under `internal/`. The recall sub-package
depends on `llm.Client` (a public seam) but is itself internal —
downstream SDK hosts get the agent's memory behavior for free without
importing the package.

---

## 6. What "done" feels like (worked example)

1. User, in repo `acme/api`: *"remember that we never mock the DB in
   integration tests — we got burned by a mocked migration last quarter."*
2. Model calls `remember({name:"no-db-mocks-in-integration-tests",
   description:"Integration tests must hit a real DB, not mocks — prior
   incident: mocked migration passed but prod broke", type:"feedback",
   scope:"project", body:"…rule… **Why:** … **How to apply:** …"})`.
3. File `<appHome>/projects/<key-for-acme-api>/memory/no-db-mocks-in-integration-tests.md`
   is written; `MEMORY.md` gains
   `- [no-db-mocks-in-integration-tests](no-db-mocks-in-integration-tests.md) — Integration tests must hit a real DB …`.
4. Next session, user: *"add a test for the new migration."* The static
   prompt shows the index line. `FindRelevant` reads the manifest, the
   side-query picks the no-db-mocks file as relevant, its body is injected
   as a `<system-reminder>` for that turn — so the model writes a
   real-DB test without being reminded.
5. 60 days later the same recall fires, but the body now carries: *"This
   memory is 60 days old … verify against current code before asserting as
   fact."*

---

## 7. Out of scope (revisit later)

- **`/dream` / background consolidation** — the turn-end `extractMemories`
  agent and periodic summarization (`ref` `autoDream`). This phase is the
  foreground store; consolidation is a separate phase that builds on it.
- **Team memory** — `ref`'s `teamMemPaths`/`teamMemPrompts` and the
  private/team `<scope>` split. evva has no team runtime (CLAUDE.md → Out of
  scope → Teams). Port only the **private** taxonomy.
- **Past-session search** — searching prior conversation transcripts as
  memory. Different data source; not in `ref/src/memdir`.
- **`/remember` and `/dream` slash commands** — the tool covers writes; a
  manual recall/consolidation command is a nice-to-have follow-up.
- **Embedding/vector recall** — the relevance path is an LLM side-query over
  descriptions (ref's design), not a vector index. Adequate at ≤200 files;
  revisit only if scale demands it.
- **Migrating `EVVA.md`** — it's user-authored repo conventions, not
  auto-memory. Leave it exactly as is.

---

## 8. Verification checklist (PR gate)

- [ ] **Task 0:** PR description states the write-surface choice
      (`remember` recommended) and why.
- [ ] **Task 1:** `frontmatter.go`, `memtype.go`, `age.go`, `scan.go`,
      `memdirpaths.go` compile; `internal/memdir` still imports only stdlib
      (`go list -deps` shows no new third-party dep).
- [ ] **Task 2:** index upsert is single-line; `RebuildIndex` drops orphans
      and adds missing (A11).
- [ ] **Task 3:** `recall` is a sub-package; `FindRelevant` returns `nil`
      on a forced client error; manifest prompt matches `formatMemoryManifest`
      shape; selected ⊆ valid filenames.
- [ ] **Task 4:** `remember` writes file + index in one call; same-name
      re-write updates in place (A8); type/scope/slug rejections clean
      (A9/A13); `MemoryDiff` metadata preserved.
- [ ] **Task 5:** prompt-snapshot test shows indexes present, bodies absent
      (A4); recall injects a single system-reminder message with freshness
      notes (A6/A7); **system-prompt bytes unchanged by recall** (A6).
- [ ] **Task 6:** Main `activeTools` has `remember` (auto-memory on), not the
      old two; subagents have none.
- [ ] **Task 7:** migration converts seed files, renames old to `.bak`,
      idempotent on second boot (A12).
- [ ] **Task 8:** `autoMemoryGuidanceSection` teaches the four types, the
      index/file model, and verify-before-citing.
- [ ] **A10:** auto-memory off → `remember` errors, no scan/recall, no
      prompt section.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` green.
- [ ] **Docs:** extending + user-guide en/zh-tw + CHANGELOG + version bump.
- [ ] **Manual (TTY):** save a memory; confirm the file + index line; start
      a fresh session; ask a related question; confirm the recall
      system-reminder appears in the transcript/logs and the model uses it;
      backdate a file's mtime and confirm the freshness caveat renders.

---

## 9. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `internal/memdir/frontmatter.go` | **New** — flat frontmatter parser | Task 1 |
| `internal/memdir/memtype.go` | **New** — `MemoryType` + parse | Task 1 |
| `internal/memdir/age.go` | **New** — age/freshness (port `memoryAge.ts`) | Task 1 |
| `internal/memdir/scan.go` | **New** — `ScanMemoryFiles` + manifest | Task 1 |
| `internal/memdir/memdirpaths.go` | **New** — dir paths + slug/safety | Task 1 |
| `internal/memdir/index.go` | **New** — read/upsert/rebuild `MEMORY.md` | Task 2 |
| `internal/memdir/migrate.go` | **New** — legacy → typed migration | Task 7 |
| `internal/memdir/recall/recall.go` | **New** — `FindRelevant` side-query | Task 3 |
| `internal/memdir/memdir.go` | Edit — `Snapshot` fields; `Load` reads indexes + dirs | Task 5.1 |
| `internal/memdir/section.go` | Keep (migration only); drop from steady-state | Task 7 |
| `internal/tools/memory/memory.go` | Rewrite — one `remember` tool; reuse `MemoryDiff` | Task 4 |
| `pkg/tools/name.go` | Edit — add `REMEMBER`; remove old two | Task 4 |
| `pkg/toolset/tags.go` | Edit — `remember` tag row | Task 6 |
| `internal/agent/profiles.go` | Edit — thread `UserMemoryIndex`; `memory.Names()` now `remember` | Task 5.2, 6 |
| `internal/agent/agent.go` | Edit — per-turn recall hook + `surfacedMemories` set | Task 5.3 |
| `internal/agent/sysprompt/main_agent.go` | Edit — `userMemoryIndexSection`; rewrite `autoMemoryGuidanceSection` | Task 5.2, 8 |
| `pkg/config/config.go` | Edit (optional) — `MemoryRecallModel`, `enable_memory_recall` | Task 3, §5.5 |
| `cmd/evva/main.go` | Edit — call `MigrateLegacyMemory` at boot | Task 7 |
| `pkg/version/version.go` | Edit — bump | Task 9 |
| `CHANGELOG.md` | Edit — Added/Changed/Removed block | Task 9 |
| `docs/extending.md`, `docs/user-guide/{en,zh-tw}/user-guide.md` | Edit | Task 9 |

---

## 10. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — frontmatter/types/age/scan/paths | ~300 | 3 h |
| Task 2 — index read/upsert/rebuild | ~120 | 1.5 h |
| Task 3 — relevance retriever + fake-client tests | ~200 | 3 h |
| Task 4 — `remember` tool + description | ~200 | 2.5 h |
| Task 5 — load + injection + per-turn recall hook | ~150 | 3 h |
| Task 6 — activation + tags | ~20 | 20 min |
| Task 7 — migration + tests | ~150 | 2.5 h |
| Task 8 — typed-memory prompt content | ~120 prose | 2 h |
| Task 9 — docs + changelog + version | ~80 | 1 h |
| Tests across the above | ~500 | 4 h |

Total: ~1,500–1,800 LOC, ~22–26 hours of focused engineering. The single
biggest risk is **Task 5.3 cache discipline**; the single biggest prose
lever is **Task 8**. Larger than ConfigTool (v1.5) because it adds a new
data layer + an LLM side-query path, not just a tool over existing setters.
