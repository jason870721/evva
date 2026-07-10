# PRD — Swarm Verify Checks — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** implemented — CHK-1..6 built 2026-07-10 on
> `feature/swarm-verify-checks` (all six work items, tests green on the
> post-DWF codebase; CHK-5 shipped in full since dynamic workflow had
> landed). Worktree-isolation workdir resolution is a seam
> (`checkWorkdirFor`) returning the space workdir until that wave exists.
> **Target release:** TBD — wave-sized minor. Per the checkpoint-rewind
> precedent, the CLAUDE.md wave → minor row is added only when the operator
> confirms the wave.
> **Roadmap source:** swarm design review 2026-07-04. Both sibling swarm PRDs
> name this as their deferred half: worktree isolation excludes "automated
> test/build gate at verify time (that is its own future wave)"
> (`swarm-worktree-isolation.md` §2 non-goals), and dynamic workflow reserves
> the `verify:"checks"` enum slot for it
> (`swarm-dynamic-workflow.md` §2, §5.3).
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1), 2026-07-04/05. All file:line references verified against
> that commit.
> **Reference source:** none — evva-native (no ref/src analog; the ledger and
> its verify step are Veronica designs).

---

## 1. TL;DR

Task verification is pure leader judgment with zero machine evidence. When a
worker reports done, the leader moves the task to `verifying`
(`task_update_status`, internal/swarm/tools/tasks.go:209-226), "inspects",
and rules (`task_verify`, tools/tasks.go:253-260) — but nothing ever *runs*.
For a coding swarm the inspection that matters is mechanical: does it build,
do the tests pass, does the linter agree. Today the leader either burns a run
slot shelling those out itself (in bypass mode), asks the worker to
self-report (trust), or approves blind.

This wave adds a **check runner** to the service: when a task enters
`verifying`, the service executes an operator-configured command (build /
test / lint) in the right directory, captures exit code + output tail as
**evidence on the task row**, and delivers it to the leader as durable mail —
so the leader's verify wake starts with the facts in hand. The command is
**operator-authored only** (manifest), never agent-authored (§4). Composed
with the dynamic-workflow wave, `verify:"checks"` completes a passing task
with zero leader wakes and escalates a failing one to the leader with
evidence attached — CI-gated leaderless chains.

The machinery is assembled, not invented: process execution and timeout-kill
come from the WIN-wave seam (`proc.Shell()`/`Group`/`KillTree`,
pkg/common/proc/shell.go:26, proc.go:21,31 — the exact trio the bash tool
uses, pkg/tools/shell/bash.go:162,182,187), evidence rides the existing
ledger row, and the alert path is `notifyOps`
(internal/swarm/scheduler.go:299).

---

## 2. Goals / non-goals

### Goals

- Every `verifying` entry on an opted-in space triggers one configured check
  command; its exit code, duration, and output tail land on the task row and
  in the leader's mailbox before (or with) the leader's verify wake.
- The command runs where the work is: the assignee's worktree when the v1.9
  isolation wave is active for that member, else the space workdir.
- Composed with dynamic workflow: `verify:"checks"` = auto-complete on pass,
  escalate-to-leader-with-evidence on fail. Without dynamic workflow the
  feature still ships whole (leader-verified flow, evidence attached).
- Agent-proof trust boundary: no agent — leader included — can choose or
  edit the command text. Checks execute only operator-authored manifest
  config.
- Windows-green via the existing shell seam; a hung check is killed by
  process tree, never orphaned.

### Non-goals (this wave)

- No per-task command strings and no leader-authored commands (§4). Per-task
  control is on/off only.
- No multi-command pipelines, no structured test-report parsing (JUnit etc.)
  — evidence is exit code + tail. A future wave can layer parsers.
- No retry policy — a flaky check reruns only when the task re-enters
  `verifying` (reject → rework → done again).
