# PRD — Swarm Dynamic Workflow — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed.
> **Target release:** `v1.10.0` (this wave claims the v1.10 minor per the
> CLAUDE.md wave → minor rule; first cut ships as `v1.10.0-beta.1`).
> **Roadmap source:** operator-commissioned swarm design review 2026-07-04 —
> "every hop in a multi-step plan costs one leader LLM wake" and "the roster
> is frozen at manifest time" surfaced as the two structural limits on swarm
> throughput, independent of (and complementary to) the v1.9 worktree wave.
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1) on 2026-07-04. All file:line references verified against
> that commit.
> **Reference source — NOT ported:** upstream gates a workflow-script engine
> behind a feature flag (`ref/src/tasks.ts:9-11`, `WORKFLOW_SCRIPTS` /
> `LocalWorkflowTask`) but the implementation is absent from the ref
> snapshot; its task system also carries dependency edges
> (`ref/src/tools/TaskCreateTool/prompt.ts:53`, "blocks/blockedBy").
> Cited as precedent only — this design is evva-native, built on the
> Veronica ledger.

---

## 1. TL;DR

The task ledger is a flat 5-state machine (`internal/swarm/store/tasks.go:80`)
with no dependency concept, and every status write is leader-only
(`TransitionTask`, tasks.go:122 → `ErrNotLeader`). The consequence: in a
multi-step plan, **every hop is a leader LLM run**. A worker finishes, mails
the leader; the leader wakes, marks `verifying`, inspects, approves — and then
must *remember* what comes next and hand-`task_assign` it. For a linear
5-task chain the leader is woken ~10 times, half of them purely to relay
dispatch decisions it already made at planning time. That is slow, burns
tokens, and bloats the leader's context — the member most punished by
auto-compaction (the RP-17 lesson).

Meanwhile the roster is static at runtime except for operator actions
(`Supervisor.AddMember/CreateMember/RemoveMember`,
`internal/swarm/supervisor.go:155/185/222`): the leader cannot scale the team
to the shape of the work, so "refactor these 5 packages in parallel" means
either 5 sequential tasks on one worker or a manifest edit and restart.

This wave makes the ledger itself the workflow engine, in two moves on one
execution model:

1. **Task graph + auto-dispatch.** `task_create` gains `depends_on`; a task
   with unmet dependencies is born `blocked`. When the last dependency
   completes, the engine flips it `blocked → pending → running` and delivers
   the same assignment mail `task_assign` sends today (tools/tasks.go:188) —
   no leader wake. A new per-task `verify` policy (`"leader"` default,
   `"auto"` for mechanical steps) lets declared-safe chains flow end-to-end
   with zero leader involvement.
2. **Ephemeral fan-out members.** A leader-only `member_spawn` clones an
   existing roster member under a derived name — no agent dir written, no
   manifest touch — and auto-retires it when its assigned work completes.
   Fan-out width stops being bounded by the manifest.

The governing invariant survives intact: **every decision is still the
leader's**. The engine holds no judgment — it only executes structure the
leader already declared (assignees, dependencies, verify policies are all
fixed at `task_create` time). §4 states the exact writer matrix.

---

## 2. Goals / non-goals

### Goals

- A leader can plan a whole dependency graph in one run; from then on the
  engine dispatches each task the moment its prerequisites complete. Leader
  wakes are spent on judgment (verification, exceptions, replanning), never
  on relaying a dispatch it already decided.
- `verify: "auto"` chains (mechanical steps) complete leaderlessly; the
  default `verify: "leader"` keeps today's human-judgment flow byte-identical.
- The leader can spawn N ephemeral clones of a roster member for fan-out,
  bounded by a manifest cap; clones retire themselves when their work
  completes, leaving transcripts and ledger history behind.
- Restart-safe: the graph lives in `vero.db`; spawned members live in
  `runtime.json`; a crashed dispatch is recovered by the existing rescan
  philosophy (DB is truth, hints are best-effort).
