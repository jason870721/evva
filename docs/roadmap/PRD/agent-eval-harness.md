# PRD — Agent Eval & Regression Harness (transcript replay + regression scoring) — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed.
> **Target release:** TBD — wave-sized minor (claims its minor at planning
> per `CLAUDE.md` → Release workflow).
> **Roadmap source:** 2026-07-06 web research pass — industry data on
> agentic systems puts production agent failure rates at 5-15%, with the
> emerging consensus being "per-stage output validation built into the
> harness, not an afterthought." evva ships prompt/tool/model changes on
> every beta with zero automated regression signal — CHANGELOG's own
> `v1.10.0-beta.1` entry documents a real instance: three missing newlines,
> an `AAP_HOME` typo, and a self-contradicting bullet in the main system
> prompt, all shipped and only caught in a later cleanup pass.
> **Evaluation provenance:** live-source audit at `dev@ef84887`
> (v1.10.0-beta.1), 2026-07-06. All file:line references verified against
> that commit.
> **Reference source:** none — evva-native. Complements, but does not
> duplicate, `docs/roadmap/veronica/explore/EX-4-replay-eval-harness.md`
> (see §2 for the boundary).

---

## 1. TL;DR

evva has no regression testing for the thing that changes on almost every
release: prompt wording, tool descriptions, model defaults. The release
workflow in `CLAUDE.md` gates on `go test ./...` (code correctness) but has
no equivalent for *agent behavior* correctness — "ship it and watch
production" is the only feedback loop today, and it has already missed real
defects (the v1.10.0-beta.1 prompt-text fixes above shipped silently in an
earlier beta and were only caught afterward).

This PRD adds a **transcript replay harness**: capture a real (or
hand-authored) session as a reusable **fixture** using evva's own
already-shipped session-snapshot format, then **replay** it — same user
turns, new system prompt / tool schema / model — and **score** whether the
agent's decisions changed. Two scoring tiers: a cheap **structural diff**
(did the sequence of tool calls change?) as the default hard signal, and an
opt-in **LLM-judge** pass (did the outcome still satisfy a short
human-written expectation?) for fixtures where exact tool-call shape isn't
the point (e.g. "does the persona still refuse this dangerous request").

The swarm project already recognizes this need —
`docs/roadmap/veronica/explore/EX-4-replay-eval-harness.md` — but it is
still an **unclaimed, blocked, large exploration spike** scoped narrowly to
replaying one already-deployed swarm's webhook/event-log traffic. This PRD
is a different, complementary scope: the **solo agent** (where the actual
incident above happened, and where the overwhelming majority of evva usage
lives), built on the session-snapshot mechanism that already exists and is
solid, producing an actual pass/fail signal instead of "diff two runs and
eyeball it."

---

## 2. Goals / non-goals

### Goals

- Any session snapshot (`internal/session.Snapshot`, already shipped)
  becomes a **fixture**: the snapshot plus a small metadata sidecar
  (a description, an optional human-written expected outcome, and the
  originally-recorded tool-call sequence as the regression baseline).
- `evva eval capture <session-id>` turns a real session into a fixture.
  `evva eval run` replays every fixture against the **current** built-in
  system prompt/tools/model (or an explicit override, for testing a
  proposed change before merging it) and reports structural divergences.
- Structural diff is the default, cheap, hard-gate signal: compare the new
  run's tool-call sequence (name + key args) against the fixture's
  recorded baseline. LLM-judge is opt-in and advisory (§7 open question 1):
  for fixtures carrying an `ExpectedOutcome` description, one extra judge
  call scores whether the new run still satisfies it.
- Reuse, don't reinvent: the replay driver extends the exact seam
  `internal/agent/compact_test.go` already uses to build a bare test
  `Agent` (§3.4); scoring targets any configured provider via
  `pkg/llm/registry.go` so a fixture can be checked against a new model,
  not just a new prompt.
- Designed so `EX-4`, if/when it's built, can **import this PRD's scoring
  layer** (structural diff + judge) rather than reinventing it — the two
  harnesses differ in *capture* mechanism (session snapshot vs. swarm event
  log) but there's no reason to have two scoring implementations.

### Non-goals (this wave)

- **Swarm event-log replay** (webhooks, timers, multi-agent wire traffic)
  — that's EX-4's scope, not duplicated here (§2 boundary above).
- **Clock/timer injection** for fully deterministic scenario replay — EX-4
  explicitly defers this too; out of scope for the same reason (large,
  separate engineering effort with uncertain near-term payoff).
