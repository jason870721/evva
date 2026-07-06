# PRD — Context Engine v2 (pruning, dedup, microcompaction, context meter) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W5 / v1.15** per
> [../long-range.md](../long-range.md).
> **Roadmap source:** 2026-07-06 long-range planning pass. Context editing /
> tool-result clearing became a first-class provider feature in 2025 (the
> Anthropic context-management API and "microcompaction" in Claude Code);
> evva has whole-session compaction (M1, shipped) but nothing between
> "everything fits" and "summarize the world".
> **Reference source:** `ref/src` compaction machinery (already ported);
> the pruning/dedup tiers are evva-native.

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

- **CTX-1 — Block ledger.** Annotations threaded through session append;
  categories for tool result / file read / user / assistant / system.
  *Accept:* ledger reproduces byte-accurate totals against a recorded
  session fixture.
- **CTX-2 — Prune pass + tombstones.** Rules per §2; tombstone text
  finalized with prompt review. *Accept:* replayed fixture shows stale
  results tombstoned, errors and pins untouched; agent successfully
  re-reads a tombstoned file in an integration test.
- **CTX-3 — Read dedup.** `(path, mtime, size)` tracking in the fs read
  tool's metadata; collapse-to-pointer at prune time. *Accept:* three
  unchanged reads → one live copy + two pointers; a changed mtime defeats
  the collapse.
- **CTX-4 — Microcompaction.** Oldest-span summarization with a hard token
  cap per invocation. *Accept:* a long fixture crosses the threshold
  without ever triggering full compaction; summary span is marked in the
  ledger.
- **CTX-5 — Context gauge + `/context` overlay.** Status-bar %, overlay
  with top-N blocks by weight and category totals. *Accept:* gauge tracks
  the ledger; overlay updates after a prune pass visibly.
- **CTX-6 — Pinning.** Keybind on the focused block, `/context` toggle,
  compaction survival. *Accept:* pinned 50KB block survives prune +
  microcompact + full compaction in a fixture run.
- **CTX-7 — Config + docs.** Thresholds, per-rung enable flags, user-guide
  page (en + zh-tw) explaining the ladder and tombstones.

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
   Leaning no for v1 — explicit recovery keeps the model's world honest.
2. Prune aggressiveness for swarm members (whose sessions are long-lived by
   design) — same defaults or a swarm profile? Defer to fast-follow.
3. Does the W6 memory wave index tombstoned content at prune time (so
   semantic recall can resurrect it), or only at session end?
