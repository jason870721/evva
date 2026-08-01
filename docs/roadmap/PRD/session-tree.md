# PRD — Session Tree (resume, fork, browse, export)

> **Audience:** senior engineers implementing this wave.
> **Status:** ✅ **BUILT** — SES-1..7 implemented 2026-08-01.
> **Audited:** 2026-08-01 at `dev @ db47629` (CTX `c02eb8e` + MEM `db47629`
> merged). Audit pass per [../long-range.md](../long-range.md) §1 step 2 —
> **read §0 first.** Half the draft's premise was already shipped and one
> work item was measured out of existence, so §1–§3 below are preserved as
> the historical concept text.
> **Target release:** **v1.19** (claimed in `CLAUDE.md` at pickup). The
> draft's tentative v1.17 slot was taken by CTX while H1 closed.
> **Roadmap source:** 2026-07-06 long-range planning pass.
> **Reference source:** `ref/src` session/resume surfaces — port the UX
> shape, not the storage (evva's snapshot format already exists).

---

## 0. Audit corrections (2026-08-01)

The draft's premise is **half true**, and the half that is false is the
half it leads with. The persistence layer is not merely present — the
*user-facing picker is already shipped*. What is genuinely missing is
everything that happens **outside** a running TUI, plus fork and export.

**1 — "no session list" is false.** `/resume` ships a paginated picker
(`pkg/ui/bubbletea/components/overlays/resume.go:33`, 10 rows/page, ←→
paging, mtime-desc), registered in both UIs
(`pkg/ui/bubbletea/app/root.go:900`, `pkg/ui/lp/app/root.go:531`) and
backed by `Controller.ListSessions` → `session.List`
(`internal/agent/agent.go:1729`). The `SessionInfo` DTO
(`pkg/ui/ui.go:362`) already carries prompt preview, timestamps, persona,
provider, model, and message count. **`evva eval list -sessions`
(`cmd/evva/eval.go:190`) is even a CLI-side lister.** SES-2 and SES-4 are
therefore *extensions*, not new construction.

**2 — the thaw path is not missing either.** `ResumeSnapshot`
(`internal/agent/agent.go:1002`) already rebuilds the persona, provider and
model with three documented fallbacks (missing persona → `evva`; unknown
provider → default; unknown model → provider's first), re-merges custom
tools, rebuilds the LLM client, re-scopes the checkpoint namespace
(`a.checkpoints.SetSession`), replays the workflow board
(`a.rescopeWorkflow(true)`), and clears the todo store. The TUI's
`transcript.LoadFromMessages` (`transcript.go:206`) renders the rehydrated
history. SES-2's "tool-state re-establishment" is **already the shipped
contract** — the work is reaching it from the command line.

**3 — the real gap is the pre-TUI entry.** `cmd/evva/main.go:71-86`
dispatches exactly five subcommands (`update`, `service`, `swarm`,
`mcp-serve`, `eval`) and then falls through to flag parsing. There is no
`resume`, no `--continue`. Once evva is running you can reach yesterday's
session; from a fresh terminal you cannot. That asymmetry is the wave.

**4 — SES-1's catalog is not justified. Cut it.** The catalog exists to
make enumeration cheap. Measured on a real store — **93 sessions, 14 MB,
14 workdir slugs** — parsing *every snapshot on the machine in full* takes
**110 ms**; the single-workdir case the picker does today tops out at
22 ms. A derived catalog buys ~100 ms and costs a second store plus the
drift class the draft's own §5 lists as a risk. Its actual payload —
parent id, title, pinned — belongs in the `Snapshot` envelope, which is
already the source of truth and already proved (v1.17's `Pins`) that
optional fields land without a `SnapshotVersion` bump. What *is* worth
taking is the cheap half: listing decodes a **header struct that omits
`Session`** (66 ms vs 107 ms measured), which matters because
cross-workdir listing is new.

**5 — SES-3's headline invariant is free.** The draft specifies "rewind
inside a fork only reaches its own span" as a rule to implement.
Checkpoints are namespaced per session id at
`<workdir>/.evva/checkpoints/<session-id>/`
(`internal/checkpoint/checkpoint.go:9`), so a fork's new id starts with an
empty namespace **by construction**. There is nothing to enforce; there is
a test to write.

**6 — SES-4 as drafted duplicates a shipped overlay.** A `/sessions`
overlay that lists sessions next to a `/resume` overlay that lists
sessions is two surfaces for one job, in a catalog already at 15 commands
(`slash/panel.go:43`). Pin / delete / fork / cross-workdir folded into
`/resume` instead — same rows, more verbs.

**7 — auto-titling by LLM is cut, and replaced.** The draft wants "a cheap
model call at session close". There is no session-close hook — a killed
terminal is the normal exit — so the one path that would produce a title
is the one that never runs. The picker *already* renders the first user
prompt as the de-facto title. Shipped instead: a `Title` field with an
explicit `/title` setter, defaulting to the existing preview. Deterministic,
free, and it names the sessions the operator actually cares about.

**8 — retention defaults to OFF, unlike checkpoints.** `checkpoint` prunes
automatically (`maxSessionDirs = 30`, `Retention{MaxCount, MaxAge}` —
`checkpoint.go:57,97`) because before-images are derived data. A session
transcript is the user's own writing. `evva sessions prune` is explicit,
dry-run-first, and pin-exempt; the config caps exist but ship at 0
(unlimited).

**9 — the ARC rider's premise is wrong.** long-range §5 parks "session
store backend interface (jsonl today, pluggable later)" on this wave. The
store is **one JSON file per session** (`store.go:1-13`), not jsonl — and
with the catalog cut there is no new store to abstract. The rider stays
open for v2.0 rather than riding W7 on a false description.

**Net:** SES-1 shrinks to snapshot fields + a header decode, SES-2 becomes
a CLI seam over shipped machinery, SES-3 and SES-5 and SES-6 are genuinely
new, SES-4 merges into an existing overlay. §4 below is annotated with what
each item became.

---

## 1. TL;DR

evva can already freeze and thaw a session — the swarm resumes members
after a crash and `/rewind` restores checkpoints — but a solo TUI user who
closes the terminal loses the thread: there is no `evva resume`, no session
list, no way to branch an experiment off yesterday's conversation, and no
way to hand a transcript to a teammate.

This wave gives sessions a **lifecycle and a family tree**:

- `evva resume` (picker) / `evva --continue` (most recent in this cwd) /
  `evva resume <id>` — thaw a session with full history, workdir, and
  tool state re-established.
- **Fork:** any checkpoint or session becomes the parent of a new branch
  (`/fork` in-session, `evva resume <id> --fork` from outside) — the
  experiment pattern rewind already enables, without destroying the
  original timeline.
- **Catalog:** a per-machine session catalog (id, auto-title, cwd, model,
  parent, timestamps, token totals) backing a `/sessions` overlay and the
  CLI picker.
- **Export:** `evva export <id>` renders a self-contained HTML transcript
  (no external assets) with tool calls collapsed, costs footnoted, and
  secrets already scrubbed (W3 dependency).

## 2. Goals / non-goals

### Goals

- Catalog as the single enumeration surface: sessions self-register on
  first persist; catalog rows are tiny (metadata only) and repairable by
  rescanning snapshot files.
- Auto-titling: first user prompt truncated → upgraded once by a cheap
  model call at session close (same do-it-once idiom as compaction
  summaries).
- Fork semantics: parent id recorded; the tree renders in `/sessions`
  (indented children). Forks share nothing mutable — history is copied at
  the fork point.
- Retention: config-driven GC (age + count caps), `evva sessions prune`,
  pinned sessions exempt.
- Export: one HTML file, embedded CSS, theme-aware, tool results
  collapsible, redaction guaranteed upstream.

### Non-goals (this wave)

- Cross-machine session sync or cloud storage.
- Multi-user simultaneous attach (EX-13 spike).
- Resuming *mid-tool* (a resumed session always starts at an iteration
  boundary; in-flight tool state is not reconstructed).
- Swarm session UX changes — members already resume via the service; this
  wave is the solo TUI story.

## 3. Design sketch

- **Storage:** reuse the existing snapshot format as the source of truth;
  the catalog (`<EVVA_HOME>/sessions/catalog.jsonl` or similar — audit
  decides placement) is derived, append-mostly, and rebuildable. No new
  serialization format.
- **Resume:** thaw = load snapshot → replay nothing (history is data, not
  effects) → re-establish tool state (workdir checks, LSP re-init, daemon
  reattach-or-mark-dead) → print a one-line "resumed from …" system note.
  Tools whose backing processes died render as terminated in history —
  same contract the swarm's crash-resume already honors.
- **Fork:** copy-on-fork of the history prefix; checkpoints before the fork
  point belong to the parent (rewind inside a fork only reaches its own
  span — keeps checkpoint/rewind's invariants intact).
- **`/sessions` overlay:** read-only list + tree, same snapshot-at-open
  idiom as `/cost`; actions limited to open-in-new / pin / delete
  (delete confirms).
- **cwd affinity:** `--continue` scopes to the current directory by
  default; the picker shows all, grouped by cwd.

## 4. Work items

> Each item carries **→ built as** — what the audit turned it into. Where
> the two differ, the "→" line is the specification and the paragraph above
> it is the historical draft.

- **SES-1 — Catalog.** Self-registration on persist, rebuild-by-scan,
  auto-title (cheap upgrade at close). *Accept:* deleting the catalog and
  rescanning reproduces it; titles appear without blocking close.
  **→ built as: snapshot fields + a header decode.** No catalog (§0.4).
  `Snapshot` gains `ParentID`, `Title`, `Pinned`, `ForkedFrom`; listing
  decodes a `Header` that omits `Session`; `ListAll` enumerates every
  workdir slug. Title is set by `/title`, defaults to the prompt preview
  (§0.7). *Accept:* listing never allocates message bodies; a snapshot
  written by v1.18 loads unchanged.
- **SES-2 — `evva resume` / `--continue` / `resume <id>`.** Picker UI,
  cwd-affinity, thaw path with tool-state re-establishment. *Accept:* a
  session with a background daemon resumes with the daemon marked dead and
  the model informed; LSP re-initializes; history renders fully.
  **→ built as: the CLI seam only** — the thaw path already ships (§0.2).
  `evva resume` (picker), `evva resume <id>`, `evva -c`/`--continue`
  (newest in this cwd, no prompt). *Accept:* `-c` in a directory with no
  sessions exits cleanly with a message, not an error.
- **SES-3 — Fork.** `/fork` + `--fork`, parent tracking, checkpoint span
  rules. *Accept:* forking at turn N yields a child whose rewind cannot
  cross the fork point; parent remains untouched and resumable.
  **→ built as drafted**, minus the enforcement: the checkpoint namespace
  already guarantees the span rule (§0.5). *Accept:* a fork's checkpoint
  list is empty; the parent's file is byte-identical after the fork.
- **SES-4 — `/sessions` overlay.** Tree render, pin/delete, open-in-new.
  *Accept:* tree shows parent/child indentation; delete removes snapshot +
  catalog row after confirm.
  **→ built as: `/resume` grows verbs** (§0.6). Parent/child indentation,
  `p` pin, `d` delete (confirm, disarmed by any other key), `a`
  all-workdirs toggle. No fork verb in the picker: resume-then-`/fork` is
  the same two keystrokes and shows you what you are branching first.
  *Accept:* a child renders indented under its parent; an orphaned fork
  still renders (as a root) rather than vanishing; delete needs two
  presses on the same row.
- **SES-5 — HTML export.** Self-contained render, collapsed tool calls,
  cost footer. *Accept:* exported file opens offline, contains zero
  external requests, and passes the redaction scan (SEC rules) in a test.
  **→ built as drafted.** `evva export <id> [-o out.html] [-full]`.
  Redaction runs unconditionally, independent of the live `redaction`
  config (§5's export-leak row).
- **SES-6 — Retention.** GC policy knobs, `sessions prune`, pin exemption.
  *Accept:* prune respects pins and caps; dry-run mode lists victims.
  **→ built as drafted, defaulting to OFF** (§0.8). `evva sessions list |
  prune [-days N] [-keep N] [-all] [-apply]`; dry-run is the default and
  `-apply` is required to delete.
- **SES-7 — Docs + changelog.** User-guide (en + zh-tw); note the
  interaction with checkpoints and the mid-tool non-goal explicitly.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Thawed tool state lies (dead daemons, moved files) | already handled by the shipped `ResumeSnapshot` (§0.2) — persona/provider/model fall back with a logged warning, the checkpoint namespace and workflow board re-scope, the todo store clears |
| ~~Catalog/snapshot drift~~ | **retired** — there is no catalog (§0.4). The snapshot is the only store |
| Fork explosion clutters the picker | tree grouping + pins + `evva sessions prune`; an orphaned fork renders as a root rather than disappearing |
| Export leaks | export builds its own redactor and always applies it — the option to skip it does not exist in the API, so no call site can get it wrong |
| Resuming a session from another project | the agent moves to that project's directory (`evva resume` chdirs before construction; the in-TUI path calls `SwitchWorkdir`). A conversation about files that are not under the agent's feet is worse than a directory change the operator asked for |

## 6. Open questions — resolved at build

1. ~~Session ids: keep snapshot-native ids or mint short human-typeable
   ones (`ev-7f3k`)?~~ **Keep the UUIDs; accept any unique prefix.** Ids
   name files on disk and the swarm keys member transcripts by them, so a
   second identifier would need a mapping nobody would maintain. Prefix
   matching gets the ergonomics for free, and an ambiguous prefix reports
   its candidates instead of guessing.
2. ~~Should `--continue` auto-resume without a picker?~~ **Auto-resume, no
   prompt.** `-c` exists precisely to skip the picker; `evva resume` is
   there when a choice is wanted. With no session in the directory it says
   so and starts fresh rather than erroring.
3. ~~Export privacy default: tool results expanded or collapsed?~~
   **Included, collapsed, and truncated at 2 KB, with `-full` for the
   archive.** Omitting results entirely makes the transcript unreadable —
   half the reasoning is in what the tools returned.