- Operator observability rides the existing surfaces: a `blocked` board
  column, dependency badges, an `ephemeral` roster pill, and durable
  event-log lines for every engine action.

### Non-goals (v1.10)

- No workflow spec/template objects and no operator authoring UI — the
  leader's tool calls are the only graph-authoring surface this wave
  (operator-side authoring is a natural fast-follow once the substrate
  exists).
- No loops, no conditionals, no OR-joins — dependencies are AND-joins over
  an acyclic graph. A "round" (werewolf nights, retry loops) remains leader
  judgment.
- No `cancelled`/`failed` task states — the 5-state machine gains exactly
  one state (`blocked`). Rework stays `verifying → running`; abandoning a
  graph branch is a leader force-unblock or simply never dispatching.
- No automated machine-evidence verify gate (build/test at verify time).
  The `verify` field is deliberately an enum so a future wave can add
  `"checks"` without schema change — that wave complements this one.
- No cross-space graphs, no persistent "workflow" entity with its own
  lifecycle, no graph visualization beyond the board column + badges.
- Spawning is cloning: a spawn reuses an existing member's definition
  verbatim (new name only). Authoring novel definitions at runtime stays
  operator territory (`CreateMember`, RP-8).

---

## 3. Verified current state

### 3.1 Every hop costs a leader wake

The shipped flow (all in `internal/swarm/tools/tasks.go`): `task_create`
requires an assignee at creation (push model, tasks.go:149; store enforces
at `store/tasks.go:92-94`) and always lands `pending`; `task_assign`
transitions `pending → running` and mails the assignee the spec
(tools/tasks.go:160-195, the `Bus.Send` at :188); the worker reports via
`send_message`; the leader wakes, `task_update_status → verifying`
(:209-226), inspects, `task_verify` (:253-260). Chaining task B after task A
therefore requires the leader to wake on A's report, verify A, then
create/assign B — the dispatch half of that wake is pure relay.

### 3.2 The ledger is flat and strictly leader-written

- `legalTransitions` (store/tasks.go:80-86) is the authoritative 5-state
  machine; `Task` (tasks.go:41-53) has `ParentID` nesting but **no
  dependency edges**.
- `TransitionTask` (tasks.go:122-124) rejects any non-leader actor before
  reading the row; workers are read-only on the ledger (their only inlet is
  `task_propose`, RP-23).
- Migrations run 0001–0005 (`internal/swarm/store/migrations/`); the next
  free slot is 0006.

### 3.3 The roster is static at runtime (operator-only mutation)

- `Supervisor.AddMember` (supervisor.go:155) hot-loads an **on-disk**
  `agents/sub/<name>/` dir; `CreateMember` (supervisor.go:185) **writes** an
  agent dir then reuses the AddMember path (with rollback); `RemoveMember`
  (supervisor.go:222) tears down loop/roster/bus/agent, deletes the member's
  schedule, persists runtime — and the leader can never be removed. All are
  operator surfaces (web/CLI); no swarm tool exposes them.
- Member construction is already clone-shaped: `constructMember`
  (space.go:311) starts from `acfg := sp.cfg.Clone()` (:314) and applies
  per-member model/effort pins — nothing in the path requires a disk dir
  beyond the def already loaded at register time.
- `registerDef` (space.go:201) is the single chokepoint where the team
  protocol is injected (dir members) and `registerPersonaDef` (space.go:244)
  the equivalent for persona members — any clone path that re-enters these
  gets correct identity + protocol for free.

