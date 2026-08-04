# PRD — Steering v2 (interrupt-grade mid-turn control)

> **Audience:** senior engineers implementing this wave.
> **Status:** ✅ **BUILT** — STE-1..6 implemented 2026-08-04.
> **Audited:** 2026-08-04 at `dev @ 6cd486e` (v1.19.0-beta.1). Audit pass per
> [../long-range.md](../long-range.md) §1 step 2 — **read §0 first.** The
> draft is wrong about the two things it builds on (the trigger it says
> exists, and the seam it says exists), so §1–§3 below are preserved as the
> historical concept text.
> **Target release:** **v1.20** (claimed in `CLAUDE.md` at pickup). The
> draft's tentative v1.18 slot was taken by MEM while H1 ran long.
> **Roadmap source:** 2026-07-06 long-range planning pass. "Steer while it
> works" became the defining interactive-agent UX of 2025-26 (Claude Code's
> mid-turn message queueing + Esc interrupt). evva already has the *passive*
> half — an iteration-boundary drain — but nothing that reaches into a
> running iteration.
> **Reference source:** `ref/src` steering/interrupt surfaces — port the
> semantics; the signal plumbing is evva-native and partially shipped.

---

## 0. Audit corrections (2026-08-04)

The draft's *diagnosis* is exactly right — a queued message really does wait
for a four-minute bash, and the TUI really has no vocabulary for urgency.
Its *description of the machinery it would build on* is wrong in two
load-bearing places, and both were discovered by reading the code rather
than by reasoning about it.

**1 — "double-Esc (existing abort, unchanged)" does not exist.** §1's
escalation table describes abort as a double-Esc gesture. A **single** Esc
cancels the whole run, in both UIs and unconditionally
(`pkg/ui/bubbletea/app/root.go:662`, `pkg/ui/lp/app/root.go:386`); there is
no double-press anywhere in the tree. This is not cosmetic: it means the
obvious key for "interject" is already taken by "abort", so the wave needs a
*third* gesture rather than a reinterpretation of a second. **Ctrl+G**
("go now") is what shipped — free in both UIs, and deliberately a separate
key rather than a composer mode, because cancelling a running call throws
away real tokens and real work and should cost a deliberate keystroke.

**2 — "the loop already owns a per-iteration context" is false, and this is
the wave.** There is exactly ONE context for an entire run. The UI creates
it in `startRun` (`root.go:1039`) and it is threaded unchanged through
`Run` → `runLoop` → `thinking` → `llmCall`, and through `dispatchToolCalls`
→ `execTool` → `tool.Execute`. Cancelling it is all-or-nothing — which is
*precisely why* the only mid-run gesture evva had was "abort". §3's
"interject cancels the current phase's child context" describes a seam that
had to be **built**, not used. That seam (`internal/agent/interject.go`,
`phase`) is the single largest item in the wave, and everything else hangs
off it.

**3 — "they're durable rows today — surface them" is false.**
`UserPromptQueue` (`internal/toolset/userprompt.go`) was a `sync.Mutex` and
a `[]string`. Nothing was persisted, so STE-4's acceptance criterion
("revoking a queued message removes its durable row") could not be met as
written. Revoke shipped as an in-memory list operation, and **the queue
stays deliberately non-durable**: a prompt the model never saw is one the
user can retype, whereas persisting it would resurrect stale instructions
on the next resume.

**4 — the pairing-discipline risk is smaller than §5 claims, and the real
coherence gap is elsewhere.** `loop.go:288` already appends the `RoleTool`
message *unconditionally* after `dispatchToolCalls`, and `execTool` already
returns a non-nil result alongside every Go-level error — so a cancelled
tool already produced a paired (if unhelpfully worded) `tool_result`, and no
provider was ever at risk of a dangling `tool_use`. The genuine gap is the
**streaming** case: on cancellation every provider returns a zero `Response`
and the partial answer is discarded — even though the user *watched it
arrive*. A transcript that drops it leaves the model denying it said what is
still on the user's screen. Capturing it is what shipped.

**5 — the partial is captured agent-side, not per-provider.** §4's STE-2
prescribes verification "against all five provider clients". There are
**six** (claude, deepseek, glm, ollama, openai, qwen), and none of them
changed: `chunkAdapter` (`internal/agent/stream.go`) is the one seam every
provider's stream already passes through, so accumulating there gives
partial capture for all six at once — and for the seventh, free. Thinking
text is deliberately *not* kept: providers that support extended thinking
require an opaque signature alongside it, and a truncated block has none.

**6 — open question 1 answers itself, in the negative.** "Should interject
during a permission-prompt wait replace the pending prompt?" It is
**unreachable from the TUI**: while an approval overlay is open it is a
modal, exclusive key consumer (`root.go:625`), so no global key — including
the interject key — is ever routed. It *is* reachable from the swarm path,
and there the existing behavior is already correct: `permission.Broker`
returns `BehaviorDeny` on context cancellation (`pkg/permission/broker.go:118`),
which produces a paired, honest "approval cancelled" result. No special
case was built.

