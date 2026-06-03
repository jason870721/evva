# SPRD-1-7 — Swarm custom tools: `task_*`, `send_message`, `list_members`

> Milestone: M1 (task_*/list_members) / M2 (send_message) ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-2, 1-5, 1-4
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

- [ ] `ToolSet.For` returns role-correct tool options (the 1-4 injection seam).
- [ ] `task_*` (Leader-write / Worker-read), `send_message` (sender baked), `list_members`
      implemented as `pkg/tools.Tool`.
- [ ] Write-class tools permission-gated (invariant #6); leader-only enforced via the store.
- [ ] Tests green incl. role split + assign-wake; no `internal/agent` import (invariant #1).
