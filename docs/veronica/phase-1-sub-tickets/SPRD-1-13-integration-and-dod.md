# SPRD-1-13 — Phase-1 integration + DoD e2e (multi-space isolation, full loop, restart)

> Milestone: DoD ｜ Status: IN REVIEW ｜ Owner: veronica ｜ Depends on: all (1-1 … 1-12)
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (§3 A1–A11, §7) ｜ Roadmap: [`../roadmap.md`](../roadmap.md) §5 DoD

## 1. Goal

The **gate ticket**: prove Phase 1 is done by turning the roadmap §5 DoD and the PRD
A1–A11 into green, automated (where feasible) checks. One end-to-end test exercises the
full loop — start → assign → collaborate → restart → resume — plus the multi-space
isolation guarantee, the dep-check, and the security baseline. Nothing new is built here;
this ticket integrates and certifies.

## 2. Scope

**In:**
- **E2e (the centerpiece, A10)**: from an `evva-swarm.yml` with **≥3 agents**, via a fake
  LLM provider: leader `task_create`→`assign`→worker runs→`send_message` report→leader
  `verifying`→`approve`; then `kill`/rebuild → unread reload + resume → the run continues.
  Assert the 5-state transitions, the message round-trip + mark-read, and post-restart continuity.
- **Multi-space isolation (A2b)**: two spaces, **same agent names**, no cross-talk; stop
  one, the other survives.
- **dep-check (A9)**: CI asserts `go list -deps ./internal/swarm/...` has no
  `internal/agent` (modulo the 1-12 `pkg/agent` seam, which is public).
- **Security baseline (A11)**: the service binds `127.0.0.1` + token; a write-class tool
  routes through `pkg/permission`.
- **Idle-cost (A5)**: a log/usage assertion that a scheduled-but-idle agent burns no tokens.
- A **DoD checklist doc** mapping each roadmap §5 box → the test/command that proves it.

**Out:** anything unimplemented in 1-1…1-12 — this ticket does **not** paper over gaps. A
failing leg means the owning ticket is reopened (roadmap §1 "no patching in a later phase").

## 3. Dependencies & what this unblocks

- Depends on: **all** prior tickets (1-1 … 1-12).
- Unblocks: the Phase-1 → Phase-2 gate (roadmap §1: Phase 2 does not open until this is green).

## 4. Technical design

- A top-level `internal/swarm/e2e_test.go` (or `internal/swarm/integration/`) that boots a
  `Service` on an ephemeral port, registers fixture spaces (a `testdata/` manifest: a leader
  + 2 workers, one scheduled), and drives the loop via the public webapi + Controller seams
  with a **deterministic fake LLM**.
- Reuse the per-ticket fakes (fake `llm.Client`, a temp-dir store) so the e2e is hermetic
  and CI-fast — **no real API calls, no network beyond loopback**.
- The DoD checklist lives in `docs/veronica/` (or this directory) and is filled in as each
  box goes green.

## 5. Acceptance criteria

1. The full-loop e2e passes: assign → collaborate → verify → restart → resume, with the
   5-state transitions and mark-read asserted.
2. Two same-named spaces run isolated; stopping one doesn't affect the other.
3. dep-check is green in CI and fails on a deliberately-added `internal/agent` import.
4. The token gate + a write-tool permission gate are asserted.
5. The idle-no-token assertion holds for a scheduled agent.
6. Every roadmap §5 DoD box maps to a passing check (the checklist is fully ticked).

## 6. Verification

- `go test ./internal/swarm/...` (incl. the e2e) green; `-race` on the e2e.
- CI runs build + test + `npm run build` + dep-check (the 1-1 pipeline, now exercising the
  whole system).
- The DoD checklist doc is committed with each box linked to its proof.

## 7. Definition of Done

- [x] Full-loop e2e (start→assign→collaborate→restart→resume) green and hermetic.
- [x] Multi-space isolation, dep-check, token + permission, idle-cost all asserted.
- [x] Roadmap §5 DoD checklist fully ticked, each box → a proof.
- [x] A1–A11 (PRD §3) all green; Phase-1 ready to gate into Phase 2.

### Implementation notes

- **`internal/swarm/service/e2e_test.go`** — hermetic, deterministic, loopback
  only. A **transcript-driven `scriptedClient`** (registered as provider
  `e2e_stub`) decides each member's next tool call purely from what its
  conversation shows — so role falls out of visibility (only a worker's
  transcript carries an assignment; only the leader's carries the KICKOFF /
  the worker's report), and the supervisor + bus + drains do all the
  orchestration. Three tests:
  - `TestE2E_FullLoop` — kick the leader → the loop self-drives
    `task_create`→`assign`→ worker `send_message` report → `task_update_status`
    verifying → `task_verify` approve → **completed**; asserts the message
    round-trip both ways with `ReadAt` set, and that idle `worker-b` never ran
    (empty transcript = no tokens).
  - `TestE2E_RestartContinuity` — kick, tear the host down mid-flight, new
    `Service` + `Reconcile`, and the reloaded swarm drives the task to
    completion with no new input.
  - `TestE2E_MultiSpaceIsolation` — two same-named spaces; drive + stop A, then
    B still completes its own independent loop.
- The 5-state path is proven by *reaching* `completed` (the store enforces the
  legal transitions) + the exhaustive `store` unit matrix; the deterministic
  restart guarantees live in `swarm.TestRestartResume`; this ticket integrates
  them.
- Poll timeouts are generous (25–30s) so the `-race` build (≈10× slower) never
  flakes — polls return the instant the ledger converges, so it costs nothing on
  the happy path.
- **DoD checklist**: [`../phase-1-dod-checklist.md`](../phase-1-dod-checklist.md)
  maps every roadmap §5 box and PRD A1–A11 to its proving test/command.

**Phase 1 DoD is GREEN — Phase 2 (trader-team) may open.**
