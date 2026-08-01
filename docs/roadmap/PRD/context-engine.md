# PRD — Context Engine v2 (pruning, span compaction, context meter)

> **Audience:** senior engineers implementing this wave.
> **Status:** ✅ **BUILT** — CTX-1..7 implemented 2026-08-01.
> **Audited:** 2026-08-01 at `dev @ 023dc87` — the tip at audit time, carrying
> SBX. EVAL landed alongside as `e8f8089`; it adds `pkg/evalharness` and the
> `evva eval` command and changes nothing under `internal/session`,
> `internal/agent` or `pkg/ui`, so every finding below still holds at that tip.
> Audit pass per [../long-range.md](../long-range.md) §1 step 2 — see
> "Audit corrections" below before reading the rest: **two of the seven work
> items were already shipped**, and the wave's central vocabulary collided
> with live code.
> **Target release:** **v1.17** (claimed in `CLAUDE.md` at pickup). The
> concept draft's tentative v1.15 slot was taken by SBX while H1 closed.
> **Roadmap source:** 2026-07-06 long-range planning pass. Context editing /
> tool-result clearing became a first-class provider feature in 2025 (the
> Anthropic context-management API and "microcompaction" in Claude Code);
> evva has whole-session compaction (M1, shipped) but nothing between
> "everything fits" and "summarize the world".
> **Reference source:** `ref/src` compaction machinery (already ported);
> the pruning/dedup tiers are evva-native.

---

## 0. Audit corrections (2026-08-01, `dev @ 023dc87`)

The concept draft was written 2026-07-06, six minors before pickup. Seven
findings; the first three changed the design rather than just the line
numbers.

**1 — CTX-3 (read dedup) is already shipped, in a stronger form.** The draft
proposes tracking `(path, mtime, size)` per read and collapsing duplicates
*at prune time*. `pkg/tools/fs/tracker.go` already records
`{Timestamp, ContentHash, IsPartialView, HasReadOffset}` per read, and
`pkg/tools/fs/read.go:190-194` already returns `fileUnchangedStub` instead of
the content when a full file is re-read at an unchanged mtime. Dedup
therefore happens at **read** time — the duplicate never enters the context
at all, so there is nothing left for a prune-time collapse to reclaim. The
tracker even carries `IsPartialView`/`HasReadOffset` specifically to keep the
stub from firing on a partial read or on an Edit's post-mutation mtime bump,
which is finer-grained than the draft's `(path, mtime, size)` key. **CTX-3
reduces to verification + documentation; no new code.**

**2 — CTX-5's status-bar gauge is already shipped.**
`pkg/ui/bubbletea/components/status/model.go:174-186` (`renderContextBar`)
renders a 12-cell half-block meter shading green → yellow → red with a
1-decimal percentage, fed from `LastTurnInputTokens` /
`constant.MODEL_CONTEXT_SIZE` at `app/root.go:343`. **Only the `/context`
overlay half of CTX-5 remains.**

**3 — "microcompaction" is already taken, and means the opposite thing.**
CTX-4 proposes microcompaction = *summarize the oldest span with an LLM
call*. evva's shipped `microCompact` (`internal/agent/compact.go:188`) makes
**no LLM call** — it elides old `RoleTool` content into a placeholder, which
is the draft's CTX-2 *prune*, not its CTX-4. The name is load-bearing across
the `/compact` chooser, `event.CompactingPayload{Type:"micro"}`, the
`compact.micro` log lines, `Session.microCompacted`, and the persisted
`SessionState.MicroCompacted` JSON field. Adopting the draft's vocabulary
would have silently redefined all of it. **Resolution:** the LLM-summarizing
rung is named **span compaction** (`spanCompact`); "micro" keeps its shipped
meaning and becomes an alias for the prune rung.

**4 — the cheap rung can only run once per session.** `microCompacted` is a
`bool` (`internal/session/session.go:36`), and `compact()` reads it as the
escalation switch (`compact.go:133,149`): after one micro pass *every*
subsequent compact goes full. An escalation ladder is impossible on top of a
one-shot flag, so the ladder state becomes "did the previous rung actually
free anything", which is re-runnable by construction.

**5 — the shipped micro-compact destroys error text.**
`compact.go:226-235` preserves `IsError` but overwrites `Content` with the
placeholder, so the error *message* is gone while the error *flag* remains —
the model sees "something failed" and cannot see what. This directly violates
§2's own rule that error results carry irreplaceable signal. Fixed by CTX-2.

**6 — the placeholder is not a tombstone.**
`"[elided by auto micro-compact]"` (`compact.go:39`) states neither what was
removed nor how to recover it, which is exactly what §3 says makes pruning
safe at temperature. Fixed by CTX-2.

