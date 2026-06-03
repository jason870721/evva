# SPRD-1-12 — M4: inbox-drainer **public seam** on `pkg/agent` + swarm consumer (drain B)

> Milestone: M4 ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-6, 1-5
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 8) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §6.3, §9.2

## 1. Goal

The **only ticket that touches evva's runtime** — and it does so as a **public, additive
`pkg/agent` seam**, not a private hack (the explicit exception to global invariant #1).
Generalize the existing `KindDrainBackgroundTask`/`KindDrainMonitorEvents` mechanism
(`pkg/event/event.go:140`) into a pluggable **inbox-drainer**: at each loop iteration
boundary the agent calls the drainer and folds any returned synthetic message into the
run. The swarm supplies a drainer that reads its mailbox so a **busy** agent sees an
urgent message *within its current run* (drain B), instead of waiting for the run to end
(drain A, 1-6).

## 2. Scope

**In (evva runtime — the sanctioned exception to invariant #1):**
- A public seam on `pkg/agent` (e.g. `WithInboxDrainer(Drainer)`, where `Drainer` returns
  `(syntheticMessage string, ok bool)`), invoked by the loop at the iteration boundary,
  mirroring the existing background-task drain. **A nil drainer is a no-op** — single-agent
  behavior must not regress.
- Wire it into `internal/agent/loop.go` at the same boundary the existing drains fire.
- A `downstream_test.go`-style compile test proving the seam is usable from outside the module.
- `docs/extending.md` section + `CHANGELOG.md` entry + `pkg/version` bump (minor, additive).

**In (swarm consumer):**
- A `swarm` drainer that, each iteration, non-blockingly checks the agent's mailbox `chan`,
  `store.GetMessage`s any UUID, formats "Message from <sender>: <body>", marks it read, and
  returns it as the synthetic message. Wired via the supervisor (1-6).

**Out:** any change to drain A (it stays for idle agents); any other change to loop behavior
(the seam is strictly additive).

## 3. Dependencies & what this unblocks

- Depends on: 1-6 (the supervisor/mailbox bookkeeping the drainer reuses), 1-5 (the mailbox).
- Unblocks: the M4 drain-B gate; 1-13 (the DoD "busy agent gets urgent mail immediately" leg).

## 4. Technical design

`pkg/agent` (seam) + `internal/agent/loop.go` (call site) + `internal/swarm` (consumer).

```go
// pkg/agent — additive, Experimental.
type Drainer interface { Drain(ctx context.Context) (msg string, ok bool) }
func WithInboxDrainer(d Drainer) Option   // nil-safe: no drainer → no-op
```

- Place the call at the **same iteration boundary** as `KindDrainBackgroundTask`; fold the
  returned message as a synthetic user message before the next LLM turn.
- The swarm drainer is **non-blocking** (a `select` with `default`) so a busy agent with an
  empty mailbox pays ~nothing per iteration.
- Keep the seam minimal and provider-agnostic; document the contract (called once per
  boundary; return `ok=false` for "nothing to fold").

## 5. Acceptance criteria

1. With no drainer set, an agent's loop behaves **identically** to today (regression test
   over an existing loop test).
2. A drainer returning a message causes that text to appear as a synthetic user turn at the
   **next iteration boundary** of an in-flight run (not after it ends).
3. The swarm drainer: send an "urgent stop" message to a **busy** agent → it folds the
   message mid-run and reacts; the message is marked read.
4. The seam is public and compiles from a separate module (downstream compile test).
5. The drainer is called at most once per boundary and is non-blocking on an empty inbox.

## 6. Verification

- `pkg/agent` unit test: nil-drainer no-op (regression) + a fake drainer folded mid-run.
- A downstream compile test (mirror `pkg/agent/downstream_test.go`).
- swarm integration with a fake LLM: a busy agent receives mid-run and marks read.
- `docs/extending.md` + `CHANGELOG` + version bump present.

## 7. Definition of Done

- [ ] `WithInboxDrainer` public seam on `pkg/agent`; the loop folds at the iteration boundary.
- [ ] Nil-drainer no-op — single-agent behavior does not regress (test).
- [ ] swarm drainer (non-blocking mailbox check → synthetic message → mark read).
- [ ] Downstream compile test; `docs/extending.md` + `CHANGELOG` + version bump.
- [ ] This is the **only** sanctioned `pkg/agent` change (README invariant #1 exception).