**7 — the drain-ordering rule as drafted contradicts its own acceptance
criterion.** §3 wants "interjects fold before queued prompts, which fold
before wakeups", but the shipped order is wakeups → user prompts
(`loop.go:177-185`), so adopting the rule verbatim would *reorder* wakeup
and alarm delivery — while STE-1's own criterion demands "existing queue
behavior byte-identical when no interject occurs". Shipped rule: **interjects
fold first; everything else keeps today's order.** That satisfies both.

**8 — two pre-existing defects were found and fixed on the way through.**
Neither was in scope as written; both were directly in the wave's path.

- **A production data race.** `ToolState.UserPromptQueue()` and
  `WakeupQueue()` lazily allocated their queue on first access with no
  synchronisation, while the writer is the UI goroutine (`EnqueueUserPrompt`)
  or the alarm scheduler and the reader is the agent loop
  (`drainUserPrompts`). It had been there since mid-run queuing shipped;
  nothing exercised the concurrency until an interject test did, and `-race`
  caught it immediately. Fixed by allocating both eagerly in `NewToolState`
  — no post-construction write, so no lock is needed at all.
- **An abort during a tool reported as a crash.** `toolErr != nil` routed
  straight to `crush()`, so pressing Esc during a long bash recorded a
  failure that never happened and parked the TUI in its sticky error state.
  It now checks `ctx.Err()` first and reports the interrupt as an interrupt.

**What this wave cost, in the terms overview.md §2 tabulates: 8 corrections,
2 of them structural** (the seam had to be built; the trigger was taken).
Unlike CTX and SES, the audit *added* work rather than subtracting it — the
draft assumed two pieces of infrastructure that did not exist. Unlike MEM,
the premise was sound: the problem is real and the design was right about
what to do, only wrong about what it had to work with.

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

---

## 7. As-built (2026-08-04)

Where the shipped shape differs from §3/§4, and why. §0 covers the audit
corrections; this is the implementation record.

| Concern | Shipped |
|---|---|
| **The seam** | `internal/agent/interject.go` — a `phase` is a `context.WithCancelCause` child scoped to one LLM call or one tool batch. `errInterjected` is the cause that distinguishes steering from abort; `phaseInterjected(parent, pctx)` checks the **parent first**, so an abort racing a steer still aborts. |
| **Between-phase steers** | `phase.armed`: a steer arriving with no phase open arms the next one. The loop `disarm()`s at the top of each iteration, *before* the drains — because a steer that landed before the drains has interrupted nothing, and the honesty note must not claim otherwise. Arming survives only the window after a boundary's drains, which is the one it exists for. |
| **Partial capture** | `chunkAdapter` accumulates text; `llmCall` returns `Response{Content: partial}` alongside the error. Provider-agnostic — no client changed. |
| **Turn survival** | The loop `continue`s instead of returning. An interject consumes one `maxIters` slot, which also bounds a pathological steer loop. |
| **Tool results** | `interruptedToolResult` synthesizes `[interrupted by user before completion]` + a rune-safe 4 KB tail of partial output, and returns **no Go error** — a killed tool is an outcome to reason about, like a non-zero exit, not a run-fatal failure. `bash` now keeps its buffered output on cancellation instead of discarding it. |
| **Honesty note** | Source-attributed (`the user`, or a swarm member name) and appended **before** the steer text, so the model reads "you were cut off" before it reads what to do instead. Names only the tools whose results actually carry the marker — a fast tool that finished before the cancellation reached it is not reported as interrupted. |
| **Keys** | Ctrl+G interject (both UIs), Enter queues, Esc aborts — unchanged. Ctrl+G with nothing running degrades to an ordinary submit; if the run ends between keystroke and call, the text falls back to the queue rather than evaporating. |
| **Surfaces** | `✉ N` / `✉ N!` status cell (drop-ranked just under the state pill), a running-mode composer placeholder, `/queue` review-and-revoke overlay, and an `INTERJECTED` transcript marker rendered on the **fold** rather than the request — the request fires from the keystroke goroutine, before the loop has done anything. |
| **Events** | `KindInterject` (requested; carries `CutPhase` + `Source`) and `KindInterjectFolded` (absorbed; carries `PartialBytes`). Open question 3: **yes.** |
| **Swarm** | Migration `0008` adds `messages.urgency` (TEXT, `''` default — legacy rows read as normal with no backfill). `send_message` takes `urgency: "normal"|"interject"`; only the exact word arms it, so an invented `"urgent"` degrades to normal. The bus's `SetUrgentHook` cuts the recipient's phase **without** re-delivering the body — the row is already durable and the member's own drainer folds it at the boundary the cut brings forward. Every failure mode (idle, frozen, unknown) is a silent no-op: urgency is an optimisation on *when*, never a condition for *whether*. |
| **Not built** | The `pkg/agent` public wrapper exposes interject through `ui.Controller` only. Speculative continuation, mid-token splicing, and side-effect rollback stay non-goals (§2). |