**7 — snapshot schema.** `SessionState`
(`internal/session/snapshot.go:46-53`) is the round-trip surface for
compaction state and is at `SnapshotVersion = 1`. Adding fields is
backward-compatible (absent → zero on load); removing `MicroCompacted` is
not, so it stays and keeps its meaning.

**Net effect on scope:** CTX-3 becomes verification-only, CTX-5 halves, and
CTX-2 turns out to be a *repair* of shipped behavior (findings 5 and 6) as
much as a new feature. The seam itself was accurate: `internal/agent/loop.go:171`
still calls `a.compact(ctx, a.session)` at the top of every iteration, which
is where the ladder belongs.

---

## 1. TL;DR

evva's context story today is binary: the transcript grows verbatim until
the compaction threshold, then a summarization pass rewrites history
(`internal/session` count/compaction machinery + `internal/agent/compact.go`
— audit pass to pin exact shapes). That loses two cheaper wins that
providers and competing harnesses now exploit aggressively:

1. **Most context weight is stale tool results.** A 40KB `read` from 60
   turns ago that was since re-read, or a test log that already served its
   purpose, doesn't need *summarizing* — it needs *dropping*, replaced by a
   one-line tombstone the model can act on (`[pruned: read of pkg/agent/loop.go
   at turn 12, 41KB — re-read if needed]`).
2. **Repeated reads of unchanged files are pure duplication.** The same
   file read three times (mtime unchanged) can collapse to the latest copy
   plus pointers.

This wave adds a **layered context engine**: prune → dedup → microcompact →
(existing) full compaction, in escalating order of cost and loss, plus the
operator-facing surfaces to see and steer it: a status-bar context gauge, a
`/context` breakdown overlay, and a **pin** mechanism (blocks exempt from
pruning). Full compaction becomes the last resort instead of the only tool.

## 2. Goals / non-goals

### Goals

- **Tool-result pruning (lossy-but-recoverable):** age- and
  supersession-based clipping of large tool results into tombstones that
  tell the model exactly how to recover (re-read, re-run). Never prunes:
  the current turn's window, pinned blocks, error results (they carry
  irreplaceable signal), and anything under a size floor.
- **Read dedup:** the fs read path records `(path, mtime, size)` per read;
  when a file is re-read unchanged, older copies collapse to pointers at
  prune time. Requires no provider cooperation.
- **Microcompaction:** when the budget is still tight after prune+dedup,
  summarize only the *oldest span* in place (bounded token cost, no
  full-stop "compacting…" pause), preserving the existing full-compaction
  path for extremes.
- **Surfaces:** status-bar gauge (% of model window, colored), `/context`
  overlay (top-N heaviest blocks by category: tool results / files /
  conversation / system), and `[pin]`/`[unpin]` — user keybind + a model
  hint in tombstone text.
- All thresholds config-tunable; defaults chosen from real transcripts.

### Non-goals (this wave)

