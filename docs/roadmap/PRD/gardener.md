# PRD — Gardener (idle-time repo chores on the dream gate) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W17 / v1.27** per
> [../long-range.md](../long-range.md). Hard dependencies: sandbox
> isolation (W3) and worktree fan-out (shipped) — unattended execution
> without both is a non-starter; outbound notifications (W1) carry the
> digest.
> **Roadmap source:** 2026-07-06 long-range planning pass. Auto-dream
> (v1.8) proved the pattern: a gated, fenced background agent doing
> maintenance while the operator is away — for *memory*. The same
> pattern applied to *code* is the creative leap: every repo carries a
> permanent tail of chores (lint debt, doc drift, flaky tests, TODO
> rot, dependency bumps) that never justify interactive attention.
> **Reference source:** none — evva-native. (Dream's gating/fencing
> design is the in-repo precedent to extend, not copy-paste.)

---

## 1. TL;DR

The **gardener** is a background agent that tends a repo during idle
hours and delivers a **morning digest** of proposed changes — each one a
local branch with a diff, a rationale, and verification evidence. It
never touches shared branches, never pushes, never merges: the operator
reviews the digest and cherry-picks with one command per proposal.

```yaml
# .evva/gardener.yml — per-repo, explicit opt-in
enable: true
chores:
  - kind: lint-debt        # burn down a named linter's findings, N files/night
  - kind: doc-drift        # README/docs claims vs code reality
  - kind: todo-harvest     # TODO/FIXME older than 90d → fix or file
  - kind: test-gaps        # uncovered exported funcs → table tests
  - kind: dep-bumps        # patch-level only; run tests to verify
budget: { usd_per_night: 2.00, max_proposals: 5 }
verify: "go test ./..."     # every proposal must pass this to enter the digest
```

Execution rides infrastructure that all exists by this point in the
roadmap: the **dream idle gate** decides *when*; a **worktree + sandbox**
(W3) decides *where*; the **verify command** decides *what's worth
showing*; **outbound notifications** (W1) and the session catalog (W7)
decide *how you find out*. The gardener is deliberately a thin
composition — its PRD is mostly policy, not machinery.

## 2. Goals / non-goals

### Goals

- Chore framework: each `kind` is a scoped prompt + a discovery step
  (cheap, deterministic where possible — linters, grep, coverage) + a
  fix loop + the mandatory verify run. Ships with the five kinds above;
  kinds are data (prompt + config), so adding one is not a code change.
- Scheduling: reuse the dream gate's idle detection and fencing (one
  background agent at a time machine-wide; gardener yields to dream and
  to any interactive session — lowest priority, always).
- Proposal format: branch `gardener/<date>-<kind>-<slug>` + a journal
  entry `{kind, rationale, diff_stat, verify_output, cost}`; digest
  renders as notification + a `evva gardener review` TUI list with
  apply/discard per proposal (apply = merge/cherry-pick locally).
- Hard rails: nightly USD budget (rate card), proposal cap, verify-pass
  requirement, repo opt-in only, sandbox mandatory for `bash`-needing
  chores, no network beyond the model APIs unless the chore's sandbox
  policy allows (dep-bumps needs registry access — explicit per-kind).
- Aging: unreviewed proposals expire (branch pruned) after N days —
  the garden never accumulates compost.

### Non-goals (this wave)

- Opening PRs / pushing anywhere (a CI-integrated variant can follow
  the W12 wave; local-only first keeps the trust story simple).
- Feature work, refactors above a size cap, or anything touching
  files changed in the last N days (avoid stepping on active work).
- Multi-repo orchestration (per-repo config only; run it in each).
- Learning/adaptive chore selection (EX-10-adjacent; v1 is config).

## 3. Design sketch

- **Idle gate reuse:** the audit pins dream's gate/fence API; gardener
  registers as a second client with lower priority. If the gate turns
  out dream-specific, generalizing it is ticket zero (small, honest
  refactor — the second consumer proves the abstraction).
- **Discovery-first economics:** each chore runs its deterministic
  discovery (lint JSON, grep hits, coverage report) *before* any model
  call, and skips the night if there's nothing to do — most nights
  should cost cents or nothing.
- **Verify as the quality bar:** a proposal that fails the repo's
  verify command is discarded and journaled (with the failure), never
  surfaced as work for the human. The digest is a menu of *green*
  diffs.
- **Trust posture:** the gardener's changes are exactly as inspectable
  as a teammate's overnight PR — branch, diff, rationale, test output.
  Nothing lands without a human `apply`.

## 4. Work items

- **GRD-1 — Config + chore framework.** `.evva/gardener.yml` schema,
  kind registry (data-driven), discovery/fix/verify pipeline skeleton.
  *Accept:* config round-trips; unknown kinds rejected at load; a
  no-findings night produces an empty journal entry and zero spend.
- **GRD-2 — Gate generalization + scheduling.** Idle-gate second
  consumer (or generalization), priority/yield rules, machine-wide
  fence. *Accept:* gardener never runs while a TUI session is active or
  dream holds the fence; interactive start preempts within seconds.
- **GRD-3 — Execution rails.** Worktree+sandbox setup per chore,
  budget metering with hard stop, per-kind network policy, recent-file
  exclusion. *Accept:* budget exhaustion mid-chore stops cleanly with a
  journaled partial; a chore touching a recently-edited file is
  filtered out at discovery.
- **GRD-4 — Proposals + digest.** Branch/journal format, expiry
  pruning, notification digest, `gardener review` apply/discard TUI.
  *Accept:* fixture night yields reviewable proposals; apply merges
  locally; expiry prunes branch + journal consistently.
- **GRD-5 — The five chore kinds.** Prompts + discovery wiring per
  kind, each with a fixture-repo test (fake LLM for the fix loop, real
  discovery). *Accept:* each kind produces a plausible green proposal
  on its fixture; dep-bumps respects patch-only.
- **GRD-6 — Docs + changelog.** User-guide (en + zh-tw): opt-in setup,
  the trust model ("a menu of green diffs"), budget guidance, how to
  write a custom chore kind.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Silent spend while the operator sleeps | hard nightly USD cap + discovery-first (most nights ~free) + spend in every digest; budget is mandatory config, no default-on |
| Proposals conflict with the operator's in-flight work | recent-file exclusion + worktree isolation + apply-time normal git conflict handling; proposals expire |
| Verify command is weak → green-but-wrong diffs | digest shows the diff and verify output, never auto-applies; docs push repos toward real verify commands (the `verify`-skill philosophy) |
| Chore prompts rot as the repo evolves | kinds are data; the eval harness (W4) can fixture-test chore prompts like any other prompt surface |
| Gate/fence bugs let it collide with dream or a session | fence is machine-wide and table-tested; gardener is strictly lowest priority and crash-safe (worktrees are disposable) |

## 6. Open questions

1. Digest delivery default: notification only, or also a terminal
   MOTD-style note on the next `evva` start in that repo? (Leaning
   both; the second is free via the session catalog.)
2. Should `apply` support batch ("apply all green")? Tempting;
   friction-per-proposal is also the safety feature. Operator call.
3. A `chore: custom` kind (operator-authored prompt + discovery
   command) in v1, or defer? Leaning ship-it — it's config-only by
   construction.
