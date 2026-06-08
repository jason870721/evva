# SPRD-1-6 — Supervisor & Scheduler: wake sources, lifecycle, per-run recover

> Milestone: M0–M3 ｜ Status: IN REVIEW ｜ Owner: (unassigned) ｜ Depends on: 1-4, 1-5, 1-2
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 3) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §5, §5.1, §5.5, §6.3, §3.1

## 1. Goal

The **per-space engine** that turns wake events into agent runs and owns each member's
lifecycle. The scheduler watches three wake sources — **message, task, timer** (§5.5) —
and when an `active+idle` agent has work, builds a synthetic prompt and calls
`Controller.Run`. The supervisor owns membership (add/freeze) and run control
(suspend = cancel the run ctx). **Every run executes inside a `recover()`-guarded
goroutine** (invariant #3) so one agent's panic degrades that run only, never the process.

## 2. Scope

Delivered across milestones; keep each slice shippable.

**In (M0 min):**
- Per-agent run loop: select on `bus.Inbox(name)`; on a wake, if `active+idle`, set
  `busy`, build a synthetic prompt, `Controller.Run(ctx, prompt)` in a `recover()`
  goroutine; on return set `idle`. Update the roster run-status on every edge.

**In (M2 — drain A):**
- Message wake → **drain A**: pull UUID(s) from the inbox, `store.GetMessage`, format
  "Message from <sender>: <body>", run with that prompt, then `store.MarkRead` (§6.3).

**In (M3):**
- `AddMember(name)` (hot-load: `agentdef.Build` → `agent.New` → roster + `bus.Register`),
  `Freeze`/`Unfreeze` (frozen → never scheduled), `Suspend(name)` (cancel that run's
  ctx) / `Resume` (new Run), `HaltAll` (the Phase-2 kill switch).
- **timer wake**: a scheduler tick reads each member's parsed `Schedule` (1-3) and, at
  due time, wakes an idle agent with a synthetic "scheduled duty" prompt.
- task wake: when a task is assigned (Leader sets `running` via 1-7), wake the assignee.

**Out:** the tools that mutate tasks / send messages (1-7); restart reload of unread
(1-11 calls `bus.Requeue` + `ResumeSession`); the loop-internal drain B (1-12).

## 3. Dependencies & what this unblocks

- Depends on: 1-4 (`SwarmSpace`/`Roster`/Controllers), 1-5 (`bus.Inbox`), 1-2
  (tasks/messages DAO).
- Unblocks: 1-8 (service drives supervisor commands), 1-11 (restart), 1-12 (drain B
  reuses the same wake/idle bookkeeping).

## 4. Technical design

Package `internal/swarm` (`supervisor.go`, `scheduler.go`).

```go
type Supervisor struct {
    sp   *SwarmSpace
    bus  *bus.Bus
    mu   sync.Mutex
    runs map[string]context.CancelFunc // in-flight run ctx per agent (suspend)
}

func (s *Supervisor) Start(ctx context.Context)   // launch per-agent run loops + timer tick
func (s *Supervisor) AddMember(name string) error // agentdef.Build → agent.New → roster+bus (M3)
func (s *Supervisor) Freeze(name string) error     // membership=frozen (M3)
func (s *Supervisor) Unfreeze(name string) error
func (s *Supervisor) Suspend(name string) error    // cancel the run ctx (M3)
func (s *Supervisor) Resume(name string) error
func (s *Supervisor) HaltAll() error               // cancel all in-flight runs

// internal: the wake → run path, recover-guarded.
func (s *Supervisor) wake(name, reason, prompt string) // idle→busy→Run→idle
```

- **Wake sources** (§5.5): `message` (inbox chan), `task` (assignment hook from 1-7),
  `timer` (tick over `agentdef.Schedule.next()`). Any one, on an idle active agent,
  triggers a Run. **Idle burns no tokens** — no Run, no LLM call.
- **Per-run recover** (§3.1, invariant #3): `go func(){ defer recoverAndLog(); Run() }()`.
- **Suspend/run race** (§5.6 risk): a per-agent state lock guards the
  idle↔busy↔suspended transition so suspending an agent mid-wake cancels cleanly.
- **standing-duty vs task-driven** (§5.5): timer/message agents need no task row; the
  task state machine is per-agent-optional.

## 5. Acceptance criteria

1. An idle agent that receives a message wakes, runs once with a prompt containing the
   sender + body, and the message's `read_at` is set (drain A).
2. `to == "all"` wakes every active member; frozen members are not scheduled.
3. A timer-scheduled agent is woken at each due tick and **does not run when idle with
   no due tick** (assert via run count / usage — the idle-no-token guarantee).
4. `Suspend` cancels an in-flight run within the ctx deadline; `Resume` starts a new run;
   `HaltAll` cancels all in-flight runs.
5. A panicking agent run is contained by `recover()` — the process and the other agents'
   loops survive (assert siblings still wake after the panic).
6. `AddMember` makes a new agent addressable (roster + bus inbox) without a restart.

## 6. Verification

- Unit/integration with a **scripted fake `llm.Client`** (no real API): assert
  wake→run→idle edges, drain-A mark-read, timer cadence, suspend cancellation, and
  recover-containment (inject a panicking fake, assert siblings live).
- A "no wake → no run" idle test proving idle doesn't burn tokens.
- `go test -race ./internal/swarm/...` clean (suspend/wake race coverage).

## 7. Definition of Done

- [x] Per-agent recover-guarded run loop; idle/busy/suspended bookkeeping on the roster.
- [x] Three wake sources (message/task/timer); idle burns no tokens.
- [x] Drain A (UUID → `GetMessage` → inject → `MarkRead`) on message wake.
- [x] Add/Freeze/Unfreeze/Suspend/Resume/HaltAll; suspend cancels the run ctx.
- [x] Panic containment proven; `-race` clean; no `internal/agent` import (invariants #1, #3).

### Implementation design / decisions

- **Three wake sources, two mechanical channels.** §5.5's `{message, task,
  timer}` collapse to **inbox + timer poke**: `task_assign` (1-7) wakes the
  assignee by *sending it a message* (§7.1 "pending→running: 發 message 推給該
  Worker"), so the task source rides the same mailbox as ordinary messages. The
  run loop selects on `bus.Inbox(name)` (message/task) and a per-agent
  `wake chan` (timer ticks + resume pokes). No separate task hook is needed in
  the supervisor.
- **Drain A reads `store.UnreadFor`, not the chan.** A mailbox UUID is only the
  "you have mail" hint; `composePrompt` builds the prompt from the member's
  unread set (DB-truth, §6.2), which naturally absorbs dropped hints (full
  buffer) and stragglers. Messages are marked read **only after a clean,
  unsuspended run** — a panic/suspend/error leaves them unread for retry on
  Resume or restart (at-least-once, never lose a teammate's message).
- **Suspension is a `serve()` gate, not a park-select.** A suspended (or frozen)
  agent's wake is a cheap no-op (no `Run`, no token); no second select state to
  reason about. Resume flips the gate and pokes the loop to re-process its unread
  work — which is exactly the still-unread message the cancelled run left behind,
  so "Resume starts a new run" falls out for free.
- **Suspend/run race closed under `memberRun.mu`.** The roster status flips
  (`RunBusy` / `RunIdle` / `RunSuspended`) inside the per-member lock, and
  `serve` re-checks `suspended` after acquiring it, so a `Suspend` landing
  mid-claim can't be clobbered by a late `RunBusy`. Lock order is always
  `memberRun.mu → roster.mu`; the supervisor's `mu` (members map + schedule/
  nextDue) never nests with `memberRun.mu`.
- **Recover guard wraps the synchronous `Run`** (`safeRun` with a deferred
  `recover`) rather than spawning a child goroutine — it contains the panic
  *and* serialises one run at a time per agent. The loop goroutine survives its
  own agent's panic (proven by a second message running after the first panics)
  and siblings are untouched (invariant #3).
- **Timer.** One tick goroutine per space (default 1s; tests 5ms) advances each
  scheduled member's `nextDue` via `agentdef.Schedule.Next` and pokes the due,
  active members. Standing-duty agents need no task row (§5.5), so the prompt is
  a generic "scheduled duty" synthetic.
- **`AddMember` reuses one construction path.** `NewSpace`'s per-member body was
  extracted into `SwarmSpace.constructMember` (+ `registerDef`); the space now
  retains the persona registry / cfg / toolset / loader so a hot-load is
  `loader.Build → registerDef → constructMember → startMemberLoop`, no restart.
  The `Bus` is now a `SwarmSpace` field (constructed in `NewSpace`, a mailbox
  registered per member) so both the supervisor and 1-7's `send_message` reach
  it via `sp`. *Known limitation:* concurrent `AddMember` during an active
  subagent-spawn resolution isn't synchronised at the `pkg/agent` registry level
  — fine for v1's user-triggered hot-load.
- **Out of scope (deferred):** the `task_*`/`send_message` tools that drive the
  bus + ledger (1-7); restart-reload of unread via `bus.Requeue` + `ResumeSession`
  (1-11); the mid-run drain B seam (1-12). This ticket ships the engine + wiring
  + tests.
