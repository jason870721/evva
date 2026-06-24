# PRD — Parallel Fan-out Reconciliation — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD — a wave-sized minor (claims its minor at planning
> per `CLAUDE.md` → Release workflow).
> **Roadmap source:** `CLAUDE.md` → Vision (*"one runtime, many personas"*);
> extends the existing worktree-isolated subagent path (`internal/agent/spawn.go`).
> **Reference source:** none — evva-native (no `ref/src/` analog). Rides the
> 2026 "parallel agents in isolated environments → review → merge/PR" pattern
> (Cursor *Build in Parallel* / Background Agents; Codex parallel). Design
> follows the existing `mode.WorktreeController` + `Spawn` isolation seam.

---

## 1. TL;DR — what this phase actually is

evva **already** fans work out in parallel and isolates it. The Agent tool
runs subagents concurrently (`internal/tools/meta/agent.go:56` — *"they
execute in parallel"*), in the background (`async_mode`,
`agent.go:122`), and each can run inside its **own git worktree**
(`isolation: "worktree"`, `agent.go:123`; provisioned in
`spawn.go:56-71` via `mode.CreateForSubagent`, cleaned up by
`finalizeIsolation`, `spawn.go:157`). So the **dispatch** half of "parallel
fan-out" shipped already.

What's missing is the **integrate** half. When N isolated workers finish, the
runtime leaves N worktrees on disk and surfaces each one's
`worktree_path` + `worktree_branch` as plain text in the tool result
(`spawn.go:167`). From there the lead agent is on its own: there is **no
merge-back primitive** (the `exit_worktree` action enum is exactly
`["keep","remove"]`, `internal/tools/mode/worktree.go:211` — keep leaves the
branch on disk, remove deletes it; *neither integrates the work*), and **no
collect/review surface** to see all live worktrees and their diffs at once.
The model has to hand-drive `git merge` through `bash`, branch by branch,
with no conflict-aware tooling.

**This phase ships the integrate half:**

1. A **`merge` action** on the worktree path: merge a worktree branch back
   into its base branch, report conflicts structurally, and (on success)
   tear the worktree down — the reconcile counterpart to today's
   keep/remove.
2. A **`worktree_list`** collect surface: enumerate every live worktree
   (subagent-spawned and user-entered) with branch, diff stat, and ahead/behind
   counts, so the lead can review the fan-out before integrating.
3. (Stretch) A thin **fan-out helper** that dispatches N isolated workers on
   slices of one task and returns a consolidated review block, so the common
   "split → isolate → reconcile" loop is one call, not N + manual git.

The whole phase reuses the existing worktree lifecycle (`runGit` helper,
`WorktreeSession`, `CleanupSubagentWorktree`) — it adds an integrate verb to
machinery that already creates, tracks, and tears down worktrees. It is
**not** the Veronica swarm: no mailbox, no persistent members, no store. This
is ephemeral task-parallelism for the solo agent — the swarm is a standing
team; this is a work crew that disbands when the job merges.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 Worktree lifecycle — `internal/tools/mode`

- `worktree_controller.go:24 WorktreeController` — the narrow seam the
  enter/exit tools call (`Workdir` / `SwitchWorkdir` / `WorktreeSession` /
  `BeginWorktreeSession` / `EndWorktreeSession`). The merge action calls the
  same controller; **no new agent coupling needed**.
- `WorktreeSession` carries `Path`, `Branch`, `OriginalWorkdir`
  (`worktree_controller.go:47`) — exactly the three facts a merge needs
  (what to merge, onto what, where the base lives).
- `worktree.go` already shells git through a `runGit(ctx, repoRoot, …)`
  helper and already computes **commits beyond the merge-base**
  (`worktree.go:428`) and **uncommitted/unmerged detection** (the
  `discard_changes` guard, `worktree.go:196`). The merge action reuses both:
  the ahead-count to decide if there's anything to merge, the dirty-check to
  refuse merging an unclean tree.
- `CreateForSubagent` / `CleanupSubagentWorktree` (`spawn.go:63,161`) — the
  subagent-side provision/teardown. Merge slots between them: integrate, then
  let the existing cleanup remove the now-merged worktree.

### 2.2 Subagent dispatch — `internal/agent/spawn.go`

- `Spawn` (`:40`) already accepts `req.Isolation == "worktree"` and
  `req.AsyncMode`, registers the child as an `agentDaemon` in the parent's
  `DaemonState` (`:96-102`), and async children deliver their terminal report
  on a later turn (`:109-124`). The fan-out helper (Task C) is a thin loop
  over this — it invents no new spawn path.
