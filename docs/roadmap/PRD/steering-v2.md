# PRD — Steering v2 (interrupt-grade mid-turn control) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W8 / v1.18** per
> [../long-range.md](../long-range.md).
> **Roadmap source:** 2026-07-06 long-range planning pass. "Steer while it
> works" became the defining interactive-agent UX of 2025-26 (Claude Code's
> mid-turn message queueing + Esc interrupt). evva already has the *passive*
> half — an iteration-boundary drain — but nothing that reaches into a
> running iteration.
> **Reference source:** `ref/src` steering/interrupt surfaces — port the
> semantics; the signal plumbing is evva-native and partially shipped.

---

## 1. TL;DR

evva's signal architecture (signal pump + durable queues + the
iteration-boundary drain in the run loop — `internal/agent/signal.go` et
al., audit to pin) means a user message typed mid-run is *not lost*: it
folds in when the current iteration ends. What's missing is everything
*stronger* than that:

- If the model is mid-way through a wrong 4-minute `bash` run, "stop that,
  tests are in ./scripts" waits for the bash to finish.
- If the model is streaming a long wrong answer, the correction waits for
  the whole stream plus any tool calls it schedules.
- The TUI has no vocabulary for "queue this politely" vs "interject now"
  vs "abort the turn".

Steering v2 adds three escalation levels, keyed off how the user sends:

| Level | Trigger | Semantics |
|---|---|---|
| **Queue** (exists) | plain Enter while running | folds at next iteration boundary — today's behavior, now labeled in the UI |
| **Interject** | send-as-interject keybind | cancel the in-flight LLM stream *or* the running tool (kill-tree), synthesize an honest interrupted-result, fold the user message in immediately, continue the same turn |
| **Abort** | double-Esc (existing abort, unchanged) | stop the turn entirely |

The hard part is transcript coherence: a cancelled tool call must still
produce a paired result block (synthesized `[interrupted by user]`), and a
cancelled stream must be truncated at a valid block boundary — provider
histories reject dangling tool_use. That pairing discipline is the core of
this wave.

## 2. Goals / non-goals

### Goals

- Interject path: cancel in-flight LLM request (context cancellation
  through `llm.Client` — verify all five providers abort cleanly) or
  in-flight tool (existing kill-tree machinery), synthesize paired results,
  inject the user message, resume the loop without losing the turn.
- UI vocabulary: pending-queue indicator (n queued, visible while the agent
  works), distinct keybinds for queue vs interject, and a composer hint
  showing which mode Enter will use.
- Queued-message editing: view and revoke queued messages before they fold
  in (they're durable rows today — surface them).
- Headless parity: the signal API accepts the same three levels so swarm
  leaders / API callers can interject workers (leader "stop, re-plan"
  mail becomes actionable mid-task instead of at the next wake).
- Every interrupt leaves an explicit system-note in history — the model
  always knows it was interrupted and why.

### Non-goals (this wave)

- Speculative continuation (running ahead while the user types).
- Mid-*token* stream splicing — interject truncates at block boundaries.
- Undoing side effects of the interrupted tool (checkpoint/rewind already
  covers file effects; a killed bash keeps its partial world).
- Voice/hotkey global listeners (EX-7 territory).

## 3. Design sketch

- **Cancellation seams:** the loop already owns a per-iteration context;
  interject cancels the *current phase's* child context (LLM call or tool
  execution), tags the cancellation cause as `interject` (distinct from
  timeout/abort — the existing kill-tree and WaitDelay machinery is reused
  as-is), and routes to a fold-in point instead of turn teardown.
- **Pairing discipline:** a cancelled tool call synthesizes
  `tool_result: [interrupted by user before completion; partial output
  below if any]` so provider transcripts stay well-formed. A cancelled
  stream is truncated to complete blocks; if the truncation orphans a
  `tool_use`, the synthesized-result rule applies to it too.
- **Priority lanes in the drain:** the existing drain gains an ordering
  rule — interjects fold before queued prompts, which fold before wakeups.
  One rule, table-tested.
- **Swarm mapping:** leader mail with a new `urgency: interject` flag maps
  to the same signal level on the member's loop. Default mail stays
  wake/queue semantics — no behavior change without the flag.

## 4. Work items

- **STE-1 — Signal levels + drain ordering.** Extend the signal enum,
  ordering rule, durable-queue compatibility. *Accept:* table test covers
  all arrival orders × levels; existing queue behavior byte-identical when
  no interject occurs.
- **STE-2 — LLM-call cancellation.** Cause-tagged cancel + block-boundary
  truncation + orphan-tool_use synthesis, verified against all five
  provider clients (recording fakes). *Accept:* interject during a
  streaming response yields a well-formed history accepted by a replay
  through each provider's request builder.
- **STE-3 — Tool cancellation.** Interject during bash (sync + daemon):
  kill-tree fires, partial output captured, synthesized result paired.
  *Accept:* interject during a `sleep 300` bash returns within the kill
  grace window with the interrupted-result in history.
- **STE-4 — TUI surfaces.** Queue indicator, interject keybind, composer
  mode hint, queued-message review/revoke overlay. *Accept:* the three
  levels are visually distinct; revoking a queued message removes its
  durable row.
- **STE-5 — Headless + swarm parity.** Signal API exposure; `urgency:
  interject` mail flag mapped on the member loop. *Accept:* a leader
  interject reaches a member mid-bash in a two-member integration fixture.
- **STE-6 — Docs + changelog.** User-guide (en + zh-tw): the three levels,
  what interject does to running tools, the honesty note in history.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Provider rejects post-interrupt history (dangling tool_use) | pairing discipline is the acceptance criterion of STE-2, tested per provider |
| Killed tool leaves half-done side effects | explicit system-note tells the model; checkpoints cover files; docs set expectations for shell state |
| Interject abused where queue suffices (costly cancelled calls) | keybind separation + composer hint make interject a deliberate act |
| Swarm interject storms (leader spams cancels) | flag is leader-only and rate-noted in the team protocol; watchdog metrics count interjects |

## 6. Open questions

1. Should interject during *permission-prompt wait* simply replace the
   pending prompt (no cancellation needed)? Likely yes — cheap win, audit
   confirms the prompt-wait seam.
2. Partial-output inclusion policy for killed bash: always include tail-N
   bytes, or only on request? Leaning tail-N with the existing truncation
   caps.
3. Does interject deserve its own event kind for the web/event-log
   surfaces? (Swarm observability likes it; cheap to add at STE-5.)
