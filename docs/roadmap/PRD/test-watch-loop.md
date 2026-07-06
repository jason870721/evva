# PRD — Test Watch Loop (continuous verification during a session) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W24**, suggested horizon H2
> per [../long-range.md](../long-range.md) §3b.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2. The
> agent-authored bug that ships is the one nobody re-ran the tests on.
> evva runs tests when the model *decides* to; humans using watch modes
> (`go test -run`, jest --watch, cargo-watch) get failure feedback
> pushed to them within seconds of a save. The agent deserves the same
> reflex — and evva already has the delivery half built: the monitor
> tool and the between-turns diagnostics drain (edit→LSP sync) prove
> the "push signal into the loop" pattern twice over.
> **Reference source:** none — evva-native.

---

## 1. TL;DR

A **watch unit**: a session-scoped background runner that re-executes a
configured verify command when the agent's edits touch matching files,
and delivers failures into the conversation the same way LSP
diagnostics already arrive — as a between-turns drain, or (opt-in)
folded into the offending edit's own tool result.

```yaml
# .evva/watch.yml (or inline via /watch on)
watch:
  - name: unit
    run: go test ./... -count=1
    debounce: 2s
    scope: ["**/*.go"]          # edits matching this trigger the run
    mode: drain                  # drain | inline | badge
```

The critical design choice: **trigger on evva's own edit/write tool
calls, not on filesystem events.** The fs tools are already the choke
point (checkpoints and LSP sync both hook there) — hooking the same
seam gives exact causality (this edit → this failure), needs no
fsnotify dependency, and never fires on the human's unrelated editor
saves unless they opt into fs-watching later.

Failure output is digested (failed tests only, first assertion diff,
capped), attributed ("after your edit to `foo.go:42`"), and rate-limited
— the loop must make the agent *converge*, not thrash.

## 2. Goals / non-goals

### Goals

- Watch unit lifecycle: `/watch on|off|status` + `.evva/watch.yml`
  config; runs as a session daemon (existing daemon abstraction,
  visible in listings, dies with the session).
- Edit-triggered scheduling: fs edit/write success → scope match →
  debounced enqueue; runs serialize (never two concurrent runs of the
  same unit); a run superseded by newer edits is cancelled and
  re-queued (freshest-wins).
- Result digestion: pass → a one-line badge-level signal (status bar
  tick + optional drain note on first green after red); fail → failed
  test names + minimal diff excerpt + attribution, size-capped.
- Delivery modes: `drain` (between-turns reminder, default), `inline`
  (fold into the triggering edit's result when the run completes within
  a short bounded window — mirroring `lsp_diagnostics_on_edit`'s shape
  and default-off), `badge` (status bar only; the model asks).
- Red/green state machine per unit: transitions are the only events
  worth words (red→green and green→red get reported; red→still-red
  reports only *newly*-failing tests).
- Works for any command, not just tests — linters, builds, typecheckers
  are the same shape.

### Non-goals (this wave)

- Filesystem watching (human-edit triggers) — explicitly deferred; the
  agent-edit seam covers the agent loop, which is this wave's product.
- Test selection intelligence (mapping edits → affected test subset) —
  the command is operator-authored; smart selection is a natural v2
  once TSI (W28) exists.
- Fixing the failures automatically — signal only; the model decides.
- CI integration (W12 owns CI; this is the interactive loop).
- Watch across swarm members (leader-configured member watch units are
  a fast-follow once solo soaks).

## 3. Design sketch

- **Seam reuse:** the audit pins the exact post-success hook shape used
  by `CheckpointSink`/`LSPSyncSink` in `pkg/tools/fs` — the watch
  trigger is a third sink implementing the same nil-safe injection
  pattern. Three consumers = the sink seam is now proven and a
  candidate for ARC-2 middleware consolidation later.
- **Debounce + supersede:** an edit burst (the model often edits 3–5
  files in one turn) coalesces to one run; a run mid-flight when new
  edits land is killed (kill-tree) and rescheduled — stale greens are
  worse than no signal.
- **Digest discipline:** parse-free by default (tail + grep-shaped
  extraction of failure blocks, capped); optional per-unit `parser:
  go-test|jest-json|junit` for structured extraction where the format
  is known. Never ship the full log into context — the model can ask
  the daemon for more via the existing monitor/task-output surfaces.
- **Anti-thrash:** per-unit cooldown floor; if N consecutive runs fail
  with identical digests, escalate once ("same failure 3 runs — am I
  looping?") then go quiet until the digest changes. This nudge is the
  wave's most valuable prompt-engineering artifact.

## 4. Work items

- **TWL-1 — Watch unit + config.** Schema, `/watch` commands, daemon
  registration, lifecycle. *Accept:* config round-trips; on/off/status
  behave; unit dies with the session.
- **TWL-2 — Edit-trigger sink.** Third fs sink, scope matching,
  debounce, supersede-and-requeue. *Accept:* a 4-file edit burst
  triggers exactly one run; an edit during a run cancels and re-queues
  it (observable via a slow fixture command).
- **TWL-3 — Digestion + state machine.** Red/green transitions,
  newly-failing extraction, caps, attribution. *Accept:* fixture
  transitions produce exactly the specified reports; still-red with
  identical digest produces silence.
- **TWL-4 — Delivery modes.** Drain reminder, bounded inline window,
  badge; config default `drain`. *Accept:* each mode delivers per spec;
  inline respects its window and falls back to drain.
- **TWL-5 — Anti-thrash + prompt guidance.** Cooldown, loop-detection
  nudge, system-prompt note teaching the model the signal exists.
  *Accept:* scripted identical-failure loop triggers the nudge once.
- **TWL-6 — Docs + changelog.** User-guide (en + zh-tw): config
  reference, mode trade-offs, "watch is a signal, not a gate"
  positioning vs the verify-command conventions elsewhere (gardener,
  swarm verify-checks).

## 5. Risks

| Risk | Mitigation |
|---|---|
| Watch noise burns context | transition-only reporting + digests + badge mode; the anti-thrash quiet rule |
| Slow suites make the loop laggy | debounce + supersede + per-unit timeout; docs push operators toward fast targeted commands (the `scope`+`run` pairing exists precisely for this) |
| Runs mutate state (badly-written test suites) | docs require idempotent commands; watch runs inherit the session's sandbox tier when W3 is active |
| Interference with the model's own test runs | serialization per unit + the model's manual runs are unaffected (different invocation); status shows a run in flight |

## 6. Open questions

1. Should first-green-after-red interrupt-grade notify (STE integration)
   rather than wait for the drain? (Satisfying, but interrupts are
   sacred — leaning no.)
2. Per-unit `scope` default: all files, or require explicit globs?
   Leaning explicit — accidental whole-repo rebuild loops are the #1
   footgun.
3. Does `/watch` deserve a bundled-skill quickstart that writes the
   yml for common stacks (go/jest/pytest)? Cheap, probably yes.
