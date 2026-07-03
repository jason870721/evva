# PRD — Swarm Worktree Isolation — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed.
> **Target release:** `v1.9.0` (this wave claims the v1.9 minor per the
> CLAUDE.md wave → minor rule; first cut ships as `v1.9.0-beta.1`).
> **Roadmap source:** swarm gap audit 2026-07-02 — "members share one
> working directory with zero isolation" surfaced as the largest structural
> gap for coding swarms, immediately after the single-agent worktree stack
> (fan-out reconcile, v1.8.2) shipped the machinery this wave reuses.
> **Evaluation provenance:** live-source audit at `dev@91a530e` (v1.8.4)
> on 2026-07-02. All file:line references verified against that commit.
> Reuse target: `internal/tools/mode/worktree.go` and the
> parallel-fanout-reconcile wave (`docs/roadmap/PRD/parallel-fanout-reconcile.md`,
> hardened cross-platform by `27896f3` and `9d55356`).

---

## 1. TL;DR

Every swarm member runs in the SAME working directory. `constructMember`
clones the config per member but the clone keeps the space's `WorkDir`
(internal/swarm/space.go:314); nothing prevents two workers from editing the
same file concurrently, and nothing records whose change won. For a *coding*
swarm — the product's headline use case — that is a correctness hole: parallel
task assignment is exactly what the leader/worker model encourages, and
parallel edits to one checkout race.

The fix is **not** new machinery. The single-agent side already has all of it:

1. **Worktree provisioning** — `enter_worktree` creates
   `<repoRoot>/.evva/worktrees/<slug>` on branch `worktree-<slug>`
   (worktree.go:88,139), and subagent `isolation:"worktree"` provisions one
   and hands the child a **config clone with `WorkDir` overridden to the
   worktree path** (internal/agent/spawn.go:60-71) — no `os.Chdir`, parent
   untouched.
2. **A merge core with an abort-on-conflict contract** —
   `executeMerge` (worktree.go:354) merges `--no-ff` *from the base
   checkout*, refuses a dirty base or an uncommitted source, and on conflict
   runs `git merge --abort` and returns the conflicted paths as an
   actionable result. Never half-applied. This shipped and hardened in the
   fan-out reconcile wave (v1.8.2, Windows fixes in v1.8.3/v1.8.4).

This wave threads that machinery into the swarm: each opted-in member gets a
**persistent, deterministically-named worktree** injected at construction
(the spawn.go clone-override pattern), and the **leader gets one new tool,
`worktree_merge`**, to land a member's committed work on the base branch as
part of task verification. Conflicts come back as structured paths and the
leader bounces the task back to the worker (`verifying → running` is already
a legal transition, internal/swarm/store/tasks.go:80). Opt-in via one
manifest knob; non-coding swarms (trading teams) are untouched.

---

## 2. Goals / non-goals

### Goals

- Two workers editing the same repo concurrently can never corrupt each
  other's work or the operator's checkout: every opted-in member works on
  its own branch in its own worktree.
- The leader integrates work member-by-member with the existing
  abort-on-conflict merge contract; a conflict is a normal, visible task
  outcome (reject-with-note), not a wedged repo.
- Restart-safe: worktrees survive `service stop && start`, `swarm stop/run`,
  and reconcile rebuilds; members reattach to their branch, transcripts keep
  resuming.
- Opt-in per space (`settings.worktree_isolation`) with per-member override;
  a space that never opts in behaves byte-identically to today.
- Green on `windows-latest` — the worktree stack's CRLF/path lessons
  (`27896f3`, `9d55356`) carry into every new test.

### Non-goals (v1.9)