- `finalizeIsolation` (`:157`) is where a finished isolated subagent's
  worktree is preserved-or-removed and its path/branch surfaced. This is the
  natural place to also record the worktree into the collect surface (Task B).

### 2.3 Daemon catalog — the existing review substrate

Subagents already appear in `DaemonState` (`spawn.go:96`), surfaced via
`daemon_list`. `worktree_list` is the worktree analog: the catalog of *live
isolated workspaces*, not *live processes*. Where they overlap (an async
subagent still running in a worktree), `worktree_list` cross-references the
daemon id so the lead sees "branch X is still being written by subagent Y."

---

## 3. Goal & acceptance criteria

**Goal:** after fanning work out into isolated worktrees, the lead agent can
review every branch and integrate the good ones back to the base — with
conflict handling — without hand-driving git through bash.

Ship is complete when **all** of these pass:

- **A1 — Merge action.** `exit_worktree` (or the worktree tool) accepts
  `action: "merge"`. With a session active and its branch ahead of the base,
  it runs the merge into `OriginalWorkdir`'s branch and reports the result.
- **A2 — Clean merge tears down.** A conflict-free merge merges the branch,
  then removes the worktree + branch (the keep/remove machinery), and reports
  files changed + commits integrated.
- **A3 — Conflict is structural, not a crash.** A merge with conflicts
  **aborts** the merge (`git merge --abort`), leaves the worktree intact, and
  returns a `tools.Result` listing the conflicted paths — the agent gets one
  actionable message, not a raw git dump. No partial-merge state is left
  behind.
- **A4 — Refuses an unclean source.** Merging a worktree with uncommitted
  changes is refused with a clear message (mirror the `discard_changes`
  guard, `worktree.go:196`) — the worker must commit first.
- **A5 — Nothing to merge is a no-op.** A worktree with zero commits beyond
  the merge-base reports "no changes to integrate" and (optionally) removes
  the empty worktree; it never errors.
- **A6 — `worktree_list`.** Lists every live worktree under
  `<repo>/.evva/worktrees/` with `path`, `branch`, base branch, ahead/behind
  counts, dirty flag, and (if still owned by a running subagent) the daemon
  id. Empty list when none — never errors.
- **A7 — Subagent worktrees are listable + mergeable.** A worktree left by a
  finished `isolation:"worktree"` subagent (`finalizeIsolation` preserved it)
  appears in `worktree_list` and can be merged by id/branch from the **lead**
  agent — closing the loop from `spawn.go:167`'s "so the user can inspect or
  merge."
- **A8 — Main-agent only.** Merge/list/fan-out are root-agent tools, stripped
  from subagent profiles (subagents already can't spawn — `spawn.go:41`;
  reconcile follows the same one-layer invariant).
- **A9 — Fan-out helper (stretch).** A single call dispatches N isolated
  workers on caller-supplied slices, waits (or returns async handles), and
  emits a consolidated block: per-worker branch + diff stat + status. Degrades
  to "dispatch only" if reconciliation is deferred.
- **A10 — Tests.** Merge clean/conflict/unclean/empty paths against a real
  temp git repo; `worktree_list` enumeration incl. ahead/behind; subagent
  worktree round-trip (spawn isolated → list → merge); subagent isolation of
  the new tools.
- **A11 — Docs + version + changelog.** User-guide (en + zh-tw) "parallel
  work" section; `CHANGELOG.md`; `pkg/version/version.go`.

---

## 4. Work breakdown (ordered)

### Task 1 — `merge` action on the worktree tool

`internal/tools/mode/worktree.go`:

- Extend the action enum to `["keep","remove","merge"]` (`:211`) and the
  schema description.
- `executeMerge(ctx, sess, base)`:
  1. Resolve base branch from `sess.OriginalWorkdir` (the branch checked out
     there when `enter_worktree`/`CreateForSubagent` ran).
  2. Refuse if the worktree tree is dirty (reuse the dirty detection behind
     `discard_changes`, `:196`) → A4.
  3. Ahead-count via the existing merge-base logic (`:428`); 0 → A5 no-op.
  4. `runGit(ctx, repoRoot, "merge", "--no-ff", sess.Branch)` **executed from
     the base worktree** (`OriginalWorkdir`), not the child worktree.
  5. On non-zero exit: parse `git` conflict output (or
     `git diff --name-only --diff-filter=U`), `git merge --abort`, return the
     conflicted paths (A3). On success: capture `--stat`, then run the
     existing remove path to tear the worktree down (A2).