### 3.4 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| Persist-before-signal mail | `bus.Bus.Send`/`deliver` (bus.go:100,152) | Auto-dispatch mail is durable by construction; a dropped hint is recovered from the DB |
| Assignment-mail body | `newTaskAssign` (tools/tasks.go:160-195) | Extract and share — engine dispatch sends the *same* mail a manual assign sends |
| Wake-loss backstop | `rescanTick` (scheduler.go:505, 8 s) | The idempotent sweep pattern the dispatch-recovery sweep joins |
| Member teardown | `Supervisor.RemoveMember` (supervisor.go:222) | Auto-retire is exactly this call, triggered by the engine instead of an operator |
| Runtime overrides persistence | `runtimeState`/`persistRuntime` (resume.go:27,66) | Where spawned-member records live and restart-resume reads them |
| Resume ordering | `Reload` (resume.go:125) — requeue after inbox exists | Spawned members re-clone in the same window manifest members reconstruct |
| Watchdogs | stall sweep (scheduler.go:315), workflow sweep (workflow_watch.go:40) | A stuck chain already pings the leader; blocked tasks need no new watchdog |
| Budget meter | `BudgetFor`/`addDailyUsage` (usage.go:52,70) | Keyed by member name — spawned members meter and freeze like anyone else |
| Synthetic event-log lines | `scheduleChangeEvent` et al. (service/service.go:1408-1609) | The exact pattern for engine actions that have no member tool-call to ride |
| Open event vocabulary | `event.Kind` (pkg/event/event.go:29) | "New kinds are added by extending this list" — task/member workflow kinds slot in |
| Role→tool gating | `toolNamesForRole` (tools/set.go:89), auto-allow safelist (:55-64) | Where `task_done`/`member_spawn`/`member_retire` register |
| Web form tool denylist | `SelectableTools` deny map (service.go:1704) | New coordination tools join the existing task-tool entries |

---

## 4. The writer decision: the ledger stays judgment-single-writer

The single-writer invariant is the ledger's load-bearing wall — but its
*purpose* is that all **judgment** flows through one agent, not that one
goroutine holds the pen. This wave splits the two: judgment stays exclusively
the leader's; the pen is shared with two strictly mechanical writers whose
every permitted move executes structure the leader already declared.

| Edge | Leader | Assignee (worker) | System (engine) |
|---|---|---|---|
| `blocked → pending` | ✓ force-unblock, with note | — | ✓ when the last dependency completes |
| `pending → running` | ✓ `task_assign` | — | ✓ auto-dispatch, only for tasks created with deps |
| `running → suspended` / `suspended → running` | ✓ | — | — |
| `running → verifying` | ✓ `task_update_status` | ✓ `task_done`, own task only | — |
| `verifying → completed` | ✓ `task_verify` | — | ✓ only when the task was created `verify:"auto"` |
| `verifying → running` (reject) | ✓ | — | — |

Why each new writer is safe:

- **System** never chooses an assignee, an order, or an outcome — assignee,
  dependencies, and verify policy were all fixed by the leader at
  `task_create`. Removing the system writer changes *when* things happen,
  never *what*.
- **Worker** gains exactly one edge, on exactly its own task, to exactly the
  state the leader would have set on receiving its report. `task_done`
  collapses the "worker mails → leader wakes → leader marks verifying" relay
  into one durable write plus (for `verify:"leader"`) one mail — the leader
  still performs the actual verification.
- Everything else — including both edges *out* of `verifying` for
  leader-verified tasks — remains `ErrNotLeader` (tasks.go:122).

Enforcement lives where it always has: in the store, as data
(`legalTransitions` grows one row; the actor check becomes the matrix above),
covered by table-driven tests over the full `(from, to, role, policy)`
product. This mirrors how the worktree PRD's §4 keeps the base branch
leader-owned: same wall, one more gate with a named key.

---

## 5. Design

### 5.1 D1 — Schema: dependency edges + verify policy (migration 0006)

```sql
CREATE TABLE task_deps (
    task_id       INTEGER NOT NULL REFERENCES tasks(id),
    depends_on_id INTEGER NOT NULL REFERENCES tasks(id),
    PRIMARY KEY (task_id, depends_on_id)
);
CREATE INDEX idx_task_deps_on ON task_deps(depends_on_id);
ALTER TABLE tasks ADD COLUMN verify_policy TEXT NOT NULL DEFAULT 'leader';
```

