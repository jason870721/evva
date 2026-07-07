# PRD — Solo Dynamic Workflow (SDW) — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** accepted — implementation lands with this PRD on
> `feature/dynamic-workflow`.
> **Target release:** `v1.11.0` (this wave claims the v1.11 minor per the
> CLAUDE.md wave → minor rule; first cut ships as `v1.11.0-beta.1`).
> **Roadmap source:** operator request 2026-07-06 — "we already have swarm
> dynamic workflow for swarm mode; now let evva tui use it. Default
> disabled, toggleable via TUI /config."
> **Parent design:** [swarm-dynamic-workflow.md](swarm-dynamic-workflow.md)
> (DWF, v1.10) — this wave is the deliberate solo port of its execution
> model. Shared vocabulary (`depends_on`, `blocked`, verify policy,
> auto-dispatch, writer matrix) is an acceptance criterion, not a
> coincidence.
> **Boundary:** [workflow-scripts.md](workflow-scripts.md) (W16 concept)
> is the *static* declarative counterpart — YAML authored up front, engine
> executes, no model planning. SDW is the *dynamic* counterpart: the root
> agent authors and extends the graph at runtime with tool calls. Both run
> on the same dispatch philosophy; WFS can later reuse SDW's engine.

---

## 1. TL;DR

The swarm's DWF wave made the ledger a workflow engine: the leader plans a
dependency graph once; the engine dispatches each task the moment its
prerequisites complete; `verify:"auto"` chains flow with zero leader wakes.

Solo mode has the same disease DWF cured — worse, because the "leader" is
the user-facing conversation. The root agent can already fan out async
subagents in worktrees (the AGENT tool), but **every hop between dependent
steps costs a root-agent turn**: worker B cannot start until the root wakes
on worker A's report, re-reads context, and hand-spawns B. Multi-step
delegation burns root context on pure relay — the exact "half the wakes are
bookkeeping" pattern DWF §1 describes.

This wave gives the solo root agent a **workflow task board** and an
**in-process dispatch engine**, gated behind a default-off config flag
(`enable_dynamic_workflow`, toggleable in the TUI `/config` overlay):

1. **Graph-planning tools.** `wf_task_create {subject, description,
   depends_on, verify, worker}` builds a dependency graph at runtime. A
   task with a `worker` spec (`agent_type`, optional `isolation:
   "worktree"`, model `level`) is **engine-managed**: the engine spawns an
   ephemeral async subagent for it the moment it is unblocked — no root
   wake. A task without `worker` is a **self-task** — a step the root does
   itself, tracked on the same graph.
2. **Verify policy.** `verify:"leader"` (default) folds the worker's result
   into the root conversation for judgment; `verify:"auto"` completes
   mechanically and cascades dependents with **zero root wakes** — the
   board and the panel carry the record.
3. **Board surfaces.** A TUI board panel (statuses, ⛓ dependency badges,
   `auto` chips, live spinner per running worker) plus a session
   `local_workflow` daemon — the reserved `daemon.KindLocalWorkflow` kind
   finally earns its slot as the engine's presence in the daemon catalog
   and the channel for its wake messages.

The governing invariant is DWF §4's, verbatim: **every decision is still
the root agent's**. The engine holds no judgment — assignee specs,
dependencies, and verify policies are all fixed at `wf_task_create` time;
the engine only executes declared structure.

