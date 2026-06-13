# Collaboration tools (auto-injected — never list these)

The runtime wires a set of coordination tools into every member based on its **role**. You do **not**
list them in `tools/active.yml` / `tools/deferr.yml`, and you do **not** explain them in a persona —
the runtime also injects the protocol for using them. This page exists so you know what each member
already has (and therefore what to leave out, and what you can rely on when writing the leader's
coordination policy).

## Who gets what

| Tool | Leader | Worker | Purpose |
| --- | :---: | :---: | --- |
| `send_message` | ✅ | ✅ | Message a teammate (or broadcast). The teammate channel. |
| `list_members` | ✅ | ✅ | The live roster: each member's name, role, status, and pending ⏰ alarms. |
| `alarm_set` | ✅ | ✅ | One-shot wake at an absolute time. A worker wakes itself; the **leader can target a teammate**. |
| `alarm_clear` | ✅ | ✅ | Cancel a pending one-shot alarm by id. |
| `task_create` | ✅ | — | Create a `pending` task (`title`, `spec`, `assignee`). |
| `task_assign` | ✅ | — | Dispatch a task (`pending → running`) and notify the assignee. |
| `task_update_status` | ✅ | — | Move a task (suspend/resume, send to `verifying`). |
| `task_verify` | ✅ | — | Approve (`verifying → completed`) or bounce back (`→ running`) with a note. |
| `task_list` | ✅ | — | The whole ledger. |
| `schedule_set` | ✅ | — | Put a member on a recurring cadence (cron/interval). |
| `schedule_clear` | ✅ | — | Remove a member's recurring schedule. |
| `proposal_list` | ✅ | — | Pending worker proposals awaiting a decision. |
| `proposal_accept` | ✅ | — | Accept a proposal — **atomically creates** the matching task. |
| `proposal_decline` | ✅ | — | Decline a proposal, with a note back to the proposer. |
| `skill_publish` | ✅ | — | Publish/update a space-shared skill (institutionalize a procedure). |
| `my_tasks` | — | ✅ | The worker's own assigned tasks. |
| `task_get` | — | ✅ | Read one task's full spec by id. |
| `task_propose` | — | ✅ | Propose trackable work for the leader to accept/decline. |
| `skill` | ✅ | ✅ | Invoke a skill by name (also auto-injected; see [../building/skills.md](../building/skills.md)). |

## Common tools (every member)

- **`send_message { to, body }`** — the *teammate* channel. `to` is a member's **name** (find it with
  `list_members`), or `"all"` to broadcast. Broadcasts reach every teammate but never the operator.
  A member replies to a teammate's message here — *not* with its output text (which only the operator
  sees).
- **`list_members`** — the roster snapshot. Use it to resolve the leader's actual member name (its
  *name* may not literally be "leader"), to see who's busy, and to spot pending alarms.
- **`alarm_set` / `alarm_clear`** — one-shot, absolute-time wakes (e.g. "re-check the run at
  2026-09-11 09:00"). Different from `schedule_set` (recurring). See
  [../building/scheduling-and-guardrails.md](../building/scheduling-and-guardrails.md).

## Leader-only tools

The leader is the **single writer** of the task ledger. Its loop:

```
task_create ──▶ task_assign ──▶ (worker works, reports via send_message) ──▶
   task_update_status{verifying} ──▶ task_verify{approve:true|false}
```

The state machine is enforced (illegal moves are rejected):

```
pending → running
running → suspended | verifying
suspended → running
verifying → completed | running
```

- **`task_create` / `task_assign`** — create then dispatch. Assigning notifies the worker.
- **`task_update_status`** — suspend/resume a task, or move it to `verifying` when a worker reports
  done.
- **`task_verify { approve }`** — `true` completes it; `false` (with a note) sends it back to `running`
  for rework.
- **`task_list`** — the leader's situational awareness.
- **`schedule_set` / `schedule_clear`** — give a member a recurring cadence at runtime (durable, but
  reset by `evva swarm .`).
- **`proposal_list` / `proposal_accept` / `proposal_decline`** — the bottom-up work inlet. Workers
  *propose*; the leader decides. `proposal_accept` atomically turns a proposal into a real task.
- **`skill_publish { name, description, body, overwrite? }`** — write/update a space-shared skill so a
  recurring procedure becomes permanent team knowledge instead of a forgotten message.

## Worker-only tools

A worker does **not** write the ledger. It:

- **`my_tasks`** — sees its assignments.
- **`task_get { task_id }`** — reads a task's full spec.
- **`task_propose { title, spec, suggested_assignee? }`** — files trackable work it discovered (a
  defect, a follow-up) without piercing the single-writer rule. It lands on the leader's proposal
  queue, the leader decides, and the worker hears back either way.

## Why these never go in `active.yml`

The permission boundary *is* the tool boundary: the runtime gives the leader the ledger-writers and
the worker the read-only views precisely because role determines authority. Listing a collaboration
tool yourself would at best duplicate the injection and at worst confuse the role model. Choose only
**domain** tools (see [catalog.md](catalog.md)); let the runtime handle coordination.

## See also

- The protocol the runtime injects alongside these tools:
  [../concepts/architecture.md](../concepts/architecture.md#authored-vs-auto-injected).
- The leader's coordination policy (what you *do* write): [../building/personas.md](../building/personas.md#the-leaders-persona-is-the-swarms-skeleton).
- Ledger discipline in practice: [../patterns/coordination.md](../patterns/coordination.md).