- **Byte-for-byte deterministic replay.** LLM non-determinism is a fact,
  not a bug; the harness measures **structural/decision-level** regression
  (did the model's *choices* change), never exact-text equality.
- **Auto-fixing regressions.** The harness flags a divergence; a human
  decides whether it's a bug or an intended behavior change.
- **Absolute competency benchmarking** (SWE-bench-style success-rate
  scoring against a fixed task suite). This is about regression **relative
  to evva's own prior behavior**, not an absolute capability leaderboard —
  a materially different, larger project.
- **Mandating a CLAUDE.md release-gate change.** This PRD ships the tool;
  whether `evva eval run` becomes a required step in the `pre-release
  feature` / `hotfix pre-release` preflight is the operator's call (§7).

---

## 3. Verified current state

### 3.1 Session/transcript persistence — already solid, reuse directly

- Format is plain JSON, no SQLite. Live state: `Session`
  (`internal/session/session.go:11-30` — `Messages []llm.Message`, `Usage`,
  `lastTurnInputTokens`, compaction flags). Serializes to a `Snapshot`
  envelope (`internal/session/snapshot.go:30-42`: Version/SessionID/
  Workdir/Profile/Provider/Model/timestamps) wrapping `SessionState`
  (`:47-53` — the actual transcript). Round-trip via `ToSnapshot()`/
  `FromSnapshot()` (`:58-82`).
- On disk: one file per session, `<APP_HOME>/sessions/<workdir-slug>/
  <session-id>.json` (`internal/session/store.go:1-3,28`), atomic
  temp+rename write (`:233-257`); `Save`/`Load`/`List`/`Delete`
  (`:54/76/111/217`).
- `ResumeSession` is real and load-bearing for this PRD:
  `internal/agent/agent.go:1600` loads via `session.Load`, delegates to
  `ResumeSnapshot` (`:911`), which rebuilds persona/tools/LLM client from
  the snapshot. Public mirror: `pkg/agent/types.go:135`
  (`Agent.ResumeSession`), `:37-46` (`ResumableSession` DTO), adapter
  `pkg/agent/agent.go:357-358`. **The fixture format in this PRD is this
  snapshot format, unmodified, plus a metadata sidecar (§4.1) — no new
  transcript representation is invented.**

### 3.2 `pkg/llm.Client` — no existing replay/recording primitive

- Interface: `pkg/llm/client.go:15-37` — `Name/Model/
  SupportsDeferLoading/Complete/Stream/Apply`.
- No `MockClient`/`FakeClient`/recorder exists anywhere (confirmed by
  repo-wide grep). The actual convention is that every test package
  hand-rolls its own tiny single-call `stubClient`/`stubLLM` with a
  scripted `complete func(...)` closure (`pkg/llm/registry_test.go:15`,
  `pkg/agent/downstream_test.go:22`, `internal/agent/compact_test.go:
  20-33`) — none are shared, none are multi-turn.
- Neither replay primitive exists today: no VCR-style canned-response
  recorder, and no transcript-prefix replay driver against a *real* model.
  The latter (replay driver, §4.2) is architecturally straightforward
  because `Message`/`Response` are already provider-agnostic and
  `pkg/llm/registry.go` already factories real clients by name — this PRD
  builds that missing driver rather than a mocking framework, because the
  whole point is to see what the **real** new prompt/model actually does.

### 3.3 RP-17 (event log) and EX-4 (the sibling proposal) — the boundary

- RP-17 (**done**, 2026-06-10): `internal/swarm/service/eventlog.go` — an
  `eventLog` type (`:27-38`) mirrors one swarm space's WS events to daily
  JSONL under `.vero/events/`. Swarm-only, entirely separate from
  `internal/session`.
- EX-4 (**still an unclaimed exploration spike**, size "large", hard-
  blocked on RP-17): proposes replaying a *deployed swarm's* webhook/wake/
  operator-message inputs from that event log into an isolated staging
  space, comparing two prompt versions' behavior by manual diff (no
  scoring tooling proposed). Explicitly swarm-only (keyed off
  `bus.go:103`'s webhook idempotency seam and `alarm.Config.Now`),
  explicitly defers clock injection, explicitly excludes external-world
  (market data) replay. Zero code exists for it.
- **This PRD is not EX-4 and does not replace it.** EX-4 is production
  forensics for one already-running swarm's wire traffic; this PRD is a
  pre-release regression gate for the solo agent (and, by construction,
  any swarm member — a member's turns are still `internal/session`-shaped
  underneath), built on the snapshot mechanism, with an actual scoring
  layer. If EX-4 is ever built, it should import this PRD's structural-
  diff/judge scorer rather than invent its own (§2 goals).

### 3.4 The nearest existing test-harness seam

- No "replay N turns through the loop" harness exists today — the closest
  precedent, `internal/agent/compact_test.go`, calls `microCompact`/
  `fullCompact` directly, bypassing `Run` entirely.
- Its `newTestAgent(client llm.Client) *Agent` helper (`compact_test.go:
  38-46`) builds a bare `Agent{session: session.New(), llm: client}`,
  skipping the full `agent.New()` construction/wiring. This is the right
  base to extend: inject state via the snapshot machinery (§3.1), script
  or point `llm` at a real client, drive turns forward, assert on
  `GetMessages()`/tool calls. The replay driver (§4.2) generalizes this
  exact helper rather than inventing a second agent-construction path.
- No golden/snapshot-fixture test convention exists in the repo today —
  the only `testdata/` directories (`pkg/mcp/testdata/stdio-echo-server`,
  `internal/swarm/agentdef/testdata/`) are unrelated fixtures (an MCP test
  server, swarm manifest YAML). `testdata/evalfixtures/` (§7 open question
  2) would be a new convention, not an existing one to conform to.

---

## 4. Design

### 4.1 Fixture format — a snapshot plus a thin sidecar

```go
// pkg/evalharness/fixture.go
type Fixture struct {
    Snapshot        session.Snapshot   // unmodified — §3.1's existing format
    Description     string             // human-authored, what this exercises
    ExpectedOutcome string             // optional; non-empty enables judge scoring
    Baseline        []ToolCallSummary  // {Name string; KeyArgs map[string]string}
                                       // recorded once at capture time
}
```

`evva eval capture <session-id> --out testdata/evalfixtures/<name>.json`
loads the session via the existing `session.Load`, extracts
`Baseline` from its recorded tool calls, and prompts for/accepts
`Description` (and optionally `ExpectedOutcome`) on the command line.

### 4.2 Replay driver — truncate to user turns, run forward for real

Replaying old **assistant** turns verbatim tests nothing (it's static
data); the useful question is "given the same **user** turns, what does the
*new* configuration decide?" So the driver:

1. Takes `Fixture.Snapshot`, truncates `SessionState.Messages` to just the
   user-authored turns (strips prior assistant/tool-result turns).
2. Builds an `Agent` via the `newTestAgent`-style seam (§3.4), but wired
   through the **current** (or `--against-prompt`-overridden) system
   prompt/tool set, and a real `llm.Client` from `pkg/llm/registry.go`
   (any configured provider — so a fixture can be checked against a
   candidate new model, not just a new prompt).
3. Feeds the user turns through the loop one at a time, capturing the
   resulting tool-call sequence as `RunResult.Actual []ToolCallSummary`.

```go
// pkg/evalharness/replay.go
func Replay(ctx context.Context, f Fixture, cfg ReplayConfig) (*RunResult, error)
```

### 4.3 Scoring — structural diff (hard), judge (advisory)

- **Structural diff** (`pkg/evalharness/diff.go`): a sequence comparison of
  `RunResult.Actual` against `Fixture.Baseline` — same tool names in the
  same order with materially similar key args is a pass; any divergence is
  reported with the exact point of departure. This is the default,
  zero-extra-LLM-call signal and (§7 open question 1) the recommended hard
  gate.
- **LLM-judge** (`pkg/evalharness/judge.go`, opt-in via `--judge`, only
  runs when `Fixture.ExpectedOutcome != ""`): one extra call handing a
  judge model the new run's final transcript plus `ExpectedOutcome`,
  returning pass/fail + a one-line reason. Recommended **advisory**, not a
  hard gate, until there's enough data to trust its false-positive rate
  (§7 open question 1) — this mirrors the general 2026 industry pattern of
  layering judge-model validation on top of, not instead of, structural
  checks.

### 4.4 CLI

```
evva eval capture <session-id> --out testdata/evalfixtures/<name>.json
evva eval run [--fixtures <dir>] [--judge] [--against-prompt <file>] [--model <name>]
evva eval capture --update <name>   # re-baseline after an intentional behavior change
```

`evva eval run` exits non-zero on any structural divergence, so it's
directly wireable into `go test`/CI or the release preflight (§7) without
extra glue.

---

## 5. Work items

**EVAL-1 — Fixture format + capture.**
`pkg/evalharness/fixture.go`; `evva eval capture` subcommand wrapping
existing `session.Load`. *Accept:* capturing a real session produces a
fixture file containing an unmodified `Snapshot` plus a correctly-extracted
`Baseline`.

**EVAL-2 — Replay driver.**
`pkg/evalharness/replay.go`, generalizing `compact_test.go`'s
`newTestAgent` seam (§3.4) into a public, reusable entry point; user-turn
truncation (§4.2); real-client wiring via `pkg/llm/registry.go`. *Accept:*
replaying a fixture against the unchanged current prompt reproduces a
tool-call sequence that structurally matches its own baseline (self-
consistency check).

**EVAL-3 — Structural diff scorer.**
`pkg/evalharness/diff.go` + divergence report format (which turn, expected
vs. actual tool/args). *Accept:* a deliberately mutated system prompt
(e.g. one that removes a "always run tests before finishing" instruction)
produces a reported divergence against a fixture that exercises that path.

**EVAL-4 — LLM-judge scorer (opt-in).**
`pkg/evalharness/judge.go`, gated on `Fixture.ExpectedOutcome` and
`--judge`. *Accept:* a fixture with an expected-outcome description scores
pass on an unmodified prompt and (in a deliberately broken test prompt)
reports a reasoned fail.

**EVAL-5 — `evva eval run` CLI + exit-code contract.**
Wires EVAL-1..4 together; `--fixtures`/`--judge`/`--against-prompt`/
`--model` flags; non-zero exit on structural divergence. *Accept:* runnable
standalone or from `go test` via a thin wrapper; CI-friendly output.

**EVAL-6 — Seed fixture set + adoption.**
Capture 5-10 real fixtures from evva's own development history (e.g. a
`/rewind` flow, a dangerous-`bash`-refusal, a multi-file edit session);
document how to add more. Explicitly **recommend** (not silently assume)
wiring `evva eval run` into the `pre-release feature`/`hotfix pre-release`
preflight in `CLAUDE.md` — present it to the operator as a follow-on
decision, don't edit the release playbook unilaterally in this PRD.