Ephemeral fan-out members (DWF's `member_spawn`) need no port: solo workers
are natively ephemeral subagents. The roster cap maps to a
`workflow_max_workers` concurrency cap (default 4).

---

## 2. Goals / non-goals

### Goals

- The root agent plans a whole dependency graph in one turn; from then on
  the engine dispatches each engine-managed task when its prerequisites
  complete, bounded by `workflow_max_workers`. Root turns are spent on
  judgment (verification, replanning, integration), never on relaying a
  dispatch already decided.
- `verify:"auto"` chains complete with zero root wakes; one "workflow
  settled" wake summarizes the run when nothing is left running or
  dispatchable.
- Dependency results flow: a worker's prompt embeds the results of the
  tasks it depended on, so chains compose without shared state.
- Default-off and inert: with the flag off (the default), the build is
  byte-identical to today — same tools, same prompt, no engine, no
  goroutines. The flag is a `/config` toggle following the
  `enable_repo_map` pattern (effect on next agent boot / profile switch).
- Restart-safe: the board is an append-only jsonl op-log keyed by session
  id, replayed on resume; tasks whose workers did not survive the restart
  return to `pending` and re-dispatch.
- Swarm-inert: wire names are `wf_`-prefixed (the swarm owns `task_create`
  et al. process-wide via `agent.WithCustomTool`), and swarm-resident
  personas (`LongRunning` defs) never mount the solo board even when the
  host config carries the flag.

### Non-goals (v1.11)

- No worker-side board tools. Workers receive their task spec (and
  dependency results) in the spawn prompt and report by finishing — the
  solo analog of `task_done` is the subagent's terminal report, recorded
  by the engine. A `my_tasks` read surface for workers is a natural v2.
- No `suspended` status. DWF inherited it from the Veronica ledger; solo
  pause is "don't create the task yet" or a leader force-hold by simply
  not verifying. The status set is `blocked / pending / running /
  verifying / completed` plus a `deleted` op.
- No loops, conditionals, or OR-joins — AND-joins over an acyclic graph,
  exactly DWF. Rounds and retries are root-agent judgment.
- No cross-session boards, no board web API, no graph visualization beyond
  the TUI panel.
- No machine-evidence verify gate (`verify:"checks"`) — the field is an
  enum string so that wave slots in without a schema change, same as DWF.
- Not the swarm and not workflow-scripts: no standing members, no mailbox,
  no SQLite, no YAML spec. Transient crew, model-planned.

---

## 3. Verified current state (dev@bbe8a50)

- **Dispatch half exists.** `internal/tools/meta/agent.go` (AGENT tool) →
  `Agent.Spawn` (`internal/agent/spawn.go:40`): async subagents
  (goroutine + `agentDaemon.Report` → terminal Lifecycle → root drain),
  worktree isolation (`mode.CreateForSubagent`, spawn.go:63), model tiers
  (`ModelForLevel`, spawn.go:54). Every async completion wakes the root
  via the signal queue (`drainDaemonSignals`,
  `internal/agent/drain_daemons.go:23`) — there is no quiet path.
- **No coordination half.** `todo_write` (`pkg/tools/todo`) is a flat,
  ephemeral, in-memory list: no dependencies, no owner, no persistence, no
  engine. The daemon catalog (`pkg/tools/daemon`) reserved
  `KindLocalWorkflow` (kind.go:35) with id prefix `w` and no impl.
- **Config + /config precedent.** `enable_repo_map` is the template: *bool
  omitempty in `FileConfig` (file_config.go:88), default-false
  normalization (load.go:243), mutex Get/Set (config.go:410/418), SaveFile
  literal (config.go:863), `cfgKindBool` entry in `buildConfigFields`
  (overlays/config.go:458). Feature flags take effect at the next agent
  boot / profile switch.
- **Profile gating seams.** `mainProfileForDef`
  (`internal/agent/profiles.go:163`) assembles active/deferred tools and
  the sysprompt `Context`; `def.LongRunning` already strips solo-only
  tools for swarm-resident personas (profiles.go:209); prompt sections
  gate on `Context` flags (auto-memory pattern,
  `sysprompt/main_agent.go:217`).
- **Name collision is real.** `internal/swarm/tools/set.go:17-37` registers
  `task_create`, `task_list`, `task_get`, … process-wide through
  `agent.WithCustomTool` — solo names must differ.

---

## 4. The writer matrix (ledger stays judgment-single-writer)

DWF §4 split judgment from the pen; SDW keeps the same wall with solo
actors. The **root** agent is the only judgment writer; the **system**
(engine) performs strictly mechanical edges executing structure the root
declared. There is no worker actor — workers have no tools.

| Edge | Root | System (engine) |
|---|---|---|
| `blocked → pending` | ✓ force-unblock, with note | ✓ when the last dependency completes |
| `pending → running` | ✓ self-task only (`wf_task_update`) | ✓ auto-dispatch, engine-managed tasks only |
| `running → verifying` | ✓ self-task only | ✓ worker terminal report (result recorded) |
| `verifying → completed` | ✓ `wf_task_verify {approve:true}` | ✓ only when the task was created `verify:"auto"` |
| `verifying → pending` (reject / rework) | ✓ `wf_task_verify {approve:false}` | ✓ worker lost on restart-resume, with note |
| `running → pending` (worker lost) | — | ✓ restart-resume reset, with note |
| `pending/blocked → deleted` | ✓ `wf_task_update {status:"deleted"}` (refused while running or depended-on) | — |
| self-task `pending → running → completed` | ✓ | — |

Two deliberate divergences from DWF, both forced by worker ephemerality:

1. **Reject re-dispatches instead of resuming.** DWF's `verifying →
   running` reject hands the task back to a *living* member. A solo worker
   is gone after its report, so reject lands the task back in `pending`;
   the engine re-dispatches a fresh worker (the root should
   `wf_task_update` the description first so the rework instruction
   travels). A `verify:"auto"` task never rejects by definition.
2. **Worker failure forces leader verify.** A crashed/killed worker moves
   the task to `verifying` with the failure recorded as its result and the
   effective policy forced to `leader` — `auto` must never rubber-stamp a
   crash.

Enforcement lives in the store as data — one transition table keyed by
`(from, to, actor)`, exhaustively table-tested, exactly like DWF-1.

---

## 5. Design

### 5.1 D1 — Board store (`pkg/tools/workflow`)

Public package (embedder-reusable, sibling of `pkg/tools/todo`), no agent
imports:

```go
type Task struct {
    ID          string   // base36, monotonic per session
    Subject     string
    Description string   // the worker's spec — written to be executable by another agent
    ActiveForm  string   // spinner text while running
    Status      Status   // blocked | pending | running | verifying | completed
    Verify      Verify   // leader | auto
    Worker      *WorkerSpec // nil = self-task (root's own step)
    DependsOn   []string
    Owner       string   // daemon id of the running worker; "" otherwise
    Result      string   // worker's final report (or failure note)
    Comments    []Comment // append-only audit: force-unblocks, verdicts, resets
    CreatedAt, UpdatedAt time.Time
}
type WorkerSpec struct {
    AgentType string // explore | plan | general-purpose | <disk agent>
    Isolation string // "" | "worktree"
    Level     int    // model tier, as the AGENT tool's level
}
```

- Dependencies may only reference **existing** tasks and are immutable
  after creation → acyclic by construction, no cycle detection (DWF 5.1).
  A dep on a completed task is satisfied at birth; a task lands `blocked`
  iff any dep is incomplete.
- Persistence is an **append-only jsonl op-log** at
  `<AppHome>/workflows/<session-id>.jsonl` — one `{op, id, fields…, ts}`
  record per mutation, replayed on `SetSession`. Same-session resume
  rebuilds the board; `/clear` rotates to the new session id (fresh
  board), mirroring `checkpoints.SetSession`. Replay tolerates CRLF and
  unknown fields; the id counter resumes past the max seen.
- The store embeds `pkg/observable.Observable` (Domain `"workflow"`) and
  notifies a full snapshot after every mutation — the TUI re-renders via
  the existing `KindStoreUpdate` bridge with zero new wiring
  (`ToolState.RegisterStore` pattern).

### 5.2 D2 — Engine: one idempotent sweep

`internal/agent/workflow_engine.go`. The engine is in-process and
synchronous — no ticker; solo needs no crash-window sweep because the
hooks are direct function calls. One primitive, DWF-2's shape:

```
DispatchReady():                       // idempotent; called after every
  for t in store.Dispatchable():       // readiness-changing mutation and
    if runningWorkers >= cap: break    // once at session resume
    store.Transition(t, running, system)   // + Owner = daemon id
    spawnQuiet(t)                      // async subagent, no root Lifecycle

onWorkerDone(taskID, result, err):     // the worker's terminal report
  store.CompleteWork(taskID, result)   // running → verifying, result recorded
  if err != nil → force leader verify; signal("task #N failed …")
  else if verify == auto:
      store.Transition(completed, system)
      ready := store.UnblockDependents(taskID)
      DispatchReady()                  // cascade, zero root wakes
  else:
      signal("task #N done by worker (verify with wf_task_verify): …")
  if settled() && hadSilentActivity → signal(settled summary)
```

- **Dispatch prompt.** Subject + description + a `## Completed dependency
  results` section (each dep: `#id subject → result`, individually capped)
  — results flow down the chain without shared state. The prompt is
  immutable once dispatched (auditability, same rule as workflow-scripts).
- **Quiet workers.** Engine-spawned subagents register the same
  `agentDaemon` (TUI strip, `daemon_list`, `daemon_stop` all work) but
  suppress the terminal Lifecycle signal — the engine owns reporting.
  The engine evicts each worker daemon after recording its result.
- **Wake messages** ride a session **workflowDaemon**
  (`daemon.KindLocalWorkflow`, id `w…`) registered when the feature wires
  up: the engine emits Event signals through it, and the existing
  `drainDaemonSignals` renders them and wakes an idle root — no new drain
  path. `verify:"leader"` completions and worker failures each emit one
  line; `verify:"auto"` completions are silent; a settled graph emits one
  summary line. `Kill()` on the workflow daemon pauses auto-dispatch (the
  operator's brake); the board tools keep working.
- **Cap.** `workflow_max_workers` (default 4, floor 1) bounds concurrent
  engine workers; excess ready tasks stay `pending` and dispatch as slots
  free — the solo analog of DWF's `settings.max_members` guardrail.

### 5.3 D3 — Tools (root-only, flag-gated)

Five tools; wire names `wf_task_create / wf_task_update / wf_task_verify /
wf_task_list / wf_task_get` (snake_case inputs, swarm-collision-free).
There is deliberately **no `wf_task_assign`** — dispatch is the engine's
job; and no separate `task_done` — that edge belongs to the system actor.

- `wf_task_create {subject*, description, active_form, depends_on?: [],
  verify?: "leader"|"auto", worker?: {agent_type, isolation?, level?}}`
  → id; validates deps exist, `verify` enum, worker agent_type against the
  spawner's live list. Creating a ready engine-managed task triggers
  `DispatchReady()`.
- `wf_task_update {task_id*, subject?, description?, active_form?,
  status?, note?}` — the root's manual writer: force-unblock, self-task
  transitions, `deleted`. Status changes route through the writer matrix
  (root actor); illegal edges return the matrix row in the error.
- `wf_task_verify {task_id*, approve*, note?}` — `verifying → completed`
  (cascades dependents) or `→ pending` (rework; engine re-dispatches).
- `wf_task_list {status?}` / `wf_task_get {task_id*}` — summaries / full
  detail including comments and result.

All five join `permission.ReadOnlyOrSelfTools`: board coordination is
governance-shaped (the store's matrix enforces), not a file/shell side
effect — the DWF safelist rationale verbatim. The real permission boundary
stays the workers' own file/shell tools, which gate as usual inside each
subagent.

### 5.4 D4 — Profile + prompt gating

In `mainProfileForDef`, when `cfg.GetEnableDynamicWorkflow() &&
!def.LongRunning`:

- `todo_write` is **swapped out** for the five board tools. One list, one
  mental model — the board subsumes the todo list (task-design.md's v1→v2
  replacement intent; ref's Task tools are its todo evolved). Flag-off
  sessions keep `todo_write` byte-identically.
- `ctx.EnableDynamicWorkflow` gates a new system-prompt section (the
  auto-memory section mechanism): plan the whole graph up front; worker
  tasks for delegable steps, self-tasks for your own; deps are immutable
  AND-joins — extend the graph by creating tasks; reserve `verify:"auto"`
  for mechanical, low-blast-radius steps; `auto` + worktree isolation is
  an anti-pattern (auto-completing unmerged work — DWF 5.8 verbatim);
  never hand-spawn AGENT workers for engine-managed tasks; on reject,
  update the description before the engine re-dispatches; the board is
  the single source of truth — keep it honest.

Subagent profiles are untouched (workers get no board tools); disk main
personas keep their own declared tool lists and may opt in by declaring
`wf_task_*` names themselves.

### 5.5 D5 — Agent wiring & restart-resume

`agent.New`, root-only, and only when the resolved profile actually mounts
`wf_task_create` (single source of truth = the toolset, so swarm members
and flag-off sessions wire nothing):

- store: `SetPersistence(<AppHome>/workflows)` + `SetSession(a.ID)`;
- engine: dispatch bound to the quiet spawn path, signals bound to a
  freshly registered workflowDaemon, cap from config;
- resume: `SetSession` replays the board, then `ResetLostRunning` (running
  tasks whose owner daemon no longer exists → `pending` with a comment)
  and one `DispatchReady()` sweep — the restart recovery;
- `/clear`: `SetSession(newID)` → fresh board (alongside the existing
  `TodoStore().Clear()` + `checkpoints.SetSession`).

### 5.6 D6 — TUI board panel

`pkg/ui/bubbletea/components/workflow/panel.go`, modeled on the todos
panel: one row per task — `✓` completed, spinner while `running` with a
live owner daemon (cross-referenced against `KindLocalAgent` snapshots,
the agents-strip pattern), `▸` running/verifying otherwise, `○` pending,
`⛓ #a #b` badges on blocked tasks, an `auto` chip on `verify:"auto"`.
Renders whenever the board is non-empty; folds to a one-line summary when
the graph settles all-completed. Controller gains `WorkflowTasks()`
(nil-safe when the feature is off).

---

## 6. Work items

**SDW-1 — Config.** `enable_dynamic_workflow` (*bool, default false) +
`workflow_max_workers` (int, 0 → 4, floor 1): FileConfig, Load
normalization, Config fields + mutex Get/Set, SaveFile literal, `/config`
overlay entries (bool + int), round-trip test.
*Accept:* flag round-trips through YAML; absent keys default off/4;
`/config` toggles in place.

**SDW-2 — Store.** `pkg/tools/workflow`: types, writer-matrix
`Transition`, dep validation (`ErrDepNotFound`, blocked birth, satisfied
completed deps), `CompleteWork`, `UnblockDependents`, `Dispatchable`,
`ResetLostRunning`, delete guards, jsonl op-log + replay, observable
notifications.
*Accept:* table-driven tests over the full `(from, to, actor)` product;
replay rebuilds state + id counter across CRLF logs; unblock is
idempotent; delete refused while running or depended-on.

**SDW-3 — Tools.** The five `wf_task_*` tools bound to the store +
engine-sweep hook; `pkg/tools/name.go` constants; `builtins.go`
registration; `ToolState.WorkflowStore()` accessor; permission safelist.
*Accept:* tool-level tests — create-with-deps lands blocked; illegal
transitions name the matrix row; verify approve cascades; reject
re-queues.

**SDW-4 — Quiet spawn seam.** Extract the shared spawn body in
`spawn.go`; `agentDaemon` gains a `quiet` flag (Report/Crush skip the
Lifecycle emit); engine path takes an `onDone` callback. Tool-path
behavior byte-identical.
*Accept:* existing spawn tests untouched and green; a quiet async worker
produces no drain signal but is visible to `daemon_list` and killable.

**SDW-5 — Engine + workflow daemon.** `workflow_engine.go`
(DispatchReady, onWorkerDone, verify policy, dep-results prompt, cap,
settled summary, worker-daemon eviction) + `workflow_daemon.go`
(`KindLocalWorkflow` impl, `LocalWorkflowMeta` in
`pkg/tools/daemon/snapshot.go`, Kill = pause) — engine core tested with
fake dispatch/signal functions, no LLM.
*Accept:* a 3-task `verify:"auto"` chain runs head-to-tail off one
planning pass with zero root signals; `verify:"leader"` emits exactly one
signal per completion; a worker failure forces leader verify; the cap
queues the N+1th ready task.

**SDW-6 — Profile + prompt + agent wiring.** profiles.go swap +
`ctx.EnableDynamicWorkflow`; the protocol section; agent.New wiring +
resume/clear hooks (5.5).
*Accept:* gating tests — flag ON mounts the five tools and drops
`todo_write`; OFF is byte-identical to today; `LongRunning` defs never
mount; prompt section present iff flag on.

**SDW-7 — TUI.** Board panel + `Controller.WorkflowTasks()` + root.go
relayout/View wiring.
*Accept:* panel renders all five statuses, dep badges, auto chip; spinner
tracks a live worker; folds when settled.

**SDW-8 — Docs + release rig.** User guide (en, zh-tw): enabling via
`/config`, the loop, verify policies, the cap, worktree interplay + the
`auto`+worktree anti-pattern; CHANGELOG `[Unreleased]` entry; the wave →
minor row in CLAUDE.md + EVVA.md; overview.md §2 row.
*Accept:* docs in both languages; changelog present; wave row appended.

Sequencing: SDW-1 → SDW-2 → SDW-3 → SDW-4 → SDW-5 → SDW-6 → SDW-7 → SDW-8
(SDW-4 startable any time after SDW-1).

---

## 7. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Writer-matrix drift | Matrix is data in one table, exhaustively product-tested (DWF-1's defense) |
| Runaway fan-out / cost surprise | `workflow_max_workers` cap (default 4) fails soft (tasks queue); protocol teaches sizing; every worker is a visible daemon the operator can kill |
| `verify:"auto"` rubber-stamps broken work | Default is `leader`; failures force leader verify; protocol reserves `auto` for mechanical steps; the settled summary + board record every auto completion |
| Quiet workers hide progress | Quiet suppresses only the *conversation* signal — the TUI agents strip, board panel, `daemon_list`, and `daemon_output` all still see every worker |
| Root context bloat from big results | Dependency results are capped per-entry in dispatch prompts; leader-verify signals carry a bounded summary; full results live on the board (`wf_task_get`) |
| Stale board after crash mid-dispatch | Op-log is truth; resume resets lost running tasks to pending with a comment and re-sweeps — at-least-once dispatch, idempotent by task id |
| Two-list confusion (todo vs board) | The swap: flag on ⇒ board replaces `todo_write` wholesale |
| Swarm collision / regression | `wf_` prefix; `LongRunning` exclusion; wiring keyed off the mounted toolset, not the bare flag |

---

## 8. Resolved questions

1. **Cap default?** 4 — a laptop-honest ceiling; DWF's 16 assumes a
   service host.
2. **Signal per auto completion?** No — silence is the feature (DWF open
   question 2, same answer); the settled summary is the checkpoint.
3. **`suspended`?** Dropped for v1.11 (§2 non-goals) — the enum stays
   string-typed so it can return without migration.
4. **Worker read access to the board?** Deferred to a v2 `my_tasks`-style
   read surface; v1 embeds everything the worker needs in its prompt.

---

## 9. Rollout

1. SDW-1..8 land via `feature/dynamic-workflow` → `dev` (normal PR flow).
2. A later `pre-release feature` cuts `v1.11.0-beta.1` — this wave claims
   the v1.11 minor.
3. Manual validation on the beta: a fan-out → join graph (2 parallel
   `verify:"auto"` workers → 1 `verify:"leader"` integrator) in the TUI,
   a restart mid-graph, a `/clear` reset, and a flag-off session
   confirming baseline behavior.
4. `release` promotes to `v1.11.0`.
