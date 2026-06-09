# SPRD-1-7 — Swarm custom tools: `task_*`, `send_message`, `list_members`

> Milestone: M1 (task_*/list_members) / M2 (send_message) ｜ Status: IN REVIEW ｜ Owner: (unassigned) ｜ Depends on: 1-2, 1-5, 1-4
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 5) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §5.3, §6.1, §7.1

## 1. Goal

The agent-facing **`pkg/tools.Tool` set** that lets a swarm collaborate: the Leader
drives the task ledger (`task_*`), every agent reads the roster (`list_members`) and
sends mail (`send_message`, with the sender baked in per agent). This ticket implements
the `ToolSet` interface that 1-4 injects, so **permission = tool set**: Leaders get the
write tools, Workers get read-only task tools.

## 2. Scope

**In:**
- `ToolSet` impl: `For(name, role, sp) []agent.Option` returning `WithCustomTool(...)`
  per the role's tools (the seam 1-4 declared).
- **Leader tools** (write): `task_create` (push: `assignee` required, status=pending),
  `task_assign` (→running + bus message to the assignee), `task_update_status`,
  `task_verify` (approve→completed / reject→running), `task_list`.
- **Worker tools** (read-only on tasks): `my_tasks`, `task_get`.
- **All agents**: `send_message` (one instance per agent; **sender baked into the
  closure** — §6.1), `list_members` (read-only roster snapshot — §5.3).