**EVAL-7 — Docs + version + changelog.**
User-guide/contributor-doc section ("Regression-testing a prompt change");
`docs/extending.md` note (embedders authoring their own personas can use
the same harness); `CHANGELOG.md`; `pkg/version/version.go`.

Sequencing: `EVAL-1 → EVAL-2 → EVAL-3 → (EVAL-4 parallel) → EVAL-5 → EVAL-6
→ EVAL-7`. EVAL-1..3 alone (structural diff only, no judge) already
deliver the core value and are the smaller, higher-certainty half.

---

## 6. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Cost — every replay is a real LLM call, multiplied by fixture count | Recommend running at pre-release cadence (per `CLAUDE.md`'s own beta-cut rhythm), not per-commit; keep the seed fixture set small and curated (§2 goals: quality over quantity) |
| Non-determinism / flaky diffs | Structural diff targets tool-call *sequence*, not exact text — the more stable signal; judge mode is advisory, not a hard gate, until it has a track record (§7) |
| Fixture staleness — a tool gets renamed, or a behavior change is *intended* | `evva eval capture --update <name>` re-baselines deliberately; "fixture fails forever" is a workflow bug, not acceptable steady state |
| Scope creep toward a full benchmark suite | §2 non-goals are the fence — this is regression-vs-self, not absolute capability scoring |
| Confusion/overlap with EX-4 | §3.3 states the boundary explicitly; design goal that EX-4 (if built) reuses this PRD's scorer rather than duplicating it |

