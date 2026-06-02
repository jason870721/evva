# Veronica — Phase 1 Sub-Tickets (SPRD)

These are the **dispatchable, session-sized sub-PRDs** that decompose
[`../prd-phase1-swarm.md`](../prd-phase1-swarm.md). Each `SPRD-1-N` is scoped
so one agent can take it in one (or a few) focused session(s). **Phase 1 is
built carefully — correctness, isolation, and tests over speed.**

> - Design of record: [`../veronica-design-v1.md`](../veronica-design-v1.md)
> - Parent PRD: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md)
> - Roadmap (milestone gates + Phase-1 DoD): [`../roadmap.md`](../roadmap.md)

---

## Ticket index

| ID | Title | Milestone | Depends on |
| --- | --- | --- | --- |
| [SPRD-1-1](SPRD-1-1-skeleton-and-depcheck.md) | Module skeleton, package layout, dep-check, CI | M0 | — |
| [SPRD-1-2](SPRD-1-2-store-persistence-layer.md) | `.vero` store: schema, RWMutex DAO, task state machine, message DAO | M1/M2 | 1-1 |
| [SPRD-1-3](SPRD-1-3-agentdef-loader.md) | Agentdef loader: `agents/{main,sub}/` → AgentDefinition + skills + schedule | M0/M3 | 1-1 |
| [SPRD-1-4](SPRD-1-4-swarmspace-and-roster.md) | SwarmSpace assembly + per-space Roster + event-sink wiring | M0 | 1-1, 1-3 |
| [SPRD-1-5](SPRD-1-5-message-bus.md) | Message bus & mailboxes (per-space, chan-of-uuid, broadcast) | M2 | 1-2 |
| [SPRD-1-6](SPRD-1-6-supervisor-scheduler.md) | Supervisor & Scheduler: wake sources, lifecycle, per-run recover | M0–M3 | 1-4, 1-5, 1-2 |
| [SPRD-1-7](SPRD-1-7-swarm-tools.md) | Swarm custom tools: `task_*`, `send_message`, `list_members` | M1/M2 | 1-2, 1-5, 1-4 |
| [SPRD-1-8](SPRD-1-8-service-and-webapi.md) | Service (multi-space host) + webapi (HTTP/WS, REST, event fan-out) | M0–M3 | 1-4, 1-6, 1-7 |
| [SPRD-1-9](SPRD-1-9-cmd-subcommands.md) | `cmd/evva` subcommands: `service` (daemon+pidfile), `swarm ./ls/stop/add` | M0/M3 | 1-8 |
| [SPRD-1-10](SPRD-1-10-vue-spa.md) | vue.js SPA: space picker, Team Board, Roster, Leader Chat, overlays | M1–M3 | 1-8 |
| [SPRD-1-11](SPRD-1-11-restart-resume.md) | Restart-resume: unread reload + `ResumeSession` + boot reconcile | M3 | 1-2, 1-5, 1-6, 1-4 |
| [SPRD-1-12](SPRD-1-12-inbox-drainer-seam.md) | M4: inbox-drainer **public seam** on `pkg/agent` + swarm consumer (drain B) | M4 | 1-6, 1-5 |
| [SPRD-1-13](SPRD-1-13-integration-and-dod.md) | Phase-1 integration + DoD e2e (multi-space isolation, full loop, restart) | DoD | all |

---

## Dependency DAG

```
1-1 ─┬─> 1-2 ─┬───────────────────────────> 1-7 ─┐
     │        └─> 1-5 ─┬──────────────────> 1-6 ──┼─> 1-8 ─> 1-9
     ├─> 1-3 ─> 1-4 ───┴──────────────────> 1-6   │     └─> 1-10
     │                  (1-4 also feeds) ─────────┘
     └ (1-2,1-5,1-6,1-4) ─> 1-11
       (1-6,1-5,pkg/agent) ─> 1-12
       (everything) ─> 1-13
```

Build order that keeps each ticket unblocked: **1-1 → {1-2, 1-3} → {1-4, 1-5}
→ {1-6, 1-7} → 1-8 → {1-9, 1-10, 1-11} → 1-12 → 1-13**. Items in `{}` can run
in parallel across agents/sessions.

## Milestone → tickets

- **M0 (walking skeleton, multi-space host):** 1-1, 1-3(min), 1-4, 1-6(min), 1-8(min), 1-9(min) → gate: 2 isolated spaces visible in Web.
- **M1 (task ledger + roster):** 1-2(tasks), 1-7(task_*/list_members), 1-10(board+roster).
- **M2 (messaging, drain A):** 1-2(messages), 1-5, 1-6(message-wake + drain A), 1-7(send_message).
- **M3 (manifest + timer + dynamic + suspend + restart):** 1-3(schedule), 1-6(timer/freeze/add/suspend), 1-11, 1-9(add).
- **M4 (drain B):** 1-12.
- **DoD gate:** 1-13.

---

## Global invariants — EVERY ticket MUST honor these

1. **pkg-only for agent concerns.** `internal/swarm/**` must NOT import
   `internal/agent` (or any evva `internal/`); use `pkg/*` only. Enforced by
   the dep-check from SPRD-1-1. The one exception is SPRD-1-12, which adds a
   *public* seam to `pkg/agent`.
2. **Per-space isolation.** No `*sql.DB` / bus / roster is shared across
   `SwarmSpace`s. Agent names are **per-space scoped** (two spaces may both
   have `leader`). Tasks/messages never cross a space boundary.
3. **Per-run panic recovery.** Every `Controller.Run` runs inside a
   `recover()`-guarded goroutine; one agent panic degrades that run only,
   never the process.
4. **Leader-only task-status writes.** Workers are read-only on tasks and
   report via `send_message`. `messages` is DB-truth; the mailbox `chan`
   carries only the message UUID; drain marks `read_at` and labels `sender`.
5. **Tests ship with the code.** No "add tests later." Each ticket lists the
   unit/integration tests it must add.
6. **Security baseline.** Service binds `127.0.0.1` + a session token;
   order/write-class tools are gated by `pkg/permission`.

## Ticket template (every SPRD follows it)

```
# SPRD-1-N — <Title>
> Milestone(s) | Status: TODO | Owner: (unassigned) | Depends on: ...
## 1. Goal
## 2. Scope (In / Out)
## 3. Dependencies & what this unblocks
## 4. Technical design (package, key types/interfaces, file layout)
## 5. Acceptance criteria (numbered, testable)
## 6. Verification (unit tests to write + how to prove it)
## 7. Definition of Done (checklist)
```

## Status workflow

`TODO → IN PROGRESS → IN REVIEW → DONE` — update the `Status:` line in the
ticket header as work proceeds. A ticket is DONE only when its §7 checklist is
fully green and the global invariants hold.
