# PRD — Swarm TUI Attach — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed.
> **Target release:** TBD — wave-sized minor (`v1.11+` candidate). Per the
> checkpoint-rewind precedent, the CLAUDE.md wave → minor row is added only
> when the operator confirms the wave.
> **Roadmap source:** swarm design review 2026-07-04 — "the Bubble Tea TUI is
> completely blind to swarms" is the largest UX asymmetry in a
> terminal-first product whose stated unifying idea is one runtime, many
> personas, **swappable UI** (docs/architecture.md, Vision).
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1), 2026-07-04/05. All file:line references verified against
> that commit.
> **Reference source:** none — evva-native. (The web console `web2/` is the
> semantic reference: the TUI mirrors its wire protocol and reducer
> behavior, not its code.)

---

## 1. TL;DR

evva is a terminal product, but its multi-agent subsystem is observable only
through a browser. Neither `pkg/ui` nor the bare-TUI path in
`cmd/evva/main.go` touches `internal/swarm`; the swarm CLI is explicit that
"the bare `evva` (TUI) path is untouched" (cmd/evva/swarm.go:32). An operator
who lives in the terminal must alt-tab to a browser to see a gate, answer a
question, or notice a stalled worker.

Everything needed to fix this already exists **server-side**. The service
exposes a complete, UI-agnostic wire surface: REST for state
(`GET /api/swarms` webapi/api.go:472, roster :561, tasks :568, pending gates
:603, durable chatlog replay :611) and one WebSocket for live events + the
three interactive commands — `run`, `respond_permission`,
`respond_question` (`dispatchInbound`, api.go:947-962; the socket's own
comment: "the live socket carries the interactive turns … lifecycle
commands go through the REST endpoints"). The Vue console is just one
client of it.

This wave adds the second client: **`evva swarm attach <ref>`** — a Bubble
Tea program that hydrates from `/chatlog` + `/pending`, folds the live `/ws`
feed through a Go port of the web's reducer semantics
(`reduceChat`, web2/src/lib/events.ts:136), and gives the terminal operator
the four things that matter mid-run: see the roster's attention state,
read any member's stream, answer gates, and send messages. The web remains
the rich workstation (membership editing, schedules, skills, memory);
attach is the cockpit.

---

## 2. Goals / non-goals

### Goals

- `evva swarm attach <ref>` opens a live terminal console for a running
  space: roster with phase + attention ordering, per-member stream view,
  task summary, and a composer.
- Approval and question gates render as overlays and are answerable in the
  terminal (WS `respond_permission` / `respond_question`), including the
  "always allow" rule option the web offers.
- Lifecycle steering via keybindings: suspend/resume/freeze/unfreeze a
  member, halt-all — the existing REST verbs (api.go:672, :816).
- Reconnect-safe: rehydrate from the durable chatlog and pending gates,
  never blanking on a failed fetch — the same non-destructive contract the
  web learned in v1.7.4 (#43).
- One wire protocol, N UIs: the TUI consumes exactly the surface the web
  consumes; zero new server endpoints in the core wave.

### Non-goals (this wave)

- No membership editing, schedule editing, skill authoring, memory viewing,
  proposal review, or metrics dashboards in the TUI — web-only, by design.
- No embedded swarm runtime: attach never links `internal/swarm` runtime
  packages into the client path; it is an HTTP/WS client like the rest of
  the swarm CLI (process model A, swarm.go:17-19).
- No multi-space split view; one attach = one space (run two terminals).
- No offline mode — no service, no attach (clear error naming
  `evva service start`).
- Not a replacement for the solo TUI: `evva` (bare) is untouched again.

---

## 3. Verified current state

### 3.1 The TUI is a registry; swarm is not in it

- `pkg/ui` is a small registry (`pkg/ui/registry.go`) — `main.go` looks up
  a UI by name (`ui.Lookup`, cmd/evva/main.go:149; `-tui` flag :88; the
  bubbletea implementation self-registers via blank import :28) and drives
  it with a live `ui.Controller` (`tui.Attach(ag.Controller())`,
  main.go:163). The controller is a *live-agent* surface — exactly what a
  remote viewer does not have.
- The bubbletea tree already contains the rendering vocabulary a swarm
  cockpit needs: `pkg/ui/bubbletea/components/{transcript, status, input,
  overlays, agents, ...}` plus `theme/`.

### 3.2 The wire surface is complete and UI-agnostic

- REST reads: swarms :472, space/roster :561, tasks :568, messages :589,
  transcript :596, **pending gates :603**, **chatlog :611**, metrics :630.
- REST writes: run :639, message :651, suspend/resume/freeze/unfreeze
  (verb map :668-676), clear :679 (409 when busy), compact :696, halt :816.
- WS: `GET /ws` (api.go:838) → `serveSocket`; subscription filtered by
  `(spaceID, agentID)` (hub.go:37-40, Publish :61); inbound `wsCommand`
  {type: run | respond_permission | respond_question} dispatched at
  api.go:947-962; failures echo a `command_error` frame (:967-971) so a
  misrouted gate reply is visible, not silent.
- Auth: bearer token, constant-time guard (`tokenGuard`, api.go:863-877);
  loopback bootstrap `GET /api/auth/bootstrap` (:463). The CLI already has
  the whole client stack: `targetAddr` (cmd/evva/servicectl.go:64),
  `readToken` (:100), `serviceClient` (:118).
- The websocket package is already a dependency — the service handler is
  `websocket.Handler` (api.go:838); its client `Dial` ships in the same
  package. No new module.

### 3.3 The reducer semantics live in the FE (and only there)

`reduceChat` (web2/src/lib/events.ts:136) folds wire events into turns —
chunk accumulation, tool cards, `user_message` merging (:183), turn
boundaries — and `attentionKind`/`attentionItems` (:381, :423) define
"who needs you". These are the *specification* the TUI must match; the
durable chatlog is deliberately replayed "through the same reducer as the
live WS feed" (RP-17 design), and a second client must hold the same
property or drift.

### 3.4 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| CLI client plumbing | servicectl.go:64,100,118 | addr/token/REST calls — attach adds `--addr`/`--token` flags on top |
| Durable replay | `GET …/chatlog` (api.go:611), v1.8.5-beta.1 | Hydration source; survives compaction (the RP-17 fix) |
| Gate re-hydration | `GET …/pending` (api.go:603) | Answerable gates after connect/reconnect |
| Bidirectional WS | serveSocket (api.go:904) + dispatchInbound (:947-962) | Live feed + gate replies + leader chat on one socket |
| Terminal rendering vocabulary | pkg/ui/bubbletea/components, theme | Viewport/stream/status/overlay patterns and theme tokens to reuse |
| Verb REST endpoints | api.go:668-676, :816 | Lifecycle keys are thin POSTs |
| Non-destructive rehydrate lesson | v1.7.4 (#43), web2 stream store | The reconnect contract the TUI copies |

---

## 4. The architecture decision: a wire-protocol client, not a runtime embedding

Three ways to put a swarm in a terminal:

1. **Link `internal/swarm` into the CLI and render spaces in-process** —
   rejected. It breaks process model A ("the service builds the agents, the
   CLI only POSTs intent", swarm.go:17-19), would fight the service for the
   same `.vero` stores, and turns one long-lived daemon into two.
2. **Adapt the existing `pkg/ui` bubbletea app by faking a
   `ui.Controller` fed from WS events** — rejected. The controller is a
   live-agent command surface (run, permissions, token usage probes);
   impersonating it from a remote stream inverts the dependency and imports
   solo-agent semantics a viewer doesn't have. The solo TUI renders *an
   agent*; attach renders *a space*.
3. **A dedicated wire-event client TUI** (`internal/swarm/tui`) — chosen.
   It consumes the same JSON the browser consumes, holds the same reducer
   semantics, and stays inside the swarm subsystem's boundary (it may
   import `internal/swarm/webapi` for wire DTO types — a types-only
   dependency; the depcheck pkg-purity rule constrains `internal/swarm/**`
   against `internal/agent`, not against its own webapi).

One consequence worth stating: the reducer gets **reimplemented in Go**.
That is a deliberate duplication with a contract — TUI-1 pins it to
recorded fixtures generated from real spaces so the Go reducer and
`events.ts` can only drift loudly (a failing golden test), never silently.

---

## 5. Design

### 5.1 D1 — Command surface

```
evva swarm attach <ref> [member]
    [--addr host:port]   # default: targetAddr() resolution
    [--token t]          # default: readToken() from <AppHome>/service/
```

New verb in `runSwarm` (swarm.go:33). `<ref>` resolves id-or-name via
`GET /api/swarms` (the existing convention). Optional `member` opens
focused on that member's stream. Non-TTY stdout → refuse with "attach needs
a terminal; use the web console at <url>".

### 5.2 D2 — Client core (`internal/swarm/tui/client`)

- REST: reuse `serviceClient`-shaped helpers parameterized by addr/token
  (small refactor of servicectl.go so flags can override; the functions
  already exist at :64,:100,:118).
- WS: `websocket.Dial(ws://…/ws?token=…&space=…)`; JSON frames decode into
  Go wire-event types (mirrors of the payloads `publish` marshals,
  service.go:874). Inbound commands marshal the `wsCommand` shape
  (api.go:932-944).
- Reconnect loop: exponential backoff (1 s → 15 s cap); on reconnect:
  re-fetch `/pending` + tail `/chatlog`, fold both through the reducer, and
  **merge** — never clear on error (the v1.7.4 contract). The known
  mid-stream gap (a turn streaming across the reconnect shows only
  post-boundary text until its coalesced block lands) is inherited from the
  chatlog design and documented, not fought.

### 5.3 D3 — The Go reducer (`internal/swarm/tui/reduce`)

A faithful port of `reduceChat` semantics: chunk accumulation by kind,
tool-use cards keyed by call id, thinking/text block closure on
tool-dispatch/turn-end/run-end/error/type-switch, `user_message` merge,
stable time ordering. Plus the phase/attention derivation
(`attentionKind`/`attentionItems` semantics: act > warn, longest-wait
first). Golden-tested against fixtures recorded from a live space (one
scripted run checked into testdata as JSONL + expected fold).

### 5.4 D4 — The program (`internal/swarm/tui/app`)

Layout (one alt-screen Bubble Tea program, theme from
pkg/ui/bubbletea/theme):

```
┌ roster ──────────┬ stream: qa ────────────────────────────┐
│ ▸ lead   idle    │ [10:31] lead → qa  task #42 …          │
│ ● qa   ⚠ gate   │ [10:32] qa  bash: go test ./…           │
│ ● dev-a  run    │          └ exit 1 (tail…)               │
│   dev-b  frozen │ [10:33] qa ✋ approval: bash "rm -rf …"  │
├ tasks ───────────┤ …                                       │
│ #42 verifying qa │                                         │
│ #43 pending  a   ├ composer ──────────────────────────────┤
│ …                │ > message qa…              [enter=send] │
└──────────────────┴─────────────────────────────────────────┘
```

- **Roster pane**: members with phase pills, ordered by attention (act >
  warn > rest); `enter` focuses that member's stream; badge counts.
- **Stream pane**: the focused member's folded turns (viewport,
  transcript-style rendering); `a` = all-members interleaved view.
- **Tasks pane**: compact `GET /api/tasks` list (status glyph, id, title,
  assignee), 5 s poll while visible — read-only.
- **Gate overlay**: opens automatically for the focused member's gate (and
  a status-line beacon for unfocused ones); approve / deny / always-allow /
  answer multi-select; sends the WS command; a `command_error` frame
  reopens the overlay with the error and re-fetches `/pending`.
- **Composer**: message the focused member (`POST …/message`) or
  `:run <prompt>` a leader turn (WS `run`).
- **Lifecycle keys**: `s/r` suspend/resume, `f/u` freeze/unfreeze (POST
  verbs), `H` halt-all with confirm, `q` detach (never stops the space).

### 5.5 D5 — Failure surfaces

Service down at launch → one-line error with `evva service start` hint.
Token mismatch → name the token file. Space not found → list available
refs (the `swarm ls` data). Mid-session WS loss → status-line "reconnecting
(nth)…" while panes keep last state.

---

## 6. Work items

**TUI-1 — Wire types + Go reducer + golden fixtures.**
Event/DTO structs, `reduce` package (5.3), fixture recorder (a dev-only
`go test` helper that replays a checked-in JSONL), goldens for fold +
attention ordering.
*Accept:* goldens cover chunked text/thinking, tool cards, user_message
merge, gate lifecycle, and match a fixture regenerated from the same input
byte-for-byte; attention ordering matches events.ts semantics on the same
fixture.

**TUI-2 — Client core.**
REST helpers with addr/token override (servicectl refactor), WS
dial/decode/send, reconnect + non-destructive rehydrate (5.2).
*Accept:* httptest + in-process ws server tests — hydrate, live fold,
reconnect merge (no blanking), command_error round-trip.

**TUI-3 — App shell.**
Program scaffold, roster + stream + tasks panes, focus model, theme reuse,
resize handling.
*Accept:* renders a recorded space fixture correctly at 80×24 and 200×60;
pane navigation and member focus work; tasks poll only while visible.

**TUI-4 — Gates.**
Overlay UX for approval (incl. always-allow rule) and question
(multi-select), WS commands, error re-open, unfocused-gate beacon,
`/pending` hydration.
*Accept:* answering a gate unblocks a live member; a denied/misrouted reply
surfaces the `command_error` and re-hydrates; gates raised while detached
appear on attach.

**TUI-5 — Composer + lifecycle keys.**
Message send, leader `run`, verb keys with optimistic pill updates +
server-truth reconcile, halt confirm.
*Accept:* each action hits its endpoint exactly once; a 409 (busy clear —
not bound by default, guard anyway) and error toasts render.

**TUI-6 — CLI verb + help + non-TTY guard.**
`attach` in `runSwarm` (swarm.go:33) + `swarmHelp` (:103), flags, ref
resolution, exit codes.
*Accept:* `evva swarm attach <name>` connects by name and by id; non-TTY
refuses with the web URL; `--addr/--token` reach a remote (RP-15
`--allow-remote`) service.

**TUI-7 — Docs + demo.**
User guide (en, zh-tw) "the terminal cockpit" — keys, gate flow, what
stays web-only; README screenshot/gif; CHANGELOG.
*Accept:* docs in both languages; keybinding reference matches the code.

Sequencing: `TUI-1 → TUI-2 → TUI-3 → {TUI-4, TUI-5} → TUI-6 → TUI-7`.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| TUI-1 | pure-Go golden tests (no terminal) | seconds |
| TUI-2 | httptest/ws in-process suites | seconds |
| TUI-3..5 | bubbletea model-level tests (Update/View snapshots, the existing pkg/ui testing pattern) — no PTY needed | seconds |
| all | no new dependencies (websocket pkg already in the module) | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Go reducer drifts from events.ts | The golden-fixture contract (TUI-1) — semantics changes must update both sides' fixtures deliberately; the chatlog replay endpoint is the shared upstream |
| Duplicated reducer maintenance cost | Accepted trade-off of §4; the alternative (server-side folding for all UIs) is a larger server change — revisit if a third UI appears (open question #2) |
| Terminal rendering of wide/rich content (diffs, long tool output) | Reuse the transcript component's wrapping discipline; tool results collapse by default, expand on key |
| WS event volume on big spaces | Subscribe space-wide but fold only chunks for the focused member eagerly; background members fold turn-boundary events only (chunks skipped — same data the chatlog keeps) |
| Two operators (web + TUI) answer the same gate | Server already arbitrates (first reply wins; the second gets an error echo) — the overlay's error path (TUI-4) handles the loss gracefully |
| Attach against an older service version | `/healthz` version probe at connect; unknown event kinds are ignored by the reducer (forward-compatible by construction) |
| Scope creep toward the web console | §2 non-goals is the fence: cockpit, not workstation |

---

## 9. Open questions

1. **Should attach be offered from the bare TUI (`/swarm attach` slash
   command) too?** Recommend defer — the slash surface implies deeper
   solo/swarm integration; ship the standalone verb first.
2. **Server-side turn folding (one reducer for all UIs)?** Recommend
   revisit after this wave proves the wire contract; it would move
   `reduceChat` semantics behind `/chatlog` for live frames too — a bigger
   protocol change than a cockpit justifies today.
3. **Read-only mode (`--observe`) for demos/pairing?** Recommend yes if
   cheap — hide composer/verbs, skip token write scopes; one flag.
4. **Mouse support?** Recommend keyboard-first v1; the component layer
   already has mouse plumbing if demand appears.

---

## 10. Rollout

1. TUI-1..TUI-7 via `feature/swarm-tui-attach` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation.
3. Beta validation: attach to the tech-team and werewolf examples (13
   members — the fold-volume stress case); a full gate flow (approve,
   deny-with-reason, always-allow, multi-select question); a kill-and-restart
   of the service mid-attach.
4. `release` promotes.