- No web "run checks now" button; no checks outside the task ledger
  (schedule-driven health checks are the cron/schedule feature's territory).
- No sandboxing beyond what the operator's own command implies — the command
  runs with the service's OS user, like every bypass-mode member shell
  already does.

---

## 3. Verified current state

### 3.1 Verify is judgment-only

- The 5-state machine (internal/swarm/store/tasks.go:80-86) reaches
  `verifying` via leader `task_update_status` (tools/tasks.go:209) and — once
  the dynamic-workflow wave ships — worker `task_done`. Both edges out
  (`completed` / back to `running`) are leader `task_verify`
  (tools/tasks.go:253-260).
- The row carries `Result` and `VerifyNote` (store/tasks.go:48-49) — text
  fields written by agents; no machine-generated field exists.
- There is no per-transition callback in the store; transition side effects
  ride the tool layer (the dynamic-workflow PRD's `OnTaskCompleted` sets the
  pattern this wave copies for verifying-entry).

### 3.2 Execution machinery already hardened

- `proc.Shell()` (pkg/common/proc/shell.go:26) resolves the POSIX shell —
  Git Bash on Windows, `EVVA_SHELL` operator override (shell.go:16) — and is
  exactly how the bash tool runs commands (`<shell> -c <command>`,
  pkg/tools/shell/bash.go:41,162).
- Process-group + tree-kill: `proc.Group(cmd)` / `proc.KillTree(cmd)`
  (pkg/common/proc/proc.go:21,31; used at bash.go:182,187) — a timed-out
  check dies with its children, both OSes.
- Timeout norms: the bash tool's `defaultBashTimeout = 2min`,
  `maxBashTimeout = 10min` (bash.go:37-38) — this wave adopts the same
  bounds for check timeouts.
- The direct-exec precedent outside any agent: `runGit` in
  internal/tools/mode/worktree.go:574 (plain `exec.CommandContext`, verified
  portable by the WIN wave).

### 3.3 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| Shell resolution + tree kill | proc.Shell/Group/KillTree (shell.go:26, proc.go:21,31) | The runner's exec core, Windows included |
| Timeout bounds | bash.go:37-38 | Default 2 min / max 10 min check timeout |
| Ops alert mail | `notifyOps` (scheduler.go:299-303) — sender "system", recipients leader + "user" | Evidence delivery + operator visibility in one call |
| Duration knob parsing | `parseStallDuration` (agentdef/manifest.go:245-252) | `verify_checks.timeout` fail-fast parse |
| Migration rig | store.go:107-111 (forward-only, embedded) | The `checks` evidence column ships as the next free migration |
| Transition hook pattern | dynamic-workflow PRD §5.2 (`OnTaskCompleted` at the tool layer) | Verifying-entry hook shape; the two waves share the seam style |
| Worktree records | worktree PRD §5.3 (`sp.worktrees` session map) | Where the runner resolves the assignee's checkout when isolation is on |

---

## 4. The trust decision: only the operator authors commands

A check command is arbitrary shell executed by the *service* — outside every
member permission gate. Who may write it:

1. **Worker-authored** — rejected outright; workers can't even write the
   ledger (store/tasks.go:122).
2. **Leader-authored (per-task command strings on `task_create`)** —
   rejected. The leader is an LLM subject to prompt injection through worker
   reports and repo content; in `default` permission mode it cannot run
   `bash` without an operator gate, and a task-field command string would be
   exactly that gate's bypass. The ledger's governance argument for
   auto-allowing task tools (tools/set.go:55-64) holds only while task
   fields cannot execute.
3. **Operator-authored (manifest `settings`)** — chosen. The manifest is
   already the operator's trust surface (permission modes, budgets,
   schedules); a command there is the same trust class as
   `permission_mode: bypass`.

Agents keep exactly one lever: `task_create {check: "off"}` — opting a task
*out* (docs-only tasks, discussion tasks). They can never opt *into* a
different command. The check config is deliberately **space-scoped, not
member-scoped**: evidence is about the repo's state, not about who edited it.

---

## 5. Design

### 5.1 D1 — Config

```yaml
settings:
  verify_checks:
    command: "go build ./... && go test ./..."   # required to enable
    timeout: 5m                                  # default 2m, max 10m
```

`agentdef.Settings.VerifyChecks *CheckSpec` (`Command string`,
`Timeout time.Duration`) — absent = feature off, whole space byte-identical
to today. Fail-fast at `LoadManifest` (manifest.go:294): empty command with
the block present, or a timeout outside (0, 10m], rejects the register (the
parseStallDuration precedent, manifest.go:245). `WriteManifest` round-trips
the block.

### 5.2 D2 — The runner

