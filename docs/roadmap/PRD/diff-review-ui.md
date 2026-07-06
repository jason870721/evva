# PRD — Diff Review UI (hunk-level edit review + propose mode) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W22**, suggested horizon H2
> per [../long-range.md](../long-range.md) §3b. Pairs naturally with
> the H2 TUI waves and with git-intelligence's hunk machinery (W25 —
> shared engine, see §3).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> evva's edit approval today is binary and immediate: approve this
> edit call, or don't. Operators reviewing agent work actually think
> in *changesets* — "show me everything you want to do to fix this
> bug, let me pick". IDE agents (Cursor, Windsurf) made
> propose-then-review their core interaction; terminal evva can have
> the same rigor without the IDE.
> **Reference source:** the fs-edit gate + diff rendering that already
> exist (`docs/roadmap/design/fs-edit-gate-parity.md` context); the
> propose-mode design is evva-native.

---

## 1. TL;DR

Two review upgrades on the existing permission/diff plumbing:

1. **Hunk-level decisions on big edits (always available).** When an
   edit/write's diff exceeds a triviality threshold, the approval
   prompt upgrades from approve/deny to a hunk navigator: per-hunk
   accept/reject (rejected hunks are surgically dropped from the
   applied change), with the model informed exactly which hunks landed
   ("hunks 1,3 applied; hunk 2 rejected by the operator") so its world
   model stays true.
2. **Propose mode (opt-in session state).** `/propose on`: edits stop
   applying immediately — they accumulate as a **pending changeset**
   (rendered live in a review panel: files, hunks, running diff-stat)
   while the agent keeps working against the *virtual* result (reads
   see pending content overlaid — the audit's hard question, §3). At a
   natural boundary the operator reviews the whole changeset
   hunk-by-hunk, applies the accepted subset atomically, and the
   session continues with an honest reconciliation note.

Mode 2 is the trust unlock for bigger autonomous swings: "go try the
refactor, I'll review the result as one piece" — without giving up the
kill-switch granularity of mode 1.

## 2. Goals / non-goals

### Goals

- Hunk navigator overlay: unified-diff render with per-hunk state
  (accept/reject/undecided), keyboard-driven, syntax-highlight reuse
  from the existing diff rendering; works identically for the
  single-edit case and the changeset case.
- Surgical application: accepted-hunk subsets apply via the same
  patch-application engine git-intelligence needs (GIT-3) — build
  once, share (whichever wave lands first owns it; the other consumes).
- Truthful model feedback: every partial decision produces a tool
  result stating precisely what applied — never a silent divergence
  between the model's belief and the file.
- Propose mode: pending-changeset store (per-file layered content),
  read-overlay so the agent sees its own pending work, bash guard
  (commands that would read mutated files get a visible warning banner
  in propose mode — they see the *disk*, not the overlay), review
  panel, atomic apply of the accepted subset, reconciliation note.
- Checkpoint integration: applying a changeset creates one checkpoint
  (one rewind unit — matching the operator's mental "that batch").
- Config: triviality threshold (lines), propose-mode default
  off, per-session toggle.

### Non-goals (this wave)

- In-editor review (ACP's lane — W11 surfaces evva edits as editor
  diffs already; this wave is the *terminal* story).
- Semantic conflict analysis between pending hunks (textual layering
  only; overlapping edits to the same region follow last-write-wins
  within the changeset, visibly).
- Propose mode across bash mutations (only fs-tool edits are
  virtualized; bash writes bypass — hence the warning banner, and
  docs are blunt about the boundary).
- Multi-operator review workflows (single operator; team review is
  the CI/PR lane).

## 3. Design sketch

- **The overlay question (audit ticket zero):** propose mode requires
  reads to see pending content. The clean seam: the fs read path
  consults the pending store before disk (same layering the LSP sync
  will need — a pending edit should also feed `didChange` so
  diagnostics reflect the virtual state; if that proves heavy,
  diagnostics-on-pending degrades to apply-time). If the read path
  has no single seam, ticket zero builds one — which ARC-2 wants
  anyway.
- **Pending store:** per-file base-hash + ordered edit layers;
  materialization produces the virtual content and the display diff.
  Base-hash drift (file changed on disk under a pending edit — human
  editor, bash) flags the file's pending layers as conflicted for
  review-time resolution, mirroring worktree-merge honesty.
- **Hunk identity:** stable hunk ids across re-renders (hash of
  context+content) so decisions survive panel refreshes and model
  follow-up edits to *other* files.
- **Reconciliation note:** one message summarizing applied/rejected/
  conflicted per file — compact, model-facing, and the trigger for
  the model to re-read anything rejected.

## 4. Work items

- **DRV-1 — Patch/hunk engine (shared with GIT-3).** Hunk inventory,
  stable ids, subset application, property tests. *Accept:* random
  accepted-subsets of fixture diffs apply cleanly; union/complement
  invariants hold.
- **DRV-2 — Hunk navigator overlay.** Render, navigation, decisions,
  threshold trigger on single edits. *Accept:* an over-threshold edit
  prompts with the navigator; a rejected middle hunk yields the
  correct partial file and the truthful tool result.
- **DRV-3 — Pending store + read overlay.** Layered content,
  base-hash drift detection, read-path integration. *Accept:* in
  propose mode the agent reads its own pending edit back; disk file
  untouched; underlying-file drift flags conflicts.
- **DRV-4 — Propose-mode session state.** `/propose on|off`, live
  review panel, bash warning banner. *Accept:* toggling round-trips;
  panel tracks a scripted multi-file editing burst live.
- **DRV-5 — Review + atomic apply + reconciliation.** Full-changeset
  navigator, subset apply, single checkpoint, reconciliation note,
  LSP sync on apply. *Accept:* end-to-end fixture — 5 files, mixed
  decisions, one conflicted — applies atomically, one checkpoint,
  correct note, diagnostics fire post-apply.
- **DRV-6 — Docs + changelog.** User-guide (en + zh-tw): the two
  modes, the bash boundary in propose mode, threshold tuning, rewind
  semantics.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Read-overlay misses a path and the agent sees stale disk | ticket-zero seam audit; a single choke point or the feature re-scopes to review-only (no virtual reads) with reduced ambition |
| Propose mode + bash produces confusing hybrid state | explicit banner, blunt docs, and the drift detector catches disk changes under pending layers |
| Hunk rejection creates syntactically broken files | that's the operator's call by design — but the LSP diagnostics fire immediately on apply, closing the loop; navigator shows hunk interdependency warnings when hunks touch adjacent context |
| Review fatigue (operators rubber-stamp) | thresholds keep small edits frictionless; propose mode is opt-in per session — the default flow stays exactly as shipped |

## 6. Open questions

1. Should propose mode auto-suggest at session start for
   `refactor`-shaped asks (prompt-classifier hint), or stay purely
   manual? Leaning manual v1.
2. Pending-changeset persistence across session death (resume with
   pending work?) — leaning yes via session-tree, flagged prominently
   on resume.
3. Hunk-level *editing* (operator tweaks a hunk before accepting) —
   powerful, complex; defer to v2 of the panel?
