# PRD — Git Intelligence (commit craft, history context, conflict assistance) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W25**, suggested horizon
> H2/H3 per [../long-range.md](../long-range.md) §3b.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> evva treats git as "a binary bash can call" — which works, but leaves
> three gaps competing harnesses have closed: history as *context*
> (blame/log informing edits), commit *craft* (clean, split, attributed
> commits as a first-class output), and conflict *assistance* (the
> single most token-expensive git situation, handled ad hoc today).
> The worktree/merge-back fabric (shipped) and the swarm's branch
> contracts make evva unusually git-dependent — the intelligence layer
> should match.
> **Reference source:** none in `ref/src` beyond commit conventions —
> evva-native.

---

## 1. TL;DR

Three sub-features under one wave, all shelling out to `git` (zero new
dependencies, per house pattern):

1. **History as context.** A `git_context {path, line_range?}` tool
   returning a digest: last-touch summary per span (blame rollup), the
   N most recent meaningful commits touching the path (subjects +
   diff-stat, merge-noise filtered), and co-change hints ("files that
   usually change with this one", from log correlation). One call
   replaces the model's current 3–4 exploratory `git log`/`git blame`
   bash rounds — cheaper, and consistently shaped for context pruning.
2. **Commit craft.** A `commit_plan` flow: given the working tree, the
   model proposes a *partition* of changes into 1–N commits (paths +
   hunks per commit, message per commit, conventional-prefix aware —
   repo conventions read from recent history); the plan renders for
   operator approval (TUI overlay), then executes via `git add -p`-
   grade staging (apply per-hunk patches to the index — mechanical,
   scriptable, no interactive git). Attribution rules (e.g. this repo's
   own `--author` convention) come from config, not prompt memory.
3. **Conflict assistance.** On merge/rebase conflict (detected after
   any git bash call, or invoked via `resolve_conflicts`), a structured
   pass: per-file conflict inventory → for each, both sides' *intent*
   digests (from the commits that introduced them — sub-feature 1
   reused) → model resolves file-by-file with `ours/theirs/merged`
   decisions logged → verify command run before concluding. Turns the
   worst free-form git flailing into a checklist.

## 2. Goals / non-goals

### Goals

- `pkg/tools/gitx` (name per audit) tool family: `git_context`,
  `commit_plan` (+ its apply step), `resolve_conflicts`; all shell to
  `git` with porcelain-v2/plumbing output parsing, all read repo
  conventions (prefix style, author rules) from config + history
  sampling.
- Commit-plan approval UX: the partition renders as an overlay (files/
  hunks per commit, messages editable) — approve/edit/reject; nothing
  touches the index before approval. Headless mode takes the plan
  as-proposed under a flag (CI profile forbids it).
- Hunk-level staging engine: deterministic index construction from a
  plan (per-file partial application via computed patches), leaving the
  working tree untouched — the operator's uncommitted extras survive.
- Conflict flow integration: post-bash git-state probe (cheap `git
  status` parse) so the agent *notices* a conflicted state immediately
  instead of discovering it three commands later.
- Swarm/branch synergy: merge-back (worktree fan-out) and the leader
  merge flows can invoke conflict assistance as a subroutine.

### Non-goals (this wave)

- Replacing bash-git for everyday operations (status/push/checkout
  stay bash; only the three high-structure flows get tools).
- Forge operations (PRs, reviews — W12's lane via `gh`).
- History *rewriting* beyond the current merge/rebase (no autonomous
  interactive rebase in v1 — commit craft covers the forward path).
- Git internals reimplementation (no go-git; shell-out only).
- Automatic conflict resolution without the verify gate.

## 3. Design sketch

- **Digest discipline everywhere:** blame rollups cap at span-level
  attribution (not line-by-line), log digests filter merge commits and
  lockfile-only changes, co-change hints report the top 3 with counts.
  Every output is designed for a context budget, mirroring the DAP
  stop-digest philosophy.
- **The partition model:** `commit_plan` output is data — `[{message,
  files: [{path, hunks: [ids]}]}]` against a hunk inventory computed
  from `git diff` — so approval UIs, headless mode, and tests all
  consume the same structure. Unassigned hunks are an explicit
  "leftover" bucket the operator sees (never silently dropped).
- **Intent digests for conflicts:** each conflicted region maps to the
  commits that last touched each side (via blame on the two parents);
  their subjects/bodies become the "what were they trying to do"
  context — usually the missing ingredient when models resolve
  conflicts badly.
- **Convention sampling:** prefix style and message shape inferred
  from the last ~50 commits once per session and cached; config
  overrides sampling (`commit_style`, author rules — this repo's
  evva-author convention becomes config, not tribal memory).

## 4. Work items

- **GIT-1 — Porcelain parsers + state probe.** status/diff/blame/log
  parsing into internal models; post-bash conflicted-state detection.
  *Accept:* parser fixtures cover renames, binary files, submodules;
  probe flags a conflicted fixture repo instantly.
- **GIT-2 — `git_context`.** Blame rollup, filtered log digest,
  co-change hints, caps. *Accept:* fixture repo with seeded history
  yields the expected digest under budget; merge noise absent.
- **GIT-3 — Hunk inventory + staging engine.** Deterministic partial
  staging from a partition, working-tree preservation. *Accept:*
  property test — for random partitions of fixture diffs, resulting
  commits' union equals the original diff and the tree is untouched.
- **GIT-4 — `commit_plan` + approval overlay.** Plan schema, TUI
  render/edit, convention sampling, headless flag. *Accept:* a mixed
  fixture change (feature + drive-by fix) yields a 2-commit plan;
  editing a message in the overlay lands in the commit; leftovers
  bucket visible.
- **GIT-5 — Conflict flow.** Inventory, intent digests, file-by-file
  loop, decision log, verify gate. *Accept:* seeded-conflict fixture
  resolves end-to-end (fake LLM scripted) with the verify command run
  before success is reported.
- **GIT-6 — Swarm/worktree integration.** Merge-back and leader-merge
  call paths can invoke the conflict flow. *Accept:* a conflicting
  fan-out merge in a fixture routes through the flow instead of
  failing raw.
- **GIT-7 — Docs + changelog.** User-guide (en + zh-tw): the three
  flows, config (style/author), CI-profile restrictions.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Partial staging corrupts the index on exotic diffs | plumbing-level implementation + the GIT-3 property test + index backup/restore around apply |
| Commit plans annoy operators who like their own style | sampling + config override + full edit in the overlay; one-commit plans stay one keypress |
| Conflict assistance overrides human judgment | per-file decisions logged + verify gate + it never auto-commits the resolution (the operator or the calling flow does) |
| Digest hides the detail that mattered | every digest names its expansion path (the underlying git command) so the model can go deeper deliberately |

## 6. Open questions

1. Should `commit_plan` become the *default* path for the existing
   "commit when asked" behavior, or an explicit ask? Leaning explicit
   first, default after soak.
2. Co-change hints: log-correlation window (12 months? 500 commits?)
   — tune on real repos during the audit.
3. Does conflict assistance deserve a swarm verify-policy hook
   (auto-verify resolved merges) in v1 or fast-follow?