- No web "merge" button — the leader tool (plus the existing approval gate)
  is the only merge surface this wave. (Open question #1.)
- No leader worktree — the leader stays on the root checkout (D8).
- No per-task ephemeral worktrees, no PR/patch export, no remote push. The
  unit of integration is "the member's committed work at merge time".
- No cross-member file locking or edit arbitration on the root checkout for
  members that opt out.
- No automated test/build gate at verify time (that is its own future wave;
  this PRD only makes concurrent *editing* safe).

---

## 3. Verified current state

### 3.1 One shared workdir, no isolation

- `constructMember` (space.go:311-314): `acfg := sp.cfg.Clone()` — scalars
  are cloned so per-member model/effort pins don't bleed, but `WorkDir` is
  the space workdir for every member. All fs/bash tools capture it at
  construction (internal/toolset/builtins.go:44-75).
- Cross-member conflict protection: none. The fs tools' read-tracking guards
  stale writes only *within* one agent; across members nothing intervenes.

### 3.2 The worktree tools are only UI-hidden — and the latent path is hazardous

`enter_worktree`/`exit_worktree` are absent from the web add-agent form
because `SelectableTools` filters them (internal/swarm/service/service.go:1658
deny map) — **a catalog filter, not a runtime block**. An operator who
hand-writes `enter_worktree` into a member's `tools/active.yml` gets a
working tool today: swarm members are root agents and install themselves as
the `WorktreeController` (internal/agent/agent.go:568). That latent path is
actively dangerous:

- **Session-slug migration.** `persistSession` derives the transcript slug
  from the *live* workdir (internal/agent/persist.go:33,
  `memdir.ProjectKey(a.workdir)`), and `ListSessions` does the same
  (agent.go:1557). A member sitting in a worktree persists its transcripts
  under the *worktree's* slug — `ResetSpace`
  (`agent.ResetWorkdirSessions`, service.go:627), `ClearMemberSession`
  (supervisor.go:504), and restart-resume (`latestSessionFor`,
  internal/swarm/resume.go:257) all assume the root slug and silently miss
  them.
- **Project permission rules follow the workdir.** `permission.LoadMember`
  reads `<workdir>/.evva/permissions.json` from the member's config workdir
  (space.go:419, pkg/permission/loader.go:70) — in a worktree that resolves
  to a stale committed copy (or nothing).

This wave closes that hazard properly instead of leaving it to chance
(§5.3, D7).

### 3.3 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| Worktree naming + layout | worktree.go:551-555, pkg/permission/types.go:30 (`.evva/worktrees`) | `worktreeDirFor`/`branchNameFor` conventions; `worktree_list` already recognizes managed dirs |
| Provision for a child agent | `CreateForSubagent` (worktree.go:646) | The create-from-HEAD flow; swarm variant must drop the random suffix (needs deterministic reattach) |
| Config-clone workdir override | spawn.go:60-71 | The exact injection pattern `constructMember` will copy |
| Merge core | `executeMerge` (worktree.go:354-498) | Dirty-base refusal, uncommitted-source refusal, no-op at ahead==0, `--no-ff` from the base, **abort + conflicted-paths on conflict** |
| Dirty detection | `worktreeHasChanges` (worktree.go:619) | "uncommitted files OR commits ahead" — the remove/preserve test |
| Cleanup semantics | `CleanupSubagentWorktree` (worktree.go:695) | auto-remove-if-unchanged, preserve-and-report otherwise |
| Observability | worktree_list.go (`aheadBehind` :184, managed filter :175) | The probes the roster column reuses |
| Cross-platform lessons | commits `27896f3`, `9d55356`; fixture `newFakeRepo` (worktree_test.go:97) | repo-local git identity, `core.autocrlf=false`, `filepath.EvalSymlinks`/`filepath.Clean` |

### 3.4 What keys off WorkDir (blast-radius audit)

Root-anchored and safe (they read `sp.Workdir`, not the member clone): the
space store (`store.Open(cfg.WorkDir)` → `.vero/vero.db`, space.go:144),
event log (service.go:471), `runtime.json` (resume.go:56), `alarms.json`
(space.go:600), member memory dirs (space.go:363), skills dirs
(space.go:297-305), the manifest itself.

Must-fix when a member's workdir moves (all in §5.3): the permission loader
(space.go:419), the memory wake reminder's *relative* index path
(space.go:679-701), and the session slug (persist.go:33 + agent.go:1557).

Wanted-to-follow (and already following, by construction-time capture):
fs/bash/repo-map tools, the memdir EVVA.md snapshot.

---

## 4. The merge-ownership decision: the leader merges, nobody else

The base checkout is one shared resource; `git merge` from two processes
into one checkout is unsafe. Three candidate owners:

1. **Worker self-merge on completion** — rejected. It races the shared base
   (workers run concurrently by design) and violates the trust boundary: a
   worker writing the operator's base branch is exactly the class of action
   the permission gate exists for, multiplied by N members.
2. **Supervisor auto-merge on `task_verify` approve** — rejected for v1.
   Conflicts need model judgment (whose change wins, what to tell the
   worker), and burying the merge inside verify makes a conflicted verify
   ambiguous: did the leader reject the *work* or the *merge*?
3. **A leader-only `worktree_merge` tool** — chosen. The leader is one agent
   with one run slot (`runOnce` claims it under the member mutex,
   internal/swarm/scheduler.go:189-201), so leader-driven merges are
   serialized *by construction* — no new locking. It matches the ledger's
   single-writer invariant (`ErrNotLeader`, tasks.go:122): the base branch
   is just another leader-owned ledger. And verify is already where the
   leader inspects work (tools/tasks.go:235) — merge slots in immediately
   before approve.

Resolution order on conflict: the tool aborts (base left clean — the fan-out
contract), returns the conflicted paths, and the leader rejects the task
back to `running` with a note telling the worker to merge the base branch
into *its own* branch, resolve in *its own* worktree, recommit, and report
again. Conflicts are always resolved on the worker's side; the base checkout
is never left mid-merge. The 5-state machine is untouched
(`verifying → running` is already legal, tasks.go:80-86).

---

## 5. Design

### 5.1 Lifecycle: one persistent worktree per opted-in member

Provisioned at `constructMember`, living as long as the member. Per-task
ephemeral worktrees were rejected because the runtime cannot switch workdirs
at task boundaries: tasks arrive as mail — possibly folded into a run
mid-flight by drain B (internal/swarm/drain.go:26) — and `SwitchWorkdir`
refuses mid-Run (agent.go:1301-1305). Persistent-per-member also covers
non-task wakes (mail/schedule) that edit files: those land on the member's
branch and reach base at its next merge (D5).

Naming is deterministic — no random suffix, unlike `CreateForSubagent` —
because restart must reattach:

- dir: `<repoRoot>/.evva/worktrees/swarm-<member>`
- branch: `worktree-swarm-<member>`

via the existing `worktreeDirFor`/`branchNameFor` with slug
`swarm-<sanitized member name>`. One space per workdir is an existing
invariant ("one db file per workdir = one space", store.go:44), so the
member name is unique per repo.

Provision/reattach semantics (`ProvisionMemberWorktree`, SWT-1):
`git worktree prune` → dir + admin entry exist → reuse; dir missing but
branch exists → `git worktree add <path> <branch>` (no `-b`); neither →
create from HEAD.

If the space workdir is nested inside a larger repo, the member's injected
workdir is `worktreePath + rel(repoRoot, spaceWorkdir)` so the member sees
the same project-relative cwd.

### 5.2 Config: opt-in knob, fail-fast validation

```yaml
settings:
  worktree_isolation: true      # default false — non-coding swarms untouched
workers:
  - agent: docs-writer
    worktree: "off"             # per-member override: "" | "on" | "off"
```

`agentdef.Settings.WorktreeIsolation bool` + `Member.Worktree string`,
member-field-beats-settings layering (the RP-24 `permission_mode` precedent,
space.go:346-352), fail-fast parse (the manifest.go:267
`parsePermissionMode` precedent), `WriteManifest` round-trip.

When any member resolves to "on", `NewSpace` preflights: `gitTopLevel`
succeeds AND `git rev-parse HEAD` resolves (an empty repo cannot host a
worktree). Otherwise the register **fails** with a targeted message. Silent
degrade would quietly drop an isolation property the operator asked for;
`worktree: off` per member is the escape hatch for mixed teams. The leader
with `worktree: on` is rejected at load (D8: merges land on the base
checkout; a root leader can read any worktree by absolute path).

### 5.3 Wiring: inject the worktree, pin root-anchored state (D7)

In `constructMember`, for a worktree-on member:

```go
sess, err := mode.ProvisionMemberWorktree(ctx, sp.Workdir, "swarm-"+name)
// fail the register on error — preflight makes this rare
acfg.WorkDir = memberWorkdir(sess.Path, sp.Workdir) // + rel subdir if nested
```

— the spawn.go:60-71 pattern. Three root-anchoring fixes ride along:

1. `permission.LoadMember(sp.Workdir, …)` instead of `acfg.WorkDir`
   (space.go:419) — project rules always come from the root checkout.
2. `memoryWakeReminder` (space.go:679-701) renders the memory index path
   absolutely when the member's workdir differs from `sp.Workdir`.
3. **Session-slug pin** — a new narrow seam, `config.Config.SessionWorkdir`
   (empty = today's behavior), honored by `persistSession` (persist.go:33)
   and `ListSessions` (agent.go:1557); the swarm sets it to `sp.Workdir` so
   member transcripts stay under the root slug and
   `Reload`/`ResetSpace`/`ClearMemberSession` keep working unchanged. This
   also defuses the §3.2 latent hazard for hand-enabled `enter_worktree`.

The space records live sessions (`sp.worktrees map[string]mode.WorktreeSession`)
for the merge tool, roster, and teardown.

### 5.4 The `worktree_merge` tool + team protocol

Leader-only (registered in `toolNamesForRole`, internal/swarm/tools/set.go:89),
wrapping the extracted `MergeBranch` core against the root checkout:

```
worktree_merge {member: "qa", task_id?: 42}
→ ok:      {merged: true, base: "main", commits: 3, files_changed: 7}
           (then fast-forwards qa's branch onto the new base tip — D4)
→ no-op:   {merged: false, reason: "nothing committed on worktree-swarm-qa"}
→ conflict:{merged: false, conflicts: ["pkg/a/x.go", "web/y.ts"]}
           (base already aborted clean — never half-applied)
```

Deliberately **not** in `permission.ReadOnlyOrSelfTools` (set.go:55-66): it
rewrites the operator's base branch — the "governance-shaped" auto-allow
argument for task tools does not extend to the user's repo history. In
`default` mode it prompts through the existing web gate; unattended swarms
use the existing levers (leader `permission_mode: bypass`, or an RP-11 allow
rule in the leader's `permissions.json`).

`teamprompt.go` protocol updates:

- Workers: *commit before reporting done* (a no-op merge is the leader's
  tell that you didn't); *start each task by merging the base branch into
  your branch*; conflicts are resolved in YOUR worktree, never on root.
- Leader: verify order is inspect → `worktree_merge` → `task_verify
  {approve:true}` (fold merge stats into the note); on conflict,
  `task_verify {approve:false, note:<conflicted paths + resolve recipe>}`.

Drift control (D4): after a successful merge the tool fast-forwards the
merged member's branch onto the new base tip (`--ff-only`; skip with a
warning if the worktree is dirty). Other members refresh lazily via the
start-of-task protocol. No runtime auto-reset of a dirty worktree, ever.

### 5.5 Lifecycle durability

| Event | Worktree behavior |
|---|---|
| freeze / suspend / `swarm stop` / service stop | untouched — durable on disk like `.vero` |
| restart / `swarm run` / reconcile | reattach (prune → reuse → re-add → fresh); member resumes its transcript (root slug, §5.3) |
| `RemoveMember` | clean (no uncommitted, ahead==0 — `worktreeHasChanges` semantics) → remove worktree + branch; dirty → preserve + one durable mail to the leader naming the branch (the `notifyLeader` pattern, service.go:1650) |
| `ResetSpace` | force-remove all `swarm-*` managed worktrees + branches alongside the existing `.vero`/session wipe (service.go:624-629) — reset means blank slate |

### 5.6 Windows

Inherit, don't reinvent: `runGit` is plain `exec.CommandContext`
(worktree.go:574) and already verified portable. Every new test fixture
carries the trio from `27896f3`/`9d55356`: repo-local `user.name`/`user.email`,
`core.autocrlf=false`, `filepath.EvalSymlinks` on temp dirs. Any comparison
between git output (forward slashes) and `filepath.Join` values goes through
`filepath.Clean` (the worktree_list.go:83-88 lesson); paths rendered into
prompts/roster use `filepath.ToSlash`.

---

## 6. Work items

**SWT-1 — Extract the reusable provision/merge core in `internal/tools/mode`.**
Package-level functions beside `CreateForSubagent`:
`ProvisionMemberWorktree(ctx, rootWorkdir, slug)` (deterministic slug,
reattach semantics per §5.1); `MergeBranch(ctx, baseDir, branch)
(MergeReport, error)` — the body of `executeMerge` (worktree.go:354-498)
minus controller/session concerns, returning
`{BaseBranch, Ahead, FilesChanged, Conflicts []string, NoOp bool}` with the
abort-on-conflict contract intact and WITHOUT tearing the worktree down;
`RefreshWorktree(ctx, wtPath, baseBranch)` (ff-only, refuse dirty).
`exit_worktree`'s merge action becomes a thin caller (teardown stays in the
tool).
*Accept:* existing `internal/tools/mode` tests green unchanged; new unit
tests cover reattach (dir exists / branch-only / neither / stale admin
entry) and MergeReport clean/conflict/no-op/unclean-source paths against
`newFakeRepo`-style fixtures.

**SWT-2 — Manifest knob.**
`Settings.WorktreeIsolation` + `Member.Worktree` (`""|"on"|"off"`,
fail-fast parse), threaded through `memberYml`/`Loaded`; `WriteManifest`
round-trips; leader `worktree: on` rejected at load.
*Accept:* load/write round-trip tests incl. omit-when-default; an invalid
value or leader-on rejects the whole manifest at register with a targeted
error.

**SWT-3 — Space wiring + root-state pinning.**
`constructMember` injection (§5.3), `NewSpace` repo/HEAD preflight (§5.2),
the three D7 fixes (permission loader, memory reminder,
`Config.SessionWorkdir` slug pin), `sp.worktrees` records.
*Accept:* integration test — a worktree member's bash `pwd`/write lands in
`.evva/worktrees/swarm-<name>`; `.vero`, event log, memory writes, and
permission rules stay root-anchored; the session snapshot appears under the
ROOT workdir slug; non-repo + enabled fails register with the §5.2 message;
a non-opted member behaves byte-identically to today.

**SWT-4 — Leader `worktree_merge` + protocol teaching.**
The §5.4 tool (leader-only, gated, optional `task_id` folds the result into
the verify note; success auto-refreshes the merged branch) + the
`teamprompt.go` worker/leader protocol updates.
*Accept:* tool tests — clean merge (base advanced, member branch
refreshed), conflict (abort, base clean, worktree intact, paths listed),
no-op, dirty-base refusal; a worker-role agent does not receive the tool;
protocol text asserts commit-before-done.

**SWT-5 — Lifecycle durability.**
Restart reattach (mostly SWT-1 semantics), `RemoveMember`
clean-remove/dirty-preserve + leader mail, `ResetSpace` worktree wipe,
`git worktree prune` before any reattach.
*Accept:* restart round-trip e2e — worker commits in its worktree,
StopSpace, RunSpace, same branch+commit visible and the member resumes its
transcript; ResetSpace leaves no `swarm-*` worktree or branch; RemoveMember
on a dirty worktree preserves it and the leader receives exactly one
durable mail naming the branch.

**SWT-6 — Observability.**
`list_members` + the web roster (`Service.Roster`) gain a worktree column:
branch, ahead/behind, dirty (reuse the worktree_list probes;
`filepath.ToSlash`). A merge conflict additionally sends one durable mail to
"user" so the operator sees it on the web without scraping transcripts.
*Accept:* roster JSON/tool output shows the column for opted members and
omits it otherwise; a forced conflict produces exactly one operator mail.

**SWT-7 — Swarm e2e + Windows CI.**
End-to-end: two workers, overlapping edits to one file; leader merges A
(clean), merges B (conflict → reject to running with note); B refreshes in
its worktree, resolves, recommits; leader merges B clean; both changes on
base; ledger shows completed×2. Fixtures copy the `newFakeRepo` hygiene.
*Accept:* e2e green on ubuntu + `windows-latest`; no new CI jobs required.

**SWT-8 — Docs + version + changelog.**
User guide (en, zh-tw): "isolated coding swarms" section — the knob, the
commit→report→verify→merge loop, the conflict recipe, unattended-mode
permission guidance, non-repo behavior, disk-usage note. `CHANGELOG.md`
entry; `pkg/version/version.go` v1.9.0 cycle.
*Accept:* docs updated in both languages; changelog entry present at the
beta cut.

Sequencing: `SWT-1 ∥ SWT-2 → SWT-3 → {SWT-4, SWT-5, SWT-6} → SWT-7 → SWT-8`.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| SWT-1 | mode-package unit tests extend the existing suite; fixtures already CRLF/identity-hardened | seconds |
| SWT-3/4/5 | swarm integration tests spin real temp git repos (share or thinly duplicate the fixture helper) | seconds each; `-race` stays on ubuntu (existing split) |
| SWT-7 | e2e joins the existing ubuntu + `windows-latest` jobs | minutes-scale on windows; no new workflow |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Dirty base checkout blocks all merges (operator edits root mid-swarm) | Existing guard refuses with a clear message (worktree.go:410-417; untracked files already exempt); the tool surfaces it verbatim; leader mails the operator to commit/stash |
| Branch drift → conflict pileup on long-lived member branches | D4 refresh-after-merge + start-of-task refresh protocol; roster ahead/behind column makes drift visible; sequential leader merges keep each merge honest against the current base |
| No git identity on host → `git merge --no-ff` fails | Preflight warning at register (`git config user.email` probe); the merge error names the fix; tests pin repo-local identity (`27896f3` lesson) |
| Worker never commits → merge no-ops, work stuck in the worktree | Protocol teaches commit-before-done; the no-op result tells the leader exactly that; dirty flag in the roster |
| Session-slug divergence breaks reset/clear/resume | The D7 slug pin — the single subtlest change; covered by SWT-3 acceptance (snapshot under root slug) and the SWT-5 restart e2e |
| Space workdir nested in a larger repo shifts cwd semantics | §5.1 rel-path mapping; nested-workdir fixture in SWT-3 |
| Disk usage: N working trees | Worktrees share `.git` objects (cheap vs clones); documented; roster shows what exists; remove/reset clean up |
| Windows CRLF / path-separator regressions | The carry-over trio baked into SWT-1/SWT-7 acceptance; suite runs on `windows-latest` |
| Uncommitted work lost on ResetSpace | Reset is already documented as destructive (wipes transcripts + ledger); the worktree wipe is consistent; RemoveMember (the accidental path) preserves dirty worktrees |
| Leader merge racing a member's in-flight commit | Merge refuses an uncommitted source (worktree.go:421-428); a commit landing between check and merge only changes the ahead-count — worst case a retry |

---

## 9. Open questions

1. **Web merge button?** Recommend defer — the tool result plus the gate
   already give the operator a hand on the wheel; a `POST .../worktree/merge`
   wrapping the same core is a clean fast-follow.
2. **A `worktree_status` tool for workers?** Recommend no — bash + git
   covers it; keep the swarm tool surface minimal.
3. **Auto-merge sugar on `task_verify {approve:true}`?** Recommend no for
   v1 (§4) — explicit merge keeps the conflict path unambiguous; revisit
   with usage data.
4. **Leader in a worktree?** Recommend rejected-at-load for v1 (D8);
   revisit if a "coding leader" pattern emerges.
5. **Fail-fast vs degrade on a non-repo workdir?** Recommend fail-fast
   (§5.2) — degrading silently drops a safety property the operator asked
   for; per-member `worktree: off` is the escape hatch.

---

## 10. Rollout

1. SWT-1..SWT-8 land via `feature/swarm-worktree` → `dev` (normal PR flow).
2. `pre-release feature` cuts `v1.9.0-beta.1` — this wave claims the v1.9
   minor (v1.8.4 is the newest stable at planning time).
3. Manual validation on the beta: a 1-leader/2-worker coding swarm on a real
   repo, ubuntu + Windows, exercising the conflict recipe end-to-end.
4. `release` promotes to `v1.9.0`.
