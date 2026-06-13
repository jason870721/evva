# Overview: what an evva swarm is

An **evva swarm** (codename *Veronica*) is a team of long-lived AI agents that collaborate on a
shared goal inside one directory. It is evva's multi-agent subsystem: where a single evva session is
one agent in your terminal, a swarm is several agents running together under a small service, talking
to each other and to you through a web console.

## The shape of a swarm

```
         ┌─────────────────────────────────────────────┐
         │                 swarm "space"                │
         │                                              │
   you ──┼──▶  LEADER  ──── task ledger ────▶  WORKER A  │
 (operator)    (plans,    (shared board)        (does    │
         │      delegates,  ◀── messages ──▶     work)    │
         │      verifies)                       WORKER B  │
         │                                      WORKER …  │
         └─────────────────────────────────────────────┘
```

- **A space** is one isolated swarm: a leader plus its workers, a private message bus, and a
  per-space ledger (a SQLite database under `.vero/`). You can run several spaces at once.
- **The leader** owns the plan. It breaks the goal into tasks, assigns each to the right worker,
  and verifies the results before reporting back to you. The leader does **not** do the workers'
  work itself.
- **The workers** carry out assigned tasks and report back. Each has its own specialty.
- **The operator** is you — the human. You talk to members through a web console; you do not see the
  internal messages teammates send each other unless you inspect them.

## Two ways members talk

This distinction is load-bearing — getting it wrong means messages are silently lost:

| Channel | Goes to | Used for |
| --- | --- | --- |
| A member's **output text** (what it "says") | The **operator** (you, in the web console) | Answering your instructions, status updates |
| The **`send_message` tool** | **Teammates** (other members) | Hand-offs, questions, "task done" reports |

A worker that finishes a task must `send_message` the leader — printing "done" as output text would
talk to *you*, not the leader, and the work would stall. The runtime teaches every member this rule
automatically; you do not write it into personas.

## The task ledger

The team's single source of truth for "what work exists and where it stands." Every task moves
through a small state machine:

```
pending ──assign──▶ running ──▶ verifying ──▶ completed
                       ▲            │
                       └── rework ──┘   (and running ⇄ suspended)
```

Only the **leader** may write task status. Workers read their assignments (`my_tasks`) and, when they
spot work worth tracking, *propose* it (`task_propose`) for the leader to accept or decline. This
single-writer rule keeps the board coherent.

## Both leader and workers are root agents

In evva's single-agent mode, a "subagent" is a short-lived child spawned by a parent. **A swarm is
different**: both the leader and the workers are independent, long-lived root agents. The
`main`/`sub` split (and the `leader`/`worker` role) is a *coordination role* — who owns the ledger —
not a parent/child spawn relationship. Every member can run for weeks, wake on a schedule, and keep
its own long-term memory.

## What a swarm is good at

- **Decomposable goals** with distinct specialties: "take a GitHub issue → ship a PR" (pm, designer,
  backend, frontend, qa), "review this diff" (correctness, security, quality reviewers + a verifier).
- **Pipelines** where each stage feeds the next and the leader gates progress.
- **Standing duties** that run on a cadence: a watchdog that scans every 30 minutes, a trader that
  wakes on market hours.
- **Parallel fan-out** where several members work independently and the leader merges results.

## What a swarm is *not*

- It is not a way to make one task faster by splitting it arbitrarily. Coordination has overhead;
  use a swarm when the work has genuinely distinct roles or runs over a long horizon.
- It is not a drop-in for a single `agent` subagent call. For a one-shot "go research X and report
  back," a single agent with the `agent` tool is simpler. A swarm earns its keep with persistence,
  specialization, and operator-in-the-loop coordination.

## Where to go next

- The pieces you actually author, and the large amount the runtime injects for free:
  [architecture.md](architecture.md).
- Build your first one in a few minutes: [../building/quickstart.md](../building/quickstart.md).
- Unfamiliar term? [glossary.md](glossary.md).