- `Status` gains `StatusBlocked = "blocked"`; `legalTransitions` gains
  exactly one row: `blocked → {pending}`. `completed` stays terminal.
- `CreateTask` accepts `DependsOn []int64` + `VerifyPolicy`; every dep must
  reference an **existing** task (else `ErrDepNotFound`); a dep on an
  already-`completed` task is satisfied at birth. The task lands `blocked`
  iff any dep is incomplete, else `pending` — a depless create is
  byte-identical to today.
- **Acyclic by construction.** Dependencies may only reference tasks that
  already exist and are immutable after creation, so a cycle can never form
  and no cycle-detection code exists. Changing the graph is additive
  (create more tasks) or a leader force-unblock; there is no dep edit.
- Force-unblock is the leader-actor `blocked → pending` with a note — the
  escape hatch when a branch is abandoned or a dep turns out irrelevant.
  A force-unblocked task with deps is *still* engine-managed (5.2): it
  auto-dispatches from `pending` like any unblocked dep-task.

### 5.2 D2 — Engine: unblock + auto-dispatch, recovery by sweep

One rule decides who dispatches: **a task with ≥1 dependency row is
engine-managed; a task with none is leader-managed exactly as today.** The
leader's manual `task_create` → `task_assign` flow is untouched for depless
tasks, and `task_assign` on a `blocked` task fails with the blocking IDs.

The hot path is synchronous and rides the completing tool call. Both
completion points — leader `task_verify {approve}` and the `verify:"auto"`
path (5.3) — converge on one space-level hook after the `completed`
transition commits:

```
OnTaskCompleted(id):
  ready := store.UnblockDependents(id)   // one tx under the store mutex:
                                         // dependents of id whose unmet-dep
                                         // count is now 0 AND status=blocked
                                         // → blocked→pending→running (system)
  for t := range ready:
    Bus.Send(assignment mail to t.Assignee)   // the shared task_assign body,
                                              // annotated "(auto-dispatched)"
    emit workflow event line (5.6)
  retire-check the completed task's assignee (5.4)
```

Tools reach the hook through the `MemberContext.Space` pointer they already
hold (the `mc.Space.Bus.Send` pattern, tools/tasks.go:188).

Crash windows are closed by the existing philosophy — DB is truth, sweeps
make it converge (`bus/doc.go`'s store-first delivery guarantee, the 8 s
`rescanTick` at scheduler.go:505): the tick additionally sweeps for **engine-managed
tasks left `blocked` with all deps complete** (crash between the completed
commit and the unblock tx) and dispatches them. The unblock tx itself is
atomic, and the assignment mail is durable-before-signal, so each window is
at most one sweep period wide and every recovery is idempotent. A mail
delivery error after the transition surfaces exactly like manual
`task_assign`'s "set running but notifying failed" arm (tools/tasks.go:195):
an event line plus the RP-22 stale-task watchdog as the final backstop.

### 5.3 D3 — `task_done` + per-task verify policy

New worker tool (worker-only in `toolNamesForRole`):

```
task_done {task_id: 42, result: "<what was produced, where>"}
```

Ownership-checked (`task.Assignee == mc.Name`, running only). One store tx
writes `Result` and transitions `running → verifying` (worker actor). Then:

- `verify:"leader"` (default): one durable mail to the leader — "task #42
  done by qa: <result>". Net effect vs today: the leader's wake starts at
  *inspection* instead of bookkeeping, and the worker no longer needs the
  `send_message` + leader `task_update_status` relay.
- `verify:"auto"`: the engine immediately completes it (system
  `verifying → completed`) and runs `OnTaskCompleted` — the chain flows with
  **zero** leader wakes. No leader mail is sent (silence is the feature; the
  board and event log carry the record). The protocol (5.7) teaches the
  leader to reserve `auto` for mechanical, low-blast-radius steps.

