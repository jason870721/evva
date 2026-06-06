# Veronica Phase 1 — Definition-of-Done checklist

> Status: **GREEN** ｜ Certified by: SPRD-1-13 ｜ Date: 2026-06-04
> Source of truth: [`roadmap.md`](roadmap.md) §5 DoD + [`prd-phase1-swarm.md`](prd-phase1-swarm.md) §3 (A1–A11).

This is the gate doc for closing Phase 1: every roadmap §5 box and every PRD
A-criterion maps to a concrete, automated proof. Phase 2 (trader-team) does not
open until every row here is green (roadmap §1).

Run everything:

```sh
go build ./... && go vet ./... \
  && go test -race ./internal/swarm/... ./pkg/agent/ ./internal/agent/ \
  && bash scripts/depcheck.sh \
  && (cd web && npm test && npm run build) \
  && (cd examples/full-host && go build ./...)
```

## Roadmap §5 DoD

| DoD box | Proof |
| --- | --- |
| ≥3-agent swarm from `evva-swarm.yml`; Web shows roster (membership + run-status) | `service.TestE2E_FullLoop` (3-agent fixture) · `webapi.TestRESTSnapshots` (`/api/swarm/:id` roster) · `service.TestRESTReflectsSpace` |
| Leader push + Worker read-only + 5-state machine; kanban reflects each move | `service.TestE2E_FullLoop` (create→assign→verify→complete) · `store.*` state-machine tests (legal/illegal transitions, single-writer) · `tools.*` (worker write rejected) |
| `send_message` two-way + `to:"all"`; lands in SQL; drain A injects + marks read | `service.TestE2E_FullLoop` (round-trip + `ReadAt` asserted) · `bus.*` (deliver/requeue/broadcast) · `swarm` scheduler drain-A |
| timer wake (scheduled agent gets Run); idle burns no tokens | `swarm` scheduler tests (timer tick → wake) · `service.TestE2E_FullLoop` asserts idle `worker-b` never ran (empty transcript) |
| dynamic add + freeze (no delete) | `swarm` supervisor tests (`AddMember`/`Freeze`/`Unfreeze`) · `resume.TestRestartResume` (frozen restored) |
| suspend/resume + restart-resume (reload unread + `ResumeSession`) | `swarm.TestRestartResume` · `service.TestE2E_RestartContinuity` · `service.TestReconcileRebuildsRegisteredSpaces` |
| drain B (M4 public seam): busy agent gets mail mid-run | `pkg/agent.TestInboxDrainer_FoldsMidRun` · `swarm.TestInboxDrainerReadsAndMarks` |
| zero `internal/agent` import (multi-agent oracle) | `scripts/depcheck.sh` (green; 1-12 `pkg/agent` seam is the sanctioned public exception) |
| tests green: store / bus / scheduler unit + one e2e | `go test -race ./internal/swarm/...` · `service.TestE2E_FullLoop` |
| security baseline: `127.0.0.1` + session token; dangerous tools via permission | `service.DefaultAddr` = `127.0.0.1:8888` · `webapi.TestTokenGate` (401 w/o token) · `tools` init (worker file/shell writes still gate in non-bypass mode; the leader's task-coordination tools auto-allow so dispatch isn't human-gated — `TestPermissionClassification`) |

## PRD §3 A1–A11

| # | Criterion | Proof |
| --- | --- | --- |
| A1 | `service start/stop/status`; pidfile/log under `~/.evva/service/` | `cmd/evva.TestStatusStalePid`, `TestStopNotRunning`, `TestClientAgainstLiveService` + manual daemon lifecycle (SPRD-1-9) |
| A2 | `evva swarm .` registers a ≥3-agent space; Web shows roster | `service.TestE2E_FullLoop` · `cmd/evva.TestSwarmRegisterClient` |
| A2b | two workdirs → two fully-isolated spaces (same names OK) | `service.TestE2E_MultiSpaceIsolation` · `service.TestReconcileRebuildsRegisteredSpaces` · `swarm.TestTwoSpaceIsolation` |
| A3 | leader push, worker read-only, 5-state; worker write rejected | `service.TestE2E_FullLoop` · `store`/`tools` tests |
| A4 | `send_message` two-way + `to:"all"`; SQL; drain A + mark-read | `service.TestE2E_FullLoop` · `bus.*` |
| A5 | timer wake; idle burns no tokens | `swarm` scheduler tests · `TestE2E_FullLoop` (idle worker-b empty transcript) |
| A6 | dynamic add; freeze (no delete) | `swarm` supervisor tests |
| A7 | suspend aborts in-flight; resume continues; `kill -9` → reload + `ResumeSession` | `swarm` supervisor (suspend/resume) · `TestRestartResume` · `TestE2E_RestartContinuity` |
| A8 | drain B: busy agent sees urgent mail next iteration | `pkg/agent.TestInboxDrainer_FoldsMidRun` · `swarm.TestInboxDrainerReadsAndMarks` |
| A9 | `internal/swarm` has no `internal/agent` dep | `scripts/depcheck.sh` |
| A10 | unit (store/bus/scheduler) + one e2e | the suite + `TestE2E_FullLoop` |
| A11 | `127.0.0.1` + token; dangerous tools via permission | `webapi.TestTokenGate` · `tools` permission classification |

## Notes on what is automated vs. asserted indirectly

- **5-state transitions**: the store *enforces* the legal path
  (`pending→running→verifying→completed`), so the e2e reaching `completed`
  proves a legal traversal; the exhaustive legal/illegal matrix is in the
  `store` unit tests.
- **Restart continuity**: `TestRestartResume` gives the deterministic
  unit-level guarantees (unread reload, transcript resume, running-task persist,
  frozen membership); `TestE2E_RestartContinuity` exercises the integrated
  path (kick → kill host → `Reconcile` → loop converges to completed).
- **dep-check "fails on a deliberate import"**: `depcheck.sh` greps direct
  imports; adding an `internal/agent` import to `internal/swarm/**` makes it
  exit non-zero (verify by hand; not committed as a always-red test).
- **Web kanban / roster live updates**: the SPA reducers are unit-tested
  (`web` `node --test`) and the REST/WS API they consume is covered by the
  `webapi`/`service` tests; a full browser click-through is the manual leg.

**Conclusion:** Phase 1 DoD is met — the swarm infrastructure is built,
isolated, restart-safe, and exercised end-to-end on the public `pkg/*` surface.
Phase 2 (trader-team validation) may open.