Keep `executeMerge` a sibling of the keep/remove handlers — same controller,
same `runGit`, same teardown. The only genuinely new code is conflict parsing
+ abort.

### Task 2 — `worktree_list` collect surface

New tool (or a `list` action) in `internal/tools/mode`:

- Enumerate `git worktree list --porcelain` filtered to
  `<repo>/.evva/worktrees/`.
- For each: branch, base, `git rev-list --left-right --count base...branch`
  (ahead/behind), dirty flag, and a cross-ref into `DaemonState` (the
  `agentDaemon` whose worktree path matches) so a still-running worker is
  flagged "in progress."
- Format as a compact table (mirror `daemon_list`'s renderer). Read-only,
  default-allow.

### Task 3 — Record subagent worktrees into the surface

`internal/agent/spawn.go` `finalizeIsolation` (`:157`): when a worktree is
**preserved** (the child made changes), it already logs path+branch — also
ensure it's discoverable by `worktree_list`. Since `worktree_list` reads
`git worktree list` live, this is mostly free; the only addition is keeping
the daemon→worktree cross-ref (record `sess.Path` on the `agentDaemon`) so
A6's "owned by subagent Y" column works.

### Task 4 — Register + gate (main-agent only)

- `pkg/tools/name.go`: `WORKTREE_LIST ToolName = "worktree_list"` (merge is an
  action on the existing `exit_worktree`, no new name).
- `internal/toolset/builtins.go`: factory for `worktree_list`.
- `internal/agent/profiles.go`: add to the **Main** active/deferred list only;
  confirm subagent profiles (`subagentProfile`, `spawn.go:186`) exclude it
  (A8).
- Permission: read-only `worktree_list` → allow; `merge` inherits
  `exit_worktree`'s gate (it mutates the base branch — it should prompt unless
  bypassed, same class as a write).

### Task 5 — Fan-out helper (stretch; ship behind A9)

A `dispatch_parallel`-style meta tool (or a documented prompt recipe) that:
loops `Spawn(Isolation:"worktree", AsyncMode:true)` over N slices, then on a
later turn collects via `worktree_list` and emits a review block. Keep it thin
— it composes Tasks 1-2 and the existing spawn; if it slips, the must-have
(merge + list) still delivers the integrate half. Decide at build time whether
this is a tool or just a documented pattern in the Agent tool description.

### Task 6 — Docs + version + changelog

- `docs/user-guide/{en,zh-tw}/user-guide.md` — "Running work in parallel":
  isolate → review with `worktree_list` → integrate with `merge`, conflict
  handling, the swarm-vs-fan-out distinction.
- `CHANGELOG.md` `### Added`; `pkg/version/version.go`.

---

## 5. Design decisions & risks

### 5.1 — Merge is a verb on the existing tool, not a new subsystem

The temptation is a "reconciliation engine." Resist it. The worktree lifecycle
already exists; this phase adds **one verb** (`merge`) and **one read**
(`worktree_list`). Everything else — provisioning, branch tracking, teardown,
dirty detection, ahead-counting — is reused from `worktree.go` and `spawn.go`.
The blast radius is the merge conflict path and the list renderer.

### 5.2 — Conflicts abort, never half-apply

A6's structural-conflict contract is the core safety property. A merge that
hits conflicts must `git merge --abort` and report — it must never leave the
base branch in a half-merged state for the model to "figure out." The agent
then decides: re-spawn the worker with conflict context, merge a different
branch first, or escalate to the user. The tool's job is a clean yes/no +
the conflicted paths, not auto-resolution.

### 5.3 — Merge runs from the base, not the child

The merge must execute in `OriginalWorkdir` (the base checkout), pulling the
child branch in — not the reverse. Running it from the child worktree would
merge the base *into* the throwaway branch, which is backwards. The
`WorktreeSession.OriginalWorkdir` field already records the right cwd.

### 5.4 — Not the swarm (and why both exist)

A reviewer will ask "isn't this Veronica?" No. The swarm is a **standing
team**: persistent members, a mailbox, a store, leader/worker protocol,
survives restarts. Fan-out is a **transient crew**: ephemeral worktrees, no
persistence, disbands on merge. The swarm is for an ongoing mission (a trading
desk); fan-out is for "split this refactor across 4 files, then reconcile."
Different lifetimes, different machinery, no overlap — fan-out reuses the
solo agent's worktree tools; the swarm reuses none of them.