`verify_policy` is validated at `task_create` (`leader`|`auto`, fail-fast),
stored as text so a future machine-evidence wave can add `"checks"` without
migration.

### 5.4 D4 — Ephemeral members: spawn, meter, retire

Two leader-only tools:

```
member_spawn {from: "backend-a", count: 2, retire: "on_complete"}
  → {spawned: ["backend-a-2", "backend-a-3"]}
member_retire {name: "backend-a-2"}    // spawned members only
```

- **Clone, don't author.** The space retains each member's `agentdef.Loaded`
  at register time (new small map, populated in `registerDef`/
  `registerPersonaDef` — space.go:201,244). `member_spawn` copies the base's
  def under a derived name and re-enters the existing chokepoints:
  `registerDef` (team protocol re-injected with the clone's name) →
  `constructMember` (space.go:311 — fresh config clone, same model/effort/
  budget/permission-mode resolution as the base) → `startMemberLoop`
  (scheduler.go:64) → `persistRuntime`. **No agent dir is written and the
  manifest is untouched** — the deliberate divergence from `CreateMember`
  (supervisor.go:185), which exists to author *new* definitions.
- **Naming.** `<base>-<n>` with `n` from a per-base monotonic counter
  persisted in `runtime.json` — names are never reused within a space's
  life, so a respawn can never resume a retired clone's transcript
  (session snapshots are keyed by member name).
- **Metering.** The budget meter and watchdogs key by member name
  (usage.go:52,70) — clones meter, freeze, and stall-alert like any member,
  each with its own daily cap inherited from the base's resolution.
- **Cap.** New `settings.max_members` (0 → default 16) bounds the **live
  roster size** at spawn time; a spawn past the cap fails with a targeted
  error naming the knob. Operator surfaces (`AddMember`/`CreateMember`) are
  deliberately not capped — the cap is a guardrail on the agent, not the
  human.
- **Auto-retire, edge-safe.** `retire:"on_complete"` (default) marks the
  clone retire-eligible when it has no incomplete assigned tasks. Retire
  never fires mid-run — `task_done` executes *inside* the clone's own run,
  so `OnTaskCompleted` only marks eligibility; the rescan tick retires
  eligible members that are **idle** with no incomplete tasks via the
  existing `RemoveMember` teardown (supervisor.go:222). ≤8 s of latency for
  a guaranteed-clean exit; transcripts and ledger history are retained
  (".vero ledger untouched", the v1 rule). `retire:"manual"` leaves the
  clone up for reuse across tasks until `member_retire` or operator action.

### 5.5 D5 — Restart-resume

- The graph needs nothing: deps, statuses, and verify policies live in
  `vero.db`, so a rebuilt space sees the same ledger — the existing tasks
  rule (resume.go doc). The dispatch-recovery sweep (5.2) covers a crash
  that separated a completion from its unblocks.
- `runtimeState` (resume.go:27) gains
  `Spawned []SpawnedMember{Name, From, Retire, Seq}`. `Reload`
  (resume.go:125) re-clones spawned members from the surviving base defs
  **in the same window** manifest members reconstruct — before unread mail
  is requeued, so a clone's mailbox exists when its mail returns (the §6.2
  ordering invariant). A record whose base no longer exists is dropped with
  a warning mail to the leader; its assigned tasks remain on the ledger for
  the leader to reassign.
- A `fresh` register (`evva swarm .`) discards spawned records with the
  other runtime overrides (manifest is authoritative — service.go:444
  semantics); a rebuild (`Reconcile`/`RunSpace`) keeps them.

### 5.6 D6 — Observability

- **Events.** Four new `event.Kind` strings — `task_unblocked`,
  `task_dispatched`, `member_spawned`, `member_retired` — emitted as
  synthetic event-log lines (the `scheduleChangeEvent` pattern,
  service.go:1408-1609) and published to the live WS feed, so both the
  console and the durable replay (`/chatlog`) carry every engine action.
