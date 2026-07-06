# PRD — Replay Lab (interactive transcript inspector + what-if branching) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W31**, suggested horizon H4
> per [../long-range.md](../long-range.md) §3b. Sits atop the eval
> harness (W4 — shares the fixture/replay engine), session tree (W7 —
> shares snapshot format + fork), and the event schema (ARC-1).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> The eval harness (W4) scores whether behavior changed; it doesn't
> help a human *understand why a run went the way it did*. When evva
> does something surprising — a wrong tool call, a bad edit, a
> derailment — the operator's only forensic tool is scrolling
> scrollback. This wave turns a recorded session into an inspectable,
> branchable object: the debugger for agent behavior, complementing
> DAP (the debugger for the code the agent writes).
> **Reference source:** none — evva-native; conceptual kin to
> time-travel debuggers and LLM-trace viewers.

---

## 1. TL;DR

**Replay Lab** loads a recorded session (the shipped snapshot format,
enriched by ARC-1 typed events) into an interactive inspector — TUI
first, exportable to the self-contained HTML of session-tree:

- **Step through** the run turn-by-turn, iteration-by-iteration: see
  the exact prompt sent (system + tools + messages as the provider saw
  it), the response, each tool call's inputs/outputs, token cost and
  cache state per call, and timing.
- **Inspect the context** at any point: what the context engine (W5)
  had pruned/pinned/tombstoned, what memory was recalled, how many
  tokens each block cost — the "what did the model actually know here"
  view.
- **What-if branch:** from any iteration, fork a **counterfactual
  run** — edit the system prompt, swap the model/route, change a tool
  result, or rewrite the user turn — and re-execute forward from that
  point (reusing the eval harness's replay engine). Compare the
  branch against the original side by side: where did the tool-call
  sequence diverge? Did the outcome improve?

This is the microscope that makes every *other* improvement wave
measurable-by-hand: "why did context pruning drop the wrong thing?"
"would routing to a bigger model here have fixed it?" "does this
prompt tweak change the derailment?" — answered by direct manipulation,
not speculation.

## 2. Goals / non-goals

### Goals

- Trace loader: snapshot + typed event log → a navigable in-memory
  model (turns → iterations → LLM calls + tool calls, with full
  request reconstruction and cost/cache/timing per call).
- Inspector TUI: step navigation, the exact-prompt view, the
  context-composition view (block ledger from W5), the tool-call
  detail view, a cost/timeline ribbon.
- What-if engine: fork-at-iteration reusing the W4 replay engine;
  mutable overlays (prompt / model / tool-result / user-turn) applied
  to the forked prefix; forward re-execution against a real or fake
  provider; the fork is a session-tree child (W7) — inspectable and
  re-forkable itself.
- Diff view: original vs branch — tool-call sequence diff (the W4
  structural-diff, surfaced visually), cost delta, outcome delta
  (LLM-judge optional, W4).
- HTML export: a self-contained replay (read-only stepper, no re-exec)
  for sharing a forensic finding — redaction-scanned (W3).
- Headless slice: `evva replay <session> --at <turn.iter> --set
  model=… --run` for scripted counterfactuals feeding eval fixtures.

### Non-goals (this wave)

- Editing the *original* session (replays and forks are immutable
  over the source; what-ifs are children).
- Live attach to a running session (that's steering/W8; replay is
  post-hoc — though a paused live session can be snapshotted and
  loaded).
- A graphical web app beyond the HTML export (the TUI is the
  interactive surface; the web console is swarm-shaped).
- Automatic root-cause diagnosis (the tool *enables* human diagnosis;
  auto-diagnosis is a much later, much harder ambition).

## 3. Design sketch

- **Faithful reconstruction is the core value:** the exact-prompt
  view must show byte-what-the-provider-saw, which requires the
  request builders to be reconstructable from the trace. The audit's
  key question: does the current snapshot + event log capture enough
  to rebuild requests, or does ARC-1/eval-capture need to record the
  assembled request? Likely the latter — recording the final request
  (post-cache-marker, post-prompt-assembly) per call is the enabling
  ticket, and it's cheap (it's already built, just not retained).
- **What-if = eval replay + overlays:** the W4 harness already
  replays turns with a swapped prompt/model; Replay Lab is that engine
  with (a) an arbitrary fork point instead of turn zero, (b) a UI to
  set overlays, and (c) a comparison view. Building W4 first means
  this wave is mostly *surface*, not engine.
- **Context view from the block ledger:** W5's per-block annotations
  (category/bytes/pinned/pruned/tombstone) render directly as the
  "what the model knew" panel — the ledger was designed to be
  inspectable; this is its payoff surface.
- **Fork lineage:** a what-if is a session-tree node with a
  `counterfactual` origin + its overlay set recorded — reproducible,
  and the overlay set can graduate directly into an eval fixture.

## 4. Work items

- **RPL-1 — Request retention (enabling ticket).** Ensure per-call
  assembled-request capture in the trace (coordinate with W4/ARC-1).
  *Accept:* a recorded session reconstructs each call's exact prompt
  byte-for-byte in a fixture.
- **RPL-2 — Trace loader + model.** Snapshot + events → navigable
  structure with cost/cache/timing. *Accept:* fixture session loads
  into the correct turn/iteration/call tree with accurate per-call
  costs.
- **RPL-3 — Inspector TUI.** Step nav, exact-prompt view, context-
  composition view, tool detail, timeline ribbon. *Accept:* stepping
  matches the recorded run; context view reflects the W5 ledger at
  each point.
- **RPL-4 — What-if engine.** Fork-at-iteration, overlays, forward
  re-exec via the W4 engine, fork-as-session-tree-child. *Accept:*
  forking with a model swap at iteration K produces a divergent child
  run; overlays recorded and reproducible.
- **RPL-5 — Diff view + judge.** Structural tool-call diff, cost/
  outcome deltas, optional LLM-judge. *Accept:* a fixture original vs
  branch renders the divergence point and cost delta correctly.
- **RPL-6 — HTML export + headless.** Read-only replay export
  (redacted) + `evva replay --set --run`. *Accept:* export opens
  offline with zero external requests and passes SEC scan; headless
  counterfactual emits a comparable result.
- **RPL-7 — Docs + changelog.** User-guide (en + zh-tw): forensic
  workflow, what-if for prompt tuning, exporting findings, the
  fixture-graduation path.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Request reconstruction is impossible from current traces | RPL-1 is the enabling ticket precisely because of this; if capture proves heavy, retention is opt-in per session with a flag |
| What-if re-exec costs real tokens surprisingly | fork clearly shows estimated cost before running; fake-provider mode for structure-only exploration; batch lane for bulk counterfactuals |
| Feature complexity balloons (it's a mini-IDE) | ruthless scoping: it's a *stepper + forker + differ*, not an editor; the engine is W4's, the storage is W7's — this wave is glue + TUI |
| Traces are huge (long sessions) | lazy loading per turn; the context view is computed, not stored twice; export caps expandable detail |

## 6. Open questions

1. Should Replay Lab and the eval-fixture *authoring* flow merge (a
   what-if branch you like becomes a saved eval case in one step)?
   Strongly leaning yes — it's the killer workflow closing W4's loop.
2. Swarm run replay (multi-member timelines) — in scope, or
   solo-session first with swarm as a fast-follow? Leaning solo first;
   multi-member timeline UI is its own design problem.
3. How much of the inspector is worth an HTML *interactive* export
   (vs static)? Leaning static-stepper only for v1 (CSP/self-contained
   constraints favor it).