- Provider-side context-management APIs (Anthropic's server-side clearing)
  — evva-side pruning works identically across all five providers; the
  provider feature can layer in later inside `pkg/llm/claude` without
  changing the model-visible contract.
- Semantic retrieval of pruned content (that's the memory wave, W6 — MEM
  recall can index tombstoned content later; seam noted in §4).
- Changing subagent/swarm compaction policies — solo loop first, swarm
  members inherit in a fast-follow.

## 3. Design sketch

- **Block ledger:** the session keeps lightweight per-message annotations
  `{category, bytes, turn, path?, mtime?, pinned, pruned}`. The engine is a
  pure function over the ledger: given a token budget and the annotations,
  produce a prune/dedup/microcompact plan; the agent applies it between
  iterations (same seam where compaction triggers today — audit to pin).
- **Tombstones are model-facing contracts:** each states what was removed,
  why, and the exact recovery action. This is what makes pruning safe at
  temperature: the model is never left guessing whether content existed.
- **Escalation ladder:** prune (free, recoverable) → dedup (free,
  recoverable) → microcompact oldest span (cheap LLM call, partially lossy)
  → full compaction (expensive, global). Each rung runs only if the budget
  is still exceeded after the previous one.
- **Pinning:** stored in the block ledger; surfaced in `/context`; the
  system prompt documents that pinned blocks survive. Pins survive
  full compaction by being re-injected verbatim after the summary.

## 4. Work items

All seven are built. Deviations from the concept text are the audit
corrections in §0.

- **CTX-1 — Block ledger.** ✅ `internal/session/ledger.go`. **Derived, not
  threaded** — the draft proposed annotating at append time, but
  `Session.Messages` is replaced wholesale by both compaction and
  `/rewind`, so an incrementally-maintained ledger desyncs on the first of
  those and stays wrong. `BuildLedger` recomputes in one pass. Categories:
  system / user / assistant / file / tool, with the system prompt passed in
  explicitly because it lives on the LLM client, not in the message list.
  Image results are counted by their base64 payload, not by the
  `[Image: …]` text they render as.
- **CTX-2 — Prune pass + tombstones.** ✅ `internal/session/prune.go`.
  `PlanPrune` is pure over the ledger; `ApplyPrune` is the mechanical
  half and does not mutate its input. Fixes audit findings 5 and 6: error
  results keep their text, and tombstones state subject, size, turn, and
  the recovery action. Never pruned: errors, pins, the last
  `prune_keep_turns` turns, the trailing `prune_keep_results` live results,
  anything under `prune_min_bytes`.
- **CTX-3 — Read dedup.** ✅ **Already shipped** — see audit finding 1.
  `pkg/tools/fs/read.go` returns a stub instead of the content when a full
  file is re-read at an unchanged mtime, so duplicates never enter the
  context. No new code; the accept criterion is met by construction and
  more strongly than the draft's prune-time collapse would have.
- **CTX-4 — Span compaction.** ✅ `spanCompact` in
  `internal/agent/compact.go`, renamed from "microcompaction" per audit
  finding 3. Folds the oldest ~50% at a boundary that can never land on a
  `RoleTool` message, merges the brief into the surviving user message
  when there is one (back-to-back user messages are accepted by some
  providers and rejected by others), and leaves the session untouched on
  failure so a transient does not push it to full compaction.
- **CTX-5 — Context gauge + `/context` overlay.** ✅ Gauge was **already
  shipped** (audit finding 2). New: the overlay
  (`pkg/ui/bubbletea/components/overlays/context.go`) with category
  totals, top-N heaviest blocks, and the pin control. Reads through the
  public `ui.ContextReport` DTO so `pkg/ui` stays implementable from
  outside the module — the `publicOnlyController` compile gate enforces it.
- **CTX-6 — Pinning.** ✅ Keyed on tool-result ID (message indices move
  under compaction; IDs don't). Space toggles from the overlay; pinned
  content is re-injected as quoted user text after both span and full
  compaction, because a bare `RoleTool` message is malformed once the
  assistant `tool_use` that requested it has been deleted. Pins round-trip
  through the session snapshot.
- **CTX-7 — Config + docs.** ✅ `context_prune` / `context_span` (both
  opt-OUT) plus `prune_min_bytes` / `prune_keep_turns` /
  `prune_keep_results` (zero means default). User-guide section in en and
  zh-tw covering the ladder, tombstones, and pinning.

### Ladder mechanics as built

`compact()` at `internal/agent/loop.go:171` runs one rung per iteration and
returns. Because the loop makes a real LLM call before the next check, the
escalation decision is always made against the *measured* post-rung prompt
size rather than an assumption — and a rung that finds nothing to do falls
through immediately instead of burning an iteration. This is what replaced
the one-shot `microCompacted` bool of audit finding 4.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Model trusts a tombstone but recovery is expensive (huge re-read) | tombstones carry the original size so the model can weigh recovery; prune skips blocks referenced in the last N turns |
| Pruning something subtly load-bearing | error results and recent turns are never pruned; escalation only under budget pressure; eval-harness fixtures (W4) gate the defaults |
| Ledger drift vs actual history after compaction rewrites | ledger is rebuilt from the post-compaction history, not patched |
| UI overlay bloat | `/context` is read-only snapshot, same idiom as the existing `/cost` overlay |

## 6. Open questions

1. Should tombstoned file reads auto-recover via the read cache (silent
   re-inject on next reference) instead of asking the model to re-read?
   **Resolved: no.** Shipped with explicit recovery — the tombstone names
   the action and the model takes it. Silent re-injection would make the
   prompt's contents depend on state the model can't see.
2. Prune aggressiveness for swarm members (whose sessions are long-lived by
   design) — same defaults or a swarm profile? **Still open.** Members
   inherit the global config today; a swarm profile is a fast-follow.
3. Does the W6 memory wave index tombstoned content at prune time (so
   semantic recall can resurrect it), or only at session end? **Still
   open** — W6's call to make. The seam is `PlanPrune`'s output, which
   names every block about to lose its body and is already a pure value.

## 7. Deferred

- **Subagent and swarm-member ladders.** `compact()` still returns early
  for subagents (`skip:subagent`), exactly as before this wave. Pruning
  would be safe there and is the obvious next increment, but the solo loop
  was the scoped target and swarm sessions have different lifetimes.
- **Token-accurate accounting.** The ledger measures bytes, not tokens; the
  gauge and the threshold continue to use the provider's reported token
  counts. Bytes are the right unit for *relative* weight ("which block is
  heavy") and a tokenizer per provider is a wave of its own.