- **Board.** `blocked` joins the status columns (6 columns); `TaskInfo`
  (webapi/api.go:294) gains `dependsOn []int64` + `verifyPolicy`; the task
  card renders dep badges ("⛓ #12 #14") and a `verify:auto` marker.
- **Roster.** `MemberInfo` (webapi/api.go:211) gains `ephemeral bool` +
  `spawnedFrom string`; the FE renders an ephemeral pill. `list_members`
  output marks clones the same way.
- **Metrics.** `spaceMetrics` (metrics.go:31) gains `AutoDispatches`,
  `MembersSpawned`, `MembersRetired`; surfaced in `/api/swarm/{id}/metrics`.
- **Form denylist.** `task_done`, `member_spawn`, `member_retire` join the
  task tools in the `SelectableTools` deny map (service.go:1704) — they are
  role-managed, never form-selectable.

### 5.7 D7 — Team protocol (`teamprompt.go`)

All updates land in the one place both member kinds share
(`teamProtocolSuffix`, teamprompt.go:46, riding `injectTeamProtocol` :32 and
the RP-29 `PromptSuffix` seam):

- `teamProtocolCommon` (:133): the ledger now has `blocked` + dependencies;
  what auto-dispatch means; deps are AND-joins and immutable.
- `leaderProtocol` (:155): plan the whole graph up front with `depends_on`;
  do **not** hand-assign engine-managed tasks (the engine dispatches them);
  reserve `verify:"auto"` for mechanical steps; use `member_spawn` for
  fan-out wider than the roster and let `on_complete` clean up; the graph is
  extended by creating tasks, not edited.
- `workerProtocol`: report completion with `task_done {result}` instead of
  the send_message relay; keep `send_message` for questions and coordination.

New tools register in the auto-allow safelist (set.go:55-64) with the same
justification as the existing task family: coordination is governance-shaped
(the store and the cap enforce), not a file/shell side effect — the real
permission boundary stays the worker's write tools.

### 5.8 D8 — Interaction with the v1.9 worktree wave

No code coupling; the waves are independently shippable in either order.
When both are on: a clone inherits the base's resolved `worktree` setting,
provisioning `swarm-<clone>` worktrees through the same `constructMember`
injection; auto-retire follows the SWT `RemoveMember` semantics (clean →
remove worktree, dirty → preserve + leader mail), so an unmerged clone
branch outlives its member and nothing is lost. The leader protocol's merge
step happens at verify time — i.e. *before* `on_complete` retire-eligibility
for `verify:"leader"` tasks. `verify:"auto"` + worktrees is called out in
the leader protocol as an anti-pattern (auto-completing unmerged work).

---

## 6. Work items

**DWF-1 — Store: graph schema + writer matrix.**
Migration 0006 (`task_deps`, `verify_policy`), `StatusBlocked`,
`legalTransitions` row, `CreateTask` deps/policy validation
(`ErrDepNotFound`, birth-state rule, acyclicity by construction),
`RoleSystem`, the §4 actor matrix in `TransitionTask`, `CompleteWork`
(result + `running → verifying` in one tx), `UnblockDependents` (atomic
unblock-and-mark-running, returns the dispatch set), dep-aware `GetTask`/
`ListTasks` loading.
*Accept:* table-driven tests over the full `(from, to, role, policy)`
matrix; dep validation (missing dep, completed dep, depless unchanged);
`UnblockDependents` flips only all-deps-complete blocked tasks and is
idempotent under repeat calls; migration applies cleanly to a populated
0005 database.

**DWF-2 — Engine: `OnTaskCompleted` + recovery sweep.**
The space-level hook (5.2) wired into `task_verify` and the auto-complete
path; the shared assignment-mail helper extracted from `newTaskAssign`
(tools/tasks.go:160) and annotated "(auto-dispatched)"; the `rescanTick`
dispatch-recovery sweep.
*Accept:* integration test — a 3-task chain with `verify:"auto"` tails runs
head-to-tail off one leader planning run with zero further leader wakes; a
simulated crash between completion and unblock is healed by the sweep within
one tick; mail rows exist before any wake signal (persist-before-signal
holds).

