# SPRD-1-11 — Restart-resume: unread reload + `ResumeSession` + boot reconcile

> Milestone: M3 ｜ Status: IN REVIEW ｜ Owner: veronica ｜ Depends on: 1-2, 1-5, 1-6, 1-4
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 1+3) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §6.2, §4.3, §5

## 1. Goal

Make a swarm **survive a process death**. After `kill -9` + restart, each space is
rebuilt from disk, every agent's **unread messages are re-queued**, each agent's evva
session is restored via `Agent.ResumeSession`, and the persistent task ledger means
in-flight work continues — no message dropped, no task lost.

## 2. Scope

**In:**
- **Boot reconcile**: on `service start`, rebuild each previously-registered space (from
  a persisted registry of workdirs) — re-run 1-3 `BuildAll` + 1-4 `NewSpace`.
- **Unread reload**: per agent, `store.UnreadFor(name)` → `bus.Requeue(name, uuids)` so
  the scheduler (1-6) drains them on its first cycle (§6.2).
- **Session resume**: load each agent's `.vero/sessions/<name>` snapshot and call
  `Agent.ResumeSession` so the rebuilt agent continues its prior transcript (§4.3).
- **Session persistence**: ensure each agent writes its snapshot to `.vero/sessions/`
  (wire the SDK session-store path to the per-space dir).
- `runtime.json` (§4.3): persist membership (active/frozen) so frozen members come back
  frozen, not active.

**Out:** drain B (1-12); model-C cross-process resilience (out of Phase 1).

## 3. Dependencies & what this unblocks

- Depends on: 1-2 (`UnreadFor`, durable tasks), 1-5 (`Requeue`), 1-6 (the scheduler that
  drains the requeued mail), 1-4 (`NewSpace` rebuild).
- Unblocks: 1-13 (the DoD restart-resume leg).

## 4. Technical design

Package `internal/swarm` (a `resume.go` + hooks in `service.go`/`supervisor.go`).

- **Registry persistence**: the service writes the set of registered workdirs (e.g.
  `~/.evva/service/spaces.json`); on start it reconstructs each.
- **Per-space resume sequence**: `NewSpace` (agents idle) → for each agent: load session
  snapshot → `ResumeSession` → `bus.Requeue(UnreadFor(name))` → start the supervisor.
  **Order matters**: requeue *after* the inbox exists, *before* the run loop starts.
- **Tasks** need no special handling — they live in `vero.db` (1-2), so a rebuilt space
  sees the same ledger; a task left `running` is simply still `running` (the Leader
  decides to resume or suspend it).
- Idempotent + crash-safe: a second restart over the same on-disk state reproduces the
  same live state.

## 5. Acceptance criteria

1. Send an unread message to an idle agent, `kill -9` the service, restart: the agent
   receives that message on its first post-restart cycle (re-queued, not lost).
2. An agent mid-conversation before the kill resumes its transcript via `ResumeSession`
   (asserted by a follow-up turn that depends on prior context).
3. A `running` task persists across restart (still `running`, same row).
4. A `frozen` member comes back `frozen` (from `runtime.json`), not active.
5. Two registered spaces both come back, isolated, after restart.

## 6. Verification

- Integration with a fake LLM provider: populate a space (messages + a task + a frozen
  member), simulate restart (tear down + rebuild from disk), assert reload/resume/state.
- A "no message lost across restart" test is the centerpiece.
- `-race` clean.

## 7. Definition of Done

- [x] Boot reconcile rebuilds every registered space from disk.
- [x] Unread reload (`UnreadFor` → `Requeue`) — no message dropped.
- [x] `Agent.ResumeSession`; tasks persist via the ledger.
- [x] Membership (frozen) restored from `runtime.json`; idempotent.
- [x] Multi-space restart isolation; tests green; no `internal/agent` import (invariant #1).

### Implementation notes

- **`internal/swarm/resume.go` — `SwarmSpace.Reload()`** stitches the three
  durable stores back together on a rebuild, in §6.2 order (call after
  `NewSpace`, before the Supervisor starts): (1) resume each member's latest
  real persona session via `Agent.ListSessions`/`ResumeSession`; (2)
  `store.UnreadFor(name)` → `bus.Requeue` so the scheduler drains it on its
  first cycle; (3) reapply frozen membership from `runtime.json`. Idempotent —
  the DB is truth, so a duplicate mailbox hint is harmless (`MarkRead` is
  idempotent). Tasks need no handling: they live in `vero.db`.
- **Session-store deviation:** the SDK persists transcripts at
  `<AppHome>/sessions/<workdir-slug>/`, keyed by persona — there is **no public
  option to relocate the session dir to `.vero/sessions/`**, and adding one
  would mean touching `pkg/agent` (forbidden outside 1-12). So sessions stay in
  the SDK's standard path; members are disambiguated by `Profile == member
  name`. Faithful to invariant #1.
- **`runtime.json`** (`<workdir>/.vero/runtime.json`) carries only membership
  (the one thing neither the ledger nor the session store holds); written by
  the Supervisor on `Freeze`/`Unfreeze`/`AddMember`.
- **Service**: `spaces.json` (under the state dir, `SetStateDir`) records the
  registered workdirs; `register()` persists + calls `sp.Reload()`; `Reconcile()`
  rebuilds each on boot (`cmd/evva` calls `SetStateDir`+`Reconcile` in the
  daemon child). **Key fix:** graceful `Stop()` now tears spaces down *without*
  rewriting `spaces.json` (preserving the reconcile set), while `StopSpace`
  (explicit, per-space) *does* drop it — otherwise a clean shutdown would erase
  the set it's meant to restore.
- Tests (`-race` clean): `resume_test.go` (the centerpiece — transcript resume +
  unread reload + running-task persist + frozen-membership across a tear-down/
  rebuild; fresh-space no-op; idempotency) and `service/reconcile_test.go` (two
  on-disk spaces rebuilt + isolated after restart; no-state no-op). Daemon
  restart verified manually (clean reconcile, no-op on empty set).