A per-space `checkRunner` owned by the `SwarmSpace`: single goroutine, one
in-flight check per task, bounded queue (drop-oldest is wrong here — refuse
duplicate enqueues per task instead; re-entry cancels the previous run for
that task via `KillTree` and starts fresh, "latest verifying-entry wins").

Execution: `proc.Shell()` → `exec.CommandContext(ctx, shell, "-c", cmd)` with
`proc.Group` at start and `proc.KillTree` on timeout/cancel (the bash tool's
exact discipline, bash.go:162-187). Working directory resolution:

- worktree isolation active for the assignee → that member's worktree path
  (the worktree PRD's `sp.worktrees` records, its §5.3);
- else → the space workdir (`sp.Workdir`).

Evidence captured: `{command, exit, durationMs, startedAt, tail}` with the
tail capped at 16 KiB (head 2 KiB + tail 14 KiB when truncated, marked) —
enough for a failing test name and stack, bounded for the ledger row and the
mail.

### 5.3 D3 — Wiring: verifying-entry in, evidence out

Trigger points converge at the tool layer (there is no store callback,
§3.1): `task_update_status → verifying` and — when dynamic workflow is
present — `task_done` both call `sp.EnqueueCheck(taskID)` after the
transition commits. The runner, on completion, in order:

1. **Persist**: write the evidence JSON to the task's new `checks` column
   (one row-update store method; the column ships in the next free migration
   — 0006 today, 0007 if dynamic workflow lands first).
2. **Mail**: `notifyOps`-style durable mail to the leader (and "user"):
   `"checks for task #42 (qa): PASS in 38s"` or FAIL + the tail. Durable
   mail means a crashed service redelivers nothing but the row survives; the
   rescan-visible evidence column is the source of truth.
3. **Event**: one synthetic `task_check_done` event line (the
   scheduleChangeEvent pattern, service/service.go:1408-1609) for the
   console and the durable chatlog.

A service restart mid-check simply loses the run: the task sits `verifying`
with no evidence, the leader's protocol (5.5) says "no check mail → ask for
state or re-enter verifying", and the RP-22 stale-task sweep
(workflow_watch.go:40) is the backstop. No check-resume machinery.

### 5.4 D4 — Composition with `verify:"auto"` (dynamic workflow)

When both waves are live, the `verify` policy gains its reserved third value:

- `verify:"checks"`: on `task_done`, run the check; **pass → system completes
  the task** (the same narrowly-scoped system transition the auto policy
  uses) and the chain proceeds; **fail → task stays `verifying`**, leader
  gets the evidence mail, and rules manually. The leader is woken only by
  failures.
- `verify:"leader"` + checks configured: evidence is advisory — the runner
  fires, the leader still rules.
- `verify:"auto"` + checks configured: auto wins (no check) — `auto` is the
  leader explicitly declaring "mechanical, don't gate"; the protocol warns
  against combining it with meaningful code steps.

Shipped standalone (no dynamic workflow), only the advisory flow exists —
still the whole evidence pipeline, minus the auto-complete edge.

### 5.5 D5 — Protocol (`teamprompt.go`)

- `leaderProtocol` (teamprompt.go:155): wait for the check mail before ruling
  on a `verifying` task; a FAIL tail is your rejection note's first draft;
  no mail after a reasonable wait → treat as no-evidence and inspect
  manually.
- `teamProtocolCommon` (:133): tasks on this space run
  `<command>` at verify time; make it pass locally before `task_done`;
  `check:"off"` exists for non-code tasks.

### 5.6 D6 — Observability

`TaskInfo` (webapi/api.go:294) gains `checks` (the evidence object); the
board task card renders a PASS/FAIL/RUNNING chip; `task_get`/`task_list`
render a one-line evidence summary. Metrics: `checksRun`, `checksFailed`,
`checksTimeout` on `spaceMetrics` (metrics.go:31).

---

## 6. Work items

**CHK-1 — Store: evidence column + accessor.**
Next-free migration adds `tasks.checks TEXT` (JSON evidence, NULL = never
ran); `SetTaskChecks(id, json)` + evidence in `GetTask`/`ListTasks`
loading.
*Accept:* migration applies to a populated db; evidence survives
restart; a task with no checks is byte-identical to today.

**CHK-2 — Runner.**
`checkRunner` (5.2): shell resolution, group+tree-kill, timeout, per-task
single-flight with cancel-and-replace, 16 KiB capped evidence, workdir
resolution seam (worktree map when present, space workdir otherwise).
*Accept:* unit tests — pass/fail/timeout (killed tree, no orphans —
`proc.Alive` probe), truncation marks, duplicate enqueue cancels the
predecessor; green on ubuntu + `windows-latest`.

**CHK-3 — Wiring + delivery.**
`EnqueueCheck` calls from the verifying-entry tool paths; persist → mail
(`notifyOps` shape) → `task_check_done` synthetic event, in that order.
*Accept:* integration — a verifying entry produces exactly one evidence row,
one leader mail, one event line; a service restart mid-check leaves a
`verifying` task with NULL evidence and no crash.

**CHK-4 — Config knob.**
`Settings.VerifyChecks` parse/validate/round-trip (5.1); `task_create`
gains `check: "on"|"off"` (default on when the space configures checks).
*Accept:* fail-fast on empty command / out-of-range timeout; absent block =
no runner constructed; `check:"off"` task never enqueues.

**CHK-5 — `verify:"checks"` composition** *(only if dynamic workflow has
shipped; otherwise re-scoped to a follow-on ticket)*.
The pass → system-complete edge, fail → stay-verifying + evidence mail;
protocol text for policy choice.
*Accept:* a 3-task `verify:"checks"` chain completes leaderlessly on green;
a red middle task halts the chain in `verifying` with evidence mailed; the
system actor's completion is refused when evidence says fail.

**CHK-6 — Observability + docs.**
DTO field, board chip, tool-output summaries, metrics counters; user guide
(en, zh-tw) "machine-checked verification" section incl. the §4 trust
model; CHANGELOG.
*Accept:* chip renders all three states; docs in both languages.

Sequencing: `CHK-1 → CHK-2 → CHK-3 → CHK-4 → {CHK-5, CHK-6}`.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| CHK-1 | store suite extension | seconds |
| CHK-2 | runner tests spawn real short-lived processes (`sleep`/`exit 1` style fixtures, POSIX-written for Git Bash) | seconds; runs on both CI OSes |
| CHK-3/5 | space-level integration on temp stores | seconds |
| all | no new dependencies | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Prompt-injected agent executes arbitrary shell via checks | §4: command text is manifest-only; agents hold only on/off; task fields never execute |
| Long check serializes the pipeline (10 min suite × N tasks) | Per-task single-flight but tasks queue independently; timeout cap 10 min; docs recommend a fast subset command; metrics expose `checksTimeout` |
| Check runs against the wrong tree (shared workdir, another member mid-edit) | Documented limitation pre-worktrees — evidence names the dir it ran in; with the v1.9 wave on, the assignee's worktree removes the race |
| Hung process survives timeout | `proc.KillTree` on the process group (proc.go:31), the bash tool's proven kill path |
| Evidence bloats the ledger | 16 KiB cap per row, latest-run-only (column overwrite, not append); vacuum (RP-16) archives completed tasks as today |
| Flaky tests auto-fail `verify:"checks"` chains | Failure escalates to the leader (never auto-reject) — a human-judgment fallback is the design, not an afterthought |
| Windows shell quirks | `proc.Shell()` + POSIX-written fixtures; suite runs on `windows-latest` |

---

## 9. Open questions

1. **Per-member command overrides?** Recommend no — evidence is about the
   repo, not the member; a mixed-language space can chain both builds in one
   command.
2. **Structured parsers (JUnit / go test -json)?** Recommend defer —
   exit+tail covers the leader's decision; parsers are additive later.
3. **Should a FAIL block leader `task_verify {approve:true}`?** Recommend no
   — the leader may overrule (flaky test, known-red main); the approval note
   records the overrule; only `verify:"checks"` auto-completion is hard-gated.
4. **Re-run tool for the leader (`task_check {task_id}`)?** Recommend defer
   — re-entering `verifying` re-runs; an explicit tool is sugar.

---

## 10. Rollout

1. CHK-1..CHK-6 via `feature/swarm-verify-checks` → `dev` (normal PR flow).
2. `pre-release feature` cuts the wave's first beta under the minor the
   operator assigns at confirmation (wave→minor row added then).
3. Beta validation: a coding swarm on a real repo — green path, red path
   (evidence mail + reject recipe), timeout path, and (if dynamic workflow
   shipped) a `verify:"checks"` chain.
4. `release` promotes.