---

## 7. Open questions

1. **Should judge-mode failures block a release, or just surface?**
   Recommend advisory-only at first (structural diff is the hard gate);
   revisit once judge-mode has enough runs to characterize its false-
   positive rate.
2. **Where do seed fixtures live?** Recommend `testdata/evalfixtures/`,
   following the existing `testdata/` convention elsewhere in the repo
   (`pkg/mcp/testdata/`, `internal/swarm/agentdef/testdata/`) — but keep
   each fixture trimmed to the minimum turns that exercise its behavior,
   since transcripts can get verbose.
3. **Formal `CLAUDE.md` release-preflight integration** — propose it
   (EVAL-6), but the operator decides whether `evva eval run` becomes a
   required or advisory step, and when.
4. **Does this eventually absorb EX-4, or stay a permanent sibling?**
   Recommend: keep them separate (different capture mechanisms — session
   snapshot vs. swarm event log — for genuinely different purposes), but
   require EX-4, if built, to import this PRD's scoring layer rather than
   reinvent one (§2 goals).

---

## 8. Rollout

1. `EVAL-1..7` via `feature/agent-eval-harness` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation.
3. Beta validation: capture a fixture from a real recent session; run
   `evva eval run` unmodified (should pass, self-consistency); deliberately
   mutate the system prompt to remove a documented behavior and confirm
   the structural diff catches it; run the same fixture set against a
   second configured model and confirm the report is legible.
4. `release` promotes.
