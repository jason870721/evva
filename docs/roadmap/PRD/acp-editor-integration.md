# PRD — ACP Editor Integration (evva inside Zed and ACP-speaking editors) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references — and re-verify the ACP spec version — before
> implementation).
> **Target release:** TBD — tentative slot **W11 / v1.21** per
> [../long-range.md](../long-range.md), paired with
> [mcp-server-mode](mcp-server-mode.md) as the interop wave.
> **Roadmap source:** 2026-07-06 long-range planning pass. The Agent
> Client Protocol (Zed, 2025; adopted by neovim plugins and JetBrains
> experiments) is doing to agent↔editor what LSP did to language↔editor.
> evva already hand-writes JSON-RPC for LSP — the transport skill exists
> in-repo; speaking ACP makes every ACP editor a free evva frontend,
> which is the purest expression of "one runtime, swappable UI".
> **Reference source:** the ACP spec (agentclientprotocol.com) + Zed's
> reference agents. No `ref/src` analogue.

---

## 1. TL;DR

evva's architecture was built for exactly this: `pkg/ui` defines a narrow
`UI`/`Controller` contract, the agent fans events to any sink, and nothing
in the runtime assumes a terminal. ACP is a JSON-RPC-over-stdio protocol
where an *editor* (client) launches an *agent* (server) and renders its
sessions natively — streamed assistant text, tool-call cards, permission
prompts, file diffs — inside the editor's own UI.

This wave ships `evva acp`: a subcommand that speaks ACP on stdio and
bridges it to a `pkg/agent` instance. A Zed user adds three lines of
config and gets evva — with its five providers, personas, permission
gate, and swarm-adjacent tools — as a first-class in-editor agent. The
editor renders; evva thinks. No TUI process, no terminal.

The implementation is deliberately a **fourth UI implementation** (after
bubbletea, lp, and the swarm web console): if ACP forces changes inside
`internal/agent`, that's an architecture smell to fix, not to code around
— this wave doubles as the proof that the UI seam actually holds.

## 2. Goals / non-goals

### Goals

- `evva acp` subcommand: ACP initialize/session lifecycle over stdio;
  hand-written JSON-RPC reusing the LSP transport code's idioms (no new
  deps — same policy that built the LSP client).
- Session bridge: ACP session ↔ `pkg/agent` session; streamed content
  mapped from evva's event stream; tool calls surfaced as ACP tool-call
  updates with status transitions.
- Permission mapping: evva's permission broker prompts flow as ACP
  permission requests (the protocol has native support); editor buttons
  answer the broker.
- Editor-native file ops: where the client advertises fs capabilities,
  route `read`/`edit` results through ACP so unsaved editor buffers are
  seen and edits appear as proposed diffs in-editor; fall back to direct
  fs when the client doesn't support it.
- Persona selection via ACP session parameters (map `/profile` to the
  protocol's mode/agent selection surface, per spec version).
- Ship a documented Zed config example + smoke-test script.

### Non-goals (this wave)

- Building editor plugins (Zed/neovim already speak the client side; we
  ship the agent side only).
- ACP *client* mode (evva driving another agent) — conceivable later via
  the same types, not now.
- Swarm control from the editor (the web console owns swarm ops; an ACP
  session may still *be* a persona that talks to a swarm as usual).
- Parity with every TUI overlay (`/context`, `/cost` render as text
  responses initially).

## 3. Design sketch

- **Process model:** editor spawns `evva acp` per workspace; one process
  may host multiple ACP sessions (map to independent `pkg/agent`
  instances, mirroring how the TUI would run multiple sessions
  sequentially — concurrency cap from config).
- **Event mapping table** (the heart of the wave): evva event kinds →
  ACP update types; tool families → ACP tool-kind hints (edit/read/
  execute/search) so editors render appropriate cards. The audit pass
  should pin this against both the current `pkg/event` vocabulary and the
  current ACP schema revision, and note protocol-version negotiation.
- **Permission fidelity:** ACP's allow/deny/allow-always options map to
  the broker's rule store — "always" persists a rule exactly as the TUI
  flow would; no separate permission state.
- **Capability degradation:** every editor-side capability (fs, terminal
  embedding) has a direct-execution fallback; the bridge advertises evva
  capabilities honestly on initialize.

## 4. Work items

- **ACP-1 — Protocol layer.** Types + stdio JSON-RPC framing +
  version negotiation, following the in-repo LSP transport idioms.
  *Accept:* golden-file conformance tests against recorded spec-example
  frames.
- **ACP-2 — `evva acp` + session bridge.** Command, agent lifecycle,
  prompt turn loop, cancellation. *Accept:* a scripted ACP client (test
  harness) completes a full turn incl. streamed chunks against a fake
  LLM.
- **ACP-3 — Tool-call surfacing.** Event-mapping table, status
  transitions, diff payloads for edits. *Accept:* an `edit` tool run
  produces an ACP tool-call card with a rendered diff in the harness.
- **ACP-4 — Permission bridge.** Broker ↔ ACP requests incl. persistent
  "always" rules. *Accept:* deny/allow/always each round-trip and the
  rule store reflects "always" identically to the TUI path.
- **ACP-5 — Editor fs delegation.** Client-capability probe, buffer-aware
  read/edit routing, fallback. *Accept:* with a harness advertising fs
  support, reads reflect unsaved buffer content; without it, direct fs
  is used.
- **ACP-6 — Docs + example.** User-guide (en + zh-tw): Zed setup, persona
  selection, capability matrix, troubleshooting. Smoke script under
  `examples/`.

## 5. Risks

| Risk | Mitigation |
|---|---|
| ACP spec is young and moving | version negotiation + a pinned supported-revision note in docs; conformance tests are golden-file based so bumps are mechanical |
| Impedance mismatch exposes UI-seam leaks in `internal/agent` | treat as findings, fix the seam (this is half the wave's value); escalate anything breaking to the v2.0 budget rule |
| Permission UX differs subtly from TUI expectations | broker is the single source of truth; only rendering differs |
| Editor fs delegation and LSP/self-healing edits interact oddly (who sees the buffer?) | audit maps the interaction; worst case: fs delegation ships behind a flag first |

## 6. Open questions

1. Which ACP revision to target at pickup time (re-check then; the spec
   moved fast through 2025-26).
2. One process per workspace vs per session — does Zed's spawning model
   make this moot?
3. Should `evva acp` sessions register in the session catalog (W7) so
   editor sessions are resumable from the TUI? (Delightful if cheap —
   leaning yes.)
