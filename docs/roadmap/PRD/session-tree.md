# PRD — Session Tree (resume, fork, browse, export) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W7 / v1.17** per
> [../long-range.md](../long-range.md).
> **Roadmap source:** 2026-07-06 long-range planning pass. Session
> continuity (`--continue`/`--resume`, forking, session pickers) is a
> baseline expectation set by Claude Code and every 2026 CLI agent; evva
> has the *persistence* layer (session snapshots power swarm crash-resume
> and checkpoint/rewind) but no *user-facing* session lifecycle for the
> solo TUI.
> **Reference source:** `ref/src` session/resume surfaces — port the UX
> shape, not the storage (evva's snapshot format already exists).

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

- **SES-1 — Catalog.** Self-registration on persist, rebuild-by-scan,
  auto-title (cheap upgrade at close). *Accept:* deleting the catalog and
  rescanning reproduces it; titles appear without blocking close.
- **SES-2 — `evva resume` / `--continue` / `resume <id>`.** Picker UI,
  cwd-affinity, thaw path with tool-state re-establishment. *Accept:* a
  session with a background daemon resumes with the daemon marked dead and
  the model informed; LSP re-initializes; history renders fully.
- **SES-3 — Fork.** `/fork` + `--fork`, parent tracking, checkpoint span
  rules. *Accept:* forking at turn N yields a child whose rewind cannot
  cross the fork point; parent remains untouched and resumable.
- **SES-4 — `/sessions` overlay.** Tree render, pin/delete, open-in-new.
  *Accept:* tree shows parent/child indentation; delete removes snapshot +
  catalog row after confirm.
- **SES-5 — HTML export.** Self-contained render, collapsed tool calls,
  cost footer. *Accept:* exported file opens offline, contains zero
  external requests, and passes the redaction scan (SEC rules) in a test.
- **SES-6 — Retention.** GC policy knobs, `sessions prune`, pin exemption.
  *Accept:* prune respects pins and caps; dry-run mode lists victims.
- **SES-7 — Docs + changelog.** User-guide (en + zh-tw); note the
  interaction with checkpoints and the mid-tool non-goal explicitly.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Thawed tool state lies (dead daemons, moved files) | explicit terminated/dead markers + a resume-time system note listing what could not be re-established — honesty over simulation |
| Catalog/snapshot drift | catalog is derived and rebuildable; never the source of truth |
| Fork explosion clutters the picker | tree grouping + GC caps + pins; children GC with parents by default |
| Export leaks | export runs the SEC scrubber unconditionally, even if live redaction was off (§SES-5 accept) |

## 6. Open questions

1. Session ids: keep snapshot-native ids or mint short human-typeable ones
   (`ev-7f3k`)? Picker UX favors short.
2. Should `--continue` auto-resume without a picker when exactly one recent
   session matches the cwd (Claude Code behavior), or always confirm?
3. Export privacy default: include tool results expanded or collapsed-only
   with a `--full` flag?