### 5.5 — Sequential-by-default integration

Even with N branches ready, merge them **one at a time** (the agent calls
`merge` N times, re-checking conflicts after each), not in a batch. Each merge
shifts the base, so a batch "merge all" would compute conflicts against a
stale base. The per-call model keeps each integration honest against the
current base. A batch convenience can come later if it proves needed.

### 5.6 — Worktree explosion / cleanup hygiene

N parallel isolated workers = N worktrees + N branches under
`<repo>/.evva/worktrees/`. `worktree_list` makes them visible; merge tears
down on success; the existing `CleanupSubagentWorktree` auto-removes
no-change ones. The residual risk is *abandoned* worktrees (worker crashed,
lead never merged). Mitigate with a `worktree_list` "stale" flag (no owning
daemon + older than session) and lean on the existing `remove` action — don't
build a GC daemon for v1.

---

## 6. Out of scope

- **Auto-conflict-resolution / 3-way merge UI** — the tool reports conflicts;
  resolution is the agent's (or user's) job.
- **Cross-repo / cross-space fan-out** — single repo, single base, like the
  existing worktree path.
- **PR creation** — opening a GitHub PR per branch is a fast-follow (compose
  `merge`'s absence with `gh` via bash); core ships local merge first.
- **Persistent parallel teams** — that's the swarm (§5.4).
- **A worktree GC daemon** — `worktree_list` + manual `remove` first (§5.6).

---

## 7. Verification checklist (PR gate)

- [ ] **Task 1:** `merge` action — clean (A2), conflict→abort+report (A3),
      unclean-refuse (A4), empty no-op (A5), against a real temp git repo.
- [ ] **Task 2:** `worktree_list` enumerates path/branch/ahead-behind/dirty +
      daemon cross-ref (A6); empty list is clean.
- [ ] **Task 3:** preserved subagent worktree appears in the list with its
      owning daemon id.
- [ ] **A7:** spawn `isolation:"worktree"` → finish → `worktree_list` → `merge`
      round-trip from the lead.
- [ ] **A8:** subagent profiles exclude `worktree_list`/merge (gating test).
- [ ] **Task 5 (if shipped):** fan-out helper dispatches N + collects a review
      block (A9).
- [ ] `go build/vet/test ./...` green.
- [ ] **Manual (TTY):** spawn 2 isolated workers editing different files →
      `worktree_list` shows both → `merge` each → base branch has both changes,
      worktrees gone. Then force a conflict (two workers edit the same line) →
      second `merge` reports the conflicted path and aborts.

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `internal/tools/mode/worktree.go` | Edit — `merge` action + `executeMerge` + conflict parse/abort | Task 1 |
| `internal/tools/mode/worktree_list.go` | **New** — collect/enumerate tool | Task 2 |
| `internal/tools/mode/*_test.go` | **New/Edit** — merge + list tests | Task 1,2,10 |
| `internal/agent/spawn.go` | Edit — record worktree path on the agentDaemon (cross-ref) | Task 3 |
| `pkg/tools/name.go` | Edit — `WORKTREE_LIST` constant | Task 4 |
| `internal/toolset/builtins.go` | Edit — `worktree_list` factory | Task 4 |
| `internal/agent/profiles.go` | Edit — register on Main only | Task 4 |
| `pkg/permission` defaults | Edit — `worktree_list → allow` | Task 4 |
| `internal/tools/meta/agent.go` (or new) | Edit (optional) — fan-out helper/recipe | Task 5 |
| `pkg/version/version.go`, `CHANGELOG.md`, user-guide en/zh-tw | Edit | Task 6 |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — merge action + conflict handling | ~180 | 3 h |
| Task 2 — worktree_list | ~140 | 2 h |
| Task 3 — daemon cross-ref | ~30 | 30 min |
| Task 4 — register + gate | ~50 | 45 min |
| Task 5 — fan-out helper (stretch) | ~150 | 2.5 h |
| Task 6 — docs + changelog + version | ~70 | 1 h |
| Tests | ~280 | 3 h |

Total (must-have, Tasks 1-4+6+tests): ~750 LOC, ~10 h focused. With the
stretch fan-out helper: ~900 LOC, ~12-13 h. The only subtle work is git
conflict parsing/abort (Task 1) — everything else is enumerate-and-format over
machinery evva already owns.