- **Permission** (invariant #6): status-writing + `task_assign` are write-class → gated
  by `pkg/permission`; reads auto-allow.

**Out:** the state-machine enforcement itself (lives in 1-2's `store`; tools call it and
surface the typed error); the wake on assignment (1-6 hooks it); domain (trading) tools
(Phase 2).

## 3. Dependencies & what this unblocks

- Depends on: 1-2 (store DAO + state machine), 1-5 (`bus.Send` for `send_message` /
  `task_assign`), 1-4 (`SwarmSpace`/`Roster` the tools read & act on).
- Unblocks: 1-8 (web surfaces the tasks/messages these produce), 1-13 (e2e collaboration).

## 4. Technical design

Package `internal/swarm/tools`. (Imports package `swarm` for `*SwarmSpace`; `swarm`
references only the `ToolSet` *interface*, so there is no import cycle — the concrete
`Set` is wired in at the service layer, 1-8.)

```go
type Set struct{} // implements swarm.ToolSet

func (Set) For(name string, role agentdef.Role, sp *swarm.SwarmSpace) []agent.Option {
    common := []agent.Option{ withSendMessage(name, sp), withListMembers(sp) }
    switch role {
    case agentdef.Leader:
        return append(common, withTaskCreate(sp), withTaskAssign(name, sp),
            withTaskUpdateStatus(sp), withTaskVerify(sp), withTaskList(sp))
    default: // worker
        return append(common, withMyTasks(name, sp), withTaskGet(sp))
    }
}
```

- Each tool is a `pkg/tools.Tool` (`Name/Description/Schema/Execute`) built by a factory
  closure capturing `(name, sp)`. The closures deref `sp` only at Execute time, so
  building them inside `NewSpace` (before `sp` is fully wired) is safe (late binding).
- `send_message`'s closure bakes the sender — Execute has no "who am I" (§6.1).
- `task_*` write tools call `sp.Store.TransitionTask(..., by=Actor{name,role})`; the
  **Leader-only** guard is enforced in the store (1-2); the tool just passes `by` and
  surfaces the typed rejection as a tool error.
- `task_assign` = set `running` **and** `bus.Send(assignee, msg)` so the assignee wakes (1-6).
- Tool descriptions in evva house style (imperative, schema-first); snake_case wire names.

## 5. Acceptance criteria

1. A Leader gets the write tool set; a Worker gets only `my_tasks`/`task_get` +
   `send_message`/`list_members` (assert `For()` output per role).
2. `task_create` requires an `assignee`; `task_assign` flips to `running` **and**
   delivers a message that wakes the assignee (observable via the bus).
3. A Worker invoking a status-write tool is rejected (store leader-only guard surfaces as
   a tool error, not a panic).
4. `send_message` persists a row with the correct baked `sender`; `to:"all"` broadcasts.
5. `list_members` returns the live roster snapshot (role/membership/run/currentTask).
6. Write-class tools route through `pkg/permission`; reads do not prompt.

## 6. Verification

- Unit tests per tool with an in-memory `store` + a fake `bus`: schema validation, the
  leader/worker split, assign-wakes-assignee, sender baking, permission gating.
- A table test asserting `For(role)` returns exactly the expected tool names.
- `go test -race ./internal/swarm/tools/...` clean.

## 7. Definition of Done

- [x] `ToolSet.For` returns role-correct tool options (the 1-4 injection seam).
- [x] `task_*` (Leader-write / Worker-read), `send_message` (sender baked), `list_members`
      implemented as `pkg/tools.Tool`.
- [x] Write-class tools permission-gated (invariant #6); leader-only enforced via the store.
- [x] Tests green incl. role split + assign-wake; no `internal/agent` import (invariant #1).

### Implementation design / decisions

- **Per-agent identity rides Config, not a closure.** The ticket assumed
  `send_message`'s sender (and `sp`) could be baked into the `For()` closure —
  but `pkg/agent.WithCustomTool` registers ONE factory per tool name
  process-wide (`if !reg.Has(name)`; the first wins) and `pkg/tools.State`
  doesn't expose the agent name. A closure would therefore freeze the *first*
  agent's identity onto every agent, and a closure-captured `*SwarmSpace` would
  leak across spaces. Fix: `constructMember` binds a `swarm.MemberContext`
  `{Name, Role, Space}` onto each agent's cloned `Config.CustomConfig` (the
  sanctioned "evva never reads this" downstream bag), and each factory reads it
  back at build time (`bind(ctor)`). So `For` only needs `role`; `name`/`sp` are
  unused. *Constraint:* a swarm agent's Config holds a live pointer and must
  never be serialized.
- **Permission = the name-keyed safelist.** The gate auto-allows
  `permission.ReadOnlyOrSelfTools[name]` and asks for everything else. `init()`
  registers the read/self tools (`send_message`, `list_members`, `task_list`,
  `my_tasks`, `task_get`, **and `task_create`**) into that exported map; the
  write-class mutators (`task_assign`, `task_update_status`, `task_verify`) stay
  out → default ask. `task_create` is treated as *planning* (auto-allow, per the
  ticket's "status-writing + task_assign" wording and pkg/permission's own doc
  note); the gated commit is `task_assign` — the moment a Worker is actually put
  to work. Gating only bites in a non-bypass space mode; the classification is
  asserted via `permission.Decide` directly (mode-independent of the manifest).
- **Tools are thin over the 1-2 store + 1-5 bus.** `task_assign` =
  `GetTask` → `TransitionTask(running, by=leader)` → `Bus.Send` to the assignee
  (the task wake source is a message, §5.5/§7.1). The leader-only guard lives in
  the store; tools pass `Actor{Name, Role}` and surface the typed rejection
  (`ErrNotLeader` / `ErrIllegalTransition` / `ErrTaskNotFound`) as a
  model-visible `Result{IsError}` — never a panic (AC#3). `send_message` does no
  recipient validation (the bus row is durable regardless; the model picks
  recipients via `list_members`, §6.1).
- **Tested without the LLM.** Tool constructors are package-level
  (`newTaskAssign(mc)` …), tested directly against a real store + bus (a lite
  `&SwarmSpace{Store, Bus}`); `list_members` and the end-to-end attach test use
  a stub-provider `NewSpace`. Coverage: role split, permission classification,
  sender-baking, broadcast, assign-wakes-assignee, worker-write rejection,
  illegal transition, verify approve/reject, list/get/my_tasks scoping, and the
  factory↔Config binding through real `agent.New`.
- **Out of scope (deferred):** wiring the concrete `Set{}` into live spaces is
  the service layer (1-8); the actual wake on an assignment message is the
  Supervisor (1-6, already landed); domain/trading tools are Phase 2.