**DWF-3 — Leader tool surface.**
`task_create` gains `depends_on` + `verify`; `task_assign` refuses a
`blocked` task naming the blocking IDs; `task_list`/`task_get` render deps,
`blocked` status, and verify policy; force-unblock via the existing
`task_update_status` (leader `blocked → pending`).
*Accept:* tool-level tests — create-with-deps lands blocked, depless create
is byte-identical to today, assign-on-blocked errors with the IDs,
force-unblock dispatches an engine-managed task on the next completion or
sweep.

**DWF-4 — Worker `task_done` + verify policy execution.**
The 5.3 tool (ownership check, `CompleteWork`), the `verify:"leader"` leader
mail, the `verify:"auto"` auto-complete → `OnTaskCompleted` path.
*Accept:* a non-assignee (and the leader) is rejected; done-on-leader-verify
produces exactly one leader mail and no completion; done-on-auto completes
with no leader mail and cascades dependents; `workerProtocol` asserts
task_done replaces the report relay.

**DWF-5 — Ephemeral members.**
`member_spawn`/`member_retire` tools; the space clone path (retained
`Loaded` map, chokepoint re-entry, monotonic naming); `settings.max_members`
(fail-fast parse, spawn-time enforcement); budget/permission inheritance;
edge-safe auto-retire via the rescan tick; `SelectableTools` denylist
entries.
*Accept:* lifecycle test — spawn 2 clones off a template, each metered and
gated independently, cap blocks the N+1th spawn with the knob named,
`on_complete` retires only idle clones with no incomplete tasks (never
mid-run), transcripts and ledger rows survive retire; `member_retire`
refuses manifest members; a worker-role agent has neither tool.

**DWF-6 — Restart-resume.**
`runtimeState.Spawned` (+ per-base `Seq`), `Reload` re-clone ordering
(before mail requeue), fresh-vs-rebuild semantics, missing-base drop with
leader mail.
*Accept:* restart round-trip — spawn, assign, `StopSpace`, `RunSpace`: the
clone is back with its mailbox before requeue (its unread mail wakes it),
name counters never reuse a retired name, fresh register drops spawned
records, missing-base drop mails the leader once and leaves the tasks.

**DWF-7 — Observability: events, DTOs, board, roster, metrics.**
The four `event.Kind` lines through pump → eventlog/chatlog + live WS;
`TaskInfo`/`MemberInfo` fields; the web board `blocked` column + dep badges
+ `verify:auto` marker; ephemeral roster pill; metrics counters.
*Accept:* chatlog replay after a graph run shows dispatch/spawn/retire lines
in order; board groups `blocked` correctly and badges render dep IDs;
`/metrics` counts match a scripted scenario; FE reducer handles the new
kinds without console errors on a `event_log:false` space (graceful absence).

**DWF-8 — Docs + example + release rig.**
User guide (en, zh-tw): "dynamic workflows" section — the graph, verify
policies, spawn/retire, the cap, and the worktree interplay note (5.8). New
`examples/evva-swarm/fanout-refactor` (1 leader + 1 template worker; the
leader plans a fan-out → join graph and spawns clones). `CHANGELOG.md`
entry; `pkg/version/version.go` v1.10.0 cycle; the wave → minor row in
`CLAUDE.md` + `EVVA.md` (already added when this PRD landed).
*Accept:* docs in both languages; the example registers and runs against a
scratch dir; changelog entry present at the beta cut.

