# PRD — DAP Debugger Integration (evva sets breakpoints, steps, inspects) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W30**, suggested horizon H3
> per [../long-range.md](../long-range.md) §3b.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> Almost no terminal agent can *debug* — they all printf. The Debug
> Adapter Protocol (DAP) is LSP's sibling: one JSON protocol, dozens of
> adapters (delve, debugpy, node-inspect, lldb/codelldb). evva already
> hand-writes LSP + JSON-RPC — the exact skill set DAP needs — and a
> shipped LSP module to mirror architecturally.
> **Reference source:** none in `ref/src` — evva-native; prior art is
> the DAP spec + editor debugger UIs.

---

## 1. TL;DR

When a test fails mysteriously, evva's only move today is bisecting with
print statements — burning turns, tokens, and file edits on what a
debugger answers in one stop. This wave adds `pkg/tools/dap`: a Debug
Adapter Protocol client (mirroring the `pkg/tools/lsp` module's shape:
config-declared adapters, lifecycle manager, one request tool) plus a
small tool family the model drives:

| Tool | Does |
|---|---|
| `debug_launch` | start a DAP session (launch or attach) against a config-declared adapter, with breakpoints |
| `debug_control` | continue / step over / step in / step out / pause; reports the stop event (reason, frame) |
| `debug_inspect` | stack trace, scoped variables, watch expressions, `evaluate` in frame context |
| `debug_stop` | terminate/disconnect + adapter teardown |

The loop this unlocks is the human one: hypothesize → breakpoint → run →
inspect state at the stop → fix — often one model turn per step, with
*ground-truth* runtime state instead of guessed state. Paired with LSP
(static truth) and the test loop, evva gets the full trio an IDE has.

Adapters are external binaries the operator already has (`dlv dap`,
`debugpy`, `js-debug`), declared in config exactly like LSP servers —
zero new Go dependencies, protocol hand-written per house policy.

## 2. Goals / non-goals

### Goals

- DAP client core: stdio transport, the DAP base protocol
  (seq/request/response/event), capability negotiation, initialize/
  launch/attach/configurationDone handshake — golden-frame tested
  against recorded adapter transcripts.
- Adapter registry in config (`dap_servers`, mirroring `lsp_servers`):
  command, args, launch-config template per language; ship documented
  presets for Go (delve), Python (debugpy), Node (js-debug).
- Stop-event digest: when execution stops, the tool result is a compact
  brief — reason, location, top frames, in-scope locals (size-capped) —
  designed for model consumption, not a UI dump.
- Bounded execution: every `debug_control` call has a timeout; a
  program that never stops returns "still running" with a handle rather
  than hanging the turn (the daemon-kind pattern for long-lived
  processes applies).
- Session hygiene: debug sessions are session-scoped daemons — visible
  in listings, killed on session end; one active debug session per
  agent session in v1.
- Permission gating: `debug_launch` is an execution permission (it runs
  the program) — same gate class as `bash`.

### Non-goals (this wave)

- A TUI debugger UI (panels, gutters). The *model* is the debugger
  user; the human watches tool results. An operator-facing UI can ride
  a later TUI wave if demand appears.
- Remote debugging across machines (attach is local-process only in
  v1).
- Adapter installation/management (operator installs `dlv` etc.; the
  doctor wave checks for them).
- Time-travel/reverse debugging (adapter-specific; not portable).
- Hot code reload.

## 3. Design sketch

- **Mirror the LSP module:** the audit pass maps `pkg/tools/lsp`'s
  manager/lifecycle/config shapes and reuses the same idioms —
  developers who know one module should know both. The JSON-RPC-ish
  dispatcher differs slightly (DAP's protocol is its own framing, not
  LSP's JSON-RPC — same Content-Length headers, different envelope),
  so the transport layer is shared-by-copy-of-idiom, not by import,
  unless the audit finds a clean common core.
- **Launch templates:** a `debug_launch` call names a preset +
  overrides (program, args, cwd, env). Templates keep the model's
  schema small; raw launch-config passthrough exists behind an
  `advanced` field for unusual adapters.
- **The stop-digest is the product.** DAP responses are verbose; the
  digest discipline (top N frames, locals truncated at depth 2, strings
  capped) is what makes this usable within context budgets. The
  context engine (W5) categorizes debug results for aggressive pruning
  — old stops are the most tombstone-able content imaginable.
- **Failure honesty:** adapter crash, port conflicts, and
  compile-before-debug failures surface as distinct, actionable errors
  (delve needs a debuggable build; the preset encodes the flags).

## 4. Work items

- **DBG-1 — Protocol core.** Framing, envelope types, handshake
  state machine, capability negotiation, golden-frame tests from
  recorded delve/debugpy transcripts. *Accept:* initialize→launch→
  setBreakpoints→configurationDone→stopped round-trips against both
  recorded transcripts.
- **DBG-2 — Adapter registry + lifecycle.** Config schema, spawn/
  teardown, daemon-kind registration, one-session guard. *Accept:*
  config round-trips; killing the agent session reaps the adapter
  process (kill-tree reuse).
- **DBG-3 — `debug_launch` + presets.** Go/Python/Node presets,
  breakpoint setting, launch vs attach. *Accept:* fixture Go program
  stops on a line breakpoint via the delve preset in CI.
- **DBG-4 — `debug_control` + stop digests.** Continue/step*, timeout
  discipline, still-running handles, digest format. *Accept:* stepping
  through the fixture yields digests under the size cap with correct
  frames; a nonstop `continue` returns the handle within timeout.
- **DBG-5 — `debug_inspect`.** Stack/scopes/variables/evaluate with
  depth+size caps. *Accept:* inspecting a struct-heavy frame truncates
  predictably; `evaluate` returns expression results in-frame.
- **DBG-6 — Loop integration.** System-prompt guidance (when to debug
  vs printf), context-engine category, permission wiring. *Accept:*
  an end-to-end fixture: failing test → launch → breakpoint → inspect
  → the model identifies a planted wrong-value bug (fake-LLM scripted).
- **DBG-7 — Docs + changelog.** User-guide (en + zh-tw): adapter
  setup per language, presets, limits; troubleshooting table.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Adapter behavioral quirks (delve vs debugpy diverge on spec corners) | capability negotiation + per-preset quirk table + golden transcripts per adapter; support the documented three first, others best-effort |
| Verbose state floods context | digest caps are hard; deeper inspection is explicit (`debug_inspect` on a named variable), never automatic |
| Hung adapters wedge the session | timeouts on every request + daemon kill-tree + `debug_stop` always works (disconnect with force) |
| Model overuses debugging for trivial bugs | system-prompt guidance frames it as the escalation after one failed hypothesis, not the first move |

## 6. Open questions

1. Shared protocol core with LSP (extract a common Content-Length
   framing package) or keep the modules independent? Audit decides by
   measuring actual overlap.
2. Conditional breakpoints and logpoints in v1? (Both are cheap
   protocol-wise; logpoints are printf-without-edits — arguably the
   killer feature. Leaning yes for logpoints.)
3. Attach-to-running-process permission ergonomics: same gate as
   launch, or scarier? (It touches a process evva didn't start.)