Sequencing: `DWF-1 → DWF-2 → {DWF-3, DWF-4} → DWF-6 → DWF-7 → DWF-8`, with
`DWF-5` startable after DWF-1 (it touches the ledger only through
retire-eligibility) and mergeable any time before DWF-6.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| DWF-1 | store unit tests extend the existing suite (pure-Go sqlite, no CGO) | seconds |
| DWF-2/3/4 | engine + tool integration tests on temp `.vero` stores; `-race` stays on ubuntu (existing split) | seconds each |
| DWF-5/6 | spawn/retire + restart tests reuse the space-lifecycle fixtures | seconds |
| DWF-7 | webapi DTO tests; FE type-check rides the existing web2 build (Node 24) | unchanged |
| all | no new Go dependencies; migrations stay embedded | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Writer-matrix drift (a future edge quietly widens system/worker power) | The matrix is data in one place (store), exhaustively table-tested; any new edge fails the product test until deliberately added |
| Runaway spawn (leader loops on `member_spawn`) | `max_members` cap fails loudly; every clone inherits a budget cap and the RP-13 breaker freezes it; protocol teaches fan-out sizing; `MembersSpawned` metric makes it visible |
| Wide fan-out wake storm (N clones woken at once) | The 256-slot mailbox drops only hints — rows are durable ("store first, then push the UUID", bus/doc.go) and the 8 s rescan converges; the werewolf example already runs 13 members on this substrate |
| `verify:"auto"` rubber-stamps broken work | Default is `leader`; protocol reserves `auto` for mechanical steps; completed is terminal so recovery is a new task, which the leader owns; the event log records every auto-completion |
| A dependency never completes → chain starves | Nothing new is stuck-prone: the RP-14 stall sweep and RP-22 stale-task sweep already alert the leader; `blocked` tasks are visible on the board; force-unblock is the manual override |
| Retire races an in-flight run | Retire is two-phase: eligibility on completion, execution only when idle, on the rescan tick — never mid-run (5.4) |
| Clone name/transcript collision across respawns | Per-base monotonic `Seq` in `runtime.json`; names never reused within a space's life |
| Migration 0006 on live spaces | Purely additive (new table + defaulted column); old rows behave identically (`verify_policy='leader'`, no deps); existing no-downgrade policy unchanged |
| Auto-dispatch fights the leader's manual flow | The engine only touches tasks born with deps; depless tasks are leader-managed byte-identically to today (5.2's one rule) |
| Worktree-wave interplay surprises | 5.8: independent shipping order, inherit-on-spawn, dirty-preserve on retire; `auto`+worktree called out as an anti-pattern in the protocol |

---

## 9. Open questions

1. **Default `max_members` value?** Recommend 16 — double the largest
   shipped example (werewolf's 13) minus headroom for the leader; a knob,
   not a wall.
2. **Should `verify:"auto"` completions mail the leader an FYI?** Recommend
   no — silence is the feature; the board, metrics, and event log carry the
   record, and a leader that wants a checkpoint puts a `verify:"leader"`
   task at the join.
3. **Annotate auto-dispatch mail?** Recommend yes — "(auto-dispatched)" in
   the assignment mail tells the worker no leader run preceded it, which
   matters when it reads ambiguity into the spec (ask the leader, don't
   guess).
4. **Should `member_retire` work on manifest members?** Recommend no —
   manifest membership is the operator's contract; the leader retires only
   what it spawned.
5. **Ship order vs v1.9 (worktrees)?** Recommend strictly independent — no
   shared code paths beyond `constructMember` re-entry, and 5.8 defines the
   combined behavior whichever lands first.

---

## 10. Rollout

1. DWF-1..DWF-8 land via `feature/swarm-dynamic-workflow` → `dev` (normal
   PR flow).
2. `pre-release feature` cuts `v1.10.0-beta.1` — this wave claims the v1.10
   minor (v1.9 is claimed by the worktree wave; whichever cuts first takes
   its own number, per the base-version decision).
3. Manual validation on the beta: the `fanout-refactor` example end-to-end
   (plan → auto-dispatch chain → spawn ×3 → join → retire ×3), plus a
   restart mid-graph; then a mixed swarm confirming depless flows are
   untouched.
4. `release` promotes to `v1.10.0`.
