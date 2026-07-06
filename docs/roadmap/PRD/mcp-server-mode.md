# PRD — MCP Server Mode (evva as a callable MCP server) — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed.
> **Target release:** TBD — wave-sized minor (`v1.11+` candidate). Per the
> checkpoint-rewind precedent, the CLAUDE.md wave → minor row is added only
> when the operator confirms the wave.
> **Roadmap source:** 2026-07-06 web research pass (commissioned alongside a
> roadmap-overview audit) — the MCP ecosystem has grown into a two-way street
> (a centralized MCP Registry, 200+ published servers, most major dev tools
> ship one) and evva only ever walks it in one direction.
> **Evaluation provenance:** live-source audit at `dev@ef84887`
> (v1.10.0-beta.1), 2026-07-06. All file:line references verified against
> that commit.
> **Reference source:** none — `ref/src` is also an MCP *client* only, so
> there's no Claude Code analogue to port. Evva-native, motivated by the
> broader MCP/A2A ecosystem rather than a ref port.

---

## 1. TL;DR

Evva has shipped a solid MCP **client** (`pkg/mcp`, v1.3.0) — it connects out
to other people's servers and turns their tools into evva `tools.Tool`s. It
has never gone the other direction. As of early 2026 the MCP ecosystem
matured into genuinely bidirectional infrastructure: the November 2025
registry revision catalogs 3,000+ publicly registered servers, ~200+ concrete
server implementations exist across GitHub/Slack/Notion/Jira/Postgres/etc.,
and in December 2025 Anthropic, Block, and OpenAI jointly formed the Agentic
AI Foundation under the Linux Foundation to steward MCP alongside Google's
sibling Agent2Agent (A2A) protocol — "MCP defines how agents use tools, A2A
defines how agents collaborate with each other." evva ships neither half of
that second direction today.

This PRD adds **MCP server mode**: `evva mcp-serve` exposes a running evva
installation — specific tools, or (the more interesting case) a whole
persona invoked end-to-end — as an MCP server that any MCP client (Claude
Desktop, another IDE, another evva instance, an A2A-aware orchestrator) can
call into. This is a **new "swappable UI" axis** in the same sense
`docs/architecture.md`'s vision statement already claims for the TUI and the
swarm web console — except here the "UI" consuming evva's observable runtime
is another agent, not a human.

The good news: **zero new dependencies.** `github.com/modelcontextprotocol/
go-sdk@v1.6.1` — already vendored for the client — ships a complete server
implementation (`mcp/server.go`, `mcp/streamable.go`) that evva has simply
never called. This is wiring, not a new SDK integration.

---

## 2. Goals / non-goals

### Goals

- `evva mcp-serve` starts evva as an MCP server over **stdio** (the "Claude
  Desktop launches me as a subprocess" shape) or **streamable HTTP** (a
  persistent, remote-embeddable server).
- Two things can be exposed, explicit opt-in only, nothing by default:
  1. **A whole persona, invoked headlessly.** `evva_explore(prompt)` runs a
     full agent loop (reusing the existing headless path, `runCLI`,
     `cmd/evva/main.go:179`) and returns the final answer as one MCP tool
     call — "subagent as a service." This is the primary, higher-value case.
  2. **A single named tool**, thin passthrough — for callers that want one
     specific capability (e.g. `repo_map`) without a whole persona.
- Reuse the existing `tools.Tool` interface end to end — the server-side
  adapter is the structural mirror of the client-side one that already
  ships (§3.3).
- An explicit allowlist (`mcpServe` in `settings.json`) governs what's
  exposed; unknown names are rejected at startup, not at call time.

### Non-goals (this wave)

- **Full A2A protocol support.** MCP and A2A are complementary but distinct
  (tool-calling vs. agent-collaboration); this PRD only does the MCP-server
  half. A2A is a plausible future PRD once evva has a concrete
  agent-to-agent collaboration need beyond what swarm already does
  internally.
- **Raw dangerous-tool passthrough** (`bash`, `edit`, `write`) exposed
  directly to an external caller. v1 only exposes read-oriented tools and
  whole-persona calls — a persona's own permission mode already gates what
  it does *internally*; handing an external, untrusted MCP caller a raw
  `bash` tool is a different trust boundary and is deliberately deferred
  (§6, §7).
- **SSE transport.** Mirrors the client-side decision already on record
  (`docs/roadmap/v1/v1-3-mcp.md` §6) — stdio + streamable HTTP only.
- **Exposing swarm-internal task-ledger tools** externally. A swarm member
  calling out is one thing; a stranger dispatching tasks into someone's
  swarm via MCP is a separate, much larger permission question — out of
  scope.
- **Streaming partial results mid-call.** v1 is synchronous request/response
  (block until the persona/tool call finishes, bounded by a timeout).
  Streaming is a plausible fast-follow once `StreamableHTTPOptions`' event
  store is actually needed.

---

## 3. Verified current state

### 3.1 The client architecture this mirrors

- `Manager` (`pkg/mcp/manager.go:18`) holds `map[string]*Client`; built via
  `NewManager(opts OpenOptions)` (`:54`) or the one-call `Open(ctx, cfg
  *Config, opts OpenOptions) (*Manager, []Warning)` (`:71`), which connects
  every configured server in parallel and fans out `ListTools`.
  `RegisterFactories(reg *pubtoolset.Registry)` (`:218`) registers a
  `tools.Tool` factory per discovered remote tool as
  `mcp__<server>__<tool>`; `DiscoveredToolNames()` (`:190`) feeds the
  agent's deferred-tool allowlist; `Shutdown()` (`:258`) closes all
  sessions.
- `Client` (`pkg/mcp/client.go:22`) wraps one `*mcpsdk.ClientSession`;
  `connect` (`:41`) builds a transport (stdio via `buildStdioTransport`,
  HTTP via `buildStreamableHTTPTransport`, optionally OAuth-wrapped), then
  `mcpsdk.NewClient(&mcpsdk.Implementation{Name:"evva",Version:"1.6.0"},
  ...)` + `sdkClient.Connect`. `ListTools`/`CallTool` (`:105,125`) do the
  runtime calls; `isSessionExpired`/`isAuthError` (`:183,199`) handle
  reconnect and 401/403.
- Config: `ServerConfig`/`TransportType` (stdio/http only) —
  `pkg/mcp/types.go:11-35`; `Load(workdir, evvaHome)` (`pkg/mcp/config.go:60`)
  merges project + user `settings.json` `mcpServers` blocks. **Server mode's
  `mcpServe` block is the structural inverse of this.**
- Results normalize via `ConvertResult(res, serverName, toolName, evvaHome)`
  (referenced `client.go:263`, implemented `pkg/mcp/result.go`).
- `pkg/mcp/doc.go` marks the package **Experimental** (stabilization
  candidate v1.7+ per `docs/contributing/sdk-stability.md`) — server mode
  joins an already-experimental surface; see §8 on the stability-tier
  question.

### 3.2 The vendored SDK already has everything server-side needs

All in `github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp/` — no new
dependency, per CLAUDE.md's "minimize external dependencies" convention:

- `func NewServer(impl *Implementation, options *ServerOptions) *Server`
  (`server.go:157`).
- `func (s *Server) AddTool(t *Tool, h ToolHandler)` (`server.go:238`),
  `type ToolHandler func(context.Context, *CallToolRequest)
  (*CallToolResult, error)` (`tool.go:30`) — or the generic ergonomic form
  `func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])`
  (`server.go:503`) with automatic schema inference from `In`/`Out`
  (`tool.go:57`) — the persona adapter (§5.2) uses this form.
- `func (s *Server) Run(ctx context.Context, t Transport) error`
  (`server.go:946`) for one-shot-per-call serving, or `Connect(ctx, t
  Transport, opts *ServerSessionOptions) (*ServerSession, error)`
  (`:1020`) for a longer-lived session.
- Transports: `StdioTransport` (`transport.go:101`) and
  `NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts
  *StreamableHTTPOptions) *StreamableHTTPHandler` (`streamable.go:194`) — a
  plain `http.Handler` you mount, with `Stateless`, `JSONResponse`,
  `EventStore` (resumption), `SessionTimeout` (`streamable.go:127`). An
  SSE-only handler also exists (`sse.go:83`) but is out of scope (§2).

### 3.3 The adapter to mirror

- `tools.Tool` interface: `pkg/tools/tool.go:18` — `Name() string`,
  `Description() string`, `Schema() json.RawMessage`, `Execute(ctx,
  *slog.Logger, json.RawMessage) (tools.Result, error)`.
- The existing **remote → evva** adapter is `mcpToolImpl`
  (`pkg/mcp/client.go:240-264`), built by `newMcpTool(c *Client, sdkTool
  *mcpsdk.Tool) tools.Tool` (`:224`): `Execute` (`:252`) unmarshals the
  incoming `json.RawMessage` into `map[string]any`, calls
  `t.client.CallTool`, normalizes the response via `ConvertResult`.
- Server mode needs the **evva → MCP** mirror: wrap an evva `tools.Tool` (or
  a persona invocation) as an `*mcpsdk.Tool` + `ToolHandler`, decoding
  `CallToolRequest.Params.Arguments` and re-encoding a `tools.Result` as
  `*mcpsdk.CallToolResult`. Structurally the same shape, opposite direction
  — no new pattern invented.

### 3.4 The headless entrypoint this reuses for personas

- `cmd/evva/main.go` dispatches on `os.Args[1]` before any TUI/agent
  bootstrap — `case "service": runService(...)`, `case "swarm":
  runSwarm(...)` (`main.go:70-79`). `evva mcp-serve` follows the identical
  pattern.
- `runCLI(ctx, acfg)` (`main.go:179`) is evva's existing headless one-shot
  path: builds `agent.New` with a `cliSink`, runs one turn, streams/collects
  the result. The persona adapter (§5.2) is a thin wrapper around this same
  construction, not a new agent-bootstrap path.
- `cmd/evva/service.go`'s `runService` (line 55) is the closer analogue for
  the *persistent* HTTP-serving form (`start`/`stop`/`status`) — `mcp-serve
  --transport http` should follow its daemon-lifecycle shape rather than
  `runCLI`'s one-shot shape.
- `docs/roadmap/PRD/structured-output-tool.md` (§1, line ~7) already frames
  itself as completing "the headless/SDK story" and cites `runCLI` as the
  precedent. MCP server mode is a sibling half of that same story: both are
  about evva being callable *without* its own CLI/TUI in front.

---

## 4. Design

### 4.1 Package placement

New files inside the existing `pkg/mcp` package (not a new top-level
package) — it's still "the MCP integration surface," just the inbound half,
and it can share `types.go`/`result.go` helpers directly:

- `pkg/mcp/serve.go` — `ServeConfig{Transport, Addr, Expose []ExposeSpec}`,
  `ExposeSpec{Kind: "tool"|"persona", Name string}`.
- `pkg/mcp/serve_adapter.go` — `adaptTool(t tools.Tool) (*mcpsdk.Tool,
  mcpsdk.ToolHandler)`, the mirror of `newMcpTool`/`mcpToolImpl`
  (`client.go:224-264`), plus `resultToMCP(tools.Result)
  *mcpsdk.CallToolResult` (the mirror of `ConvertResult`).
- `pkg/mcp/serve_persona.go` — `adaptPersona(spawner PersonaSpawner, name
  string) (*mcpsdk.Tool, mcpsdk.ToolHandlerFor[PersonaArgs, PersonaResult])`
  using the generic `AddTool[In, Out]` form (§3.2) so the schema
  (`{prompt: string}` in, `{answer: string}` out) is inferred, not
  hand-written.

### 4.2 The persona call path

`PersonaArgs{Prompt string}` → the handler builds an `agent.New` exactly as
`runCLI` does (§3.4) — fresh session per call in v1 (no cross-call state;
resumable sessions are an open question, §7) — runs it to completion, and
returns `PersonaResult{Answer string}`. The incoming `Prompt` is treated as
**untrusted content**, following the existing `RP-21` convention for
externally-sourced text reaching an agent (`<untrusted-content>` wrapping) —
an external MCP caller is a strictly lower trust level than the operator
typing into the TUI, and the persona's own permission mode (not "trust
because it came in as a tool call") is what still gates any dangerous action
it takes internally.

### 4.3 CLI + transports

- `cmd/evva/mcpserve.go` (new, mirrors `service.go`'s shape): `evva
  mcp-serve --transport stdio|http --addr 127.0.0.1:PORT --config <path>`.
- **stdio**: `server.Run(ctx, &mcpsdk.StdioTransport{})` — the Claude
  Desktop / "launch me as a subprocess" case.
- **streamable http**: `mcpsdk.NewStreamableHTTPHandler(...)` mounted as a
  plain `http.Handler`, same mounting shape `internal/swarm/service`
  already uses for its own webapi — so auth follows the identical bearer-
  token pattern RP-15 established for the swarm webapi (minted token,
  loopback-only unless explicitly opted into remote).

### 4.4 Exposure allowlist

New `mcpServe` block in `settings.json`, the structural inverse of the
existing `mcpServers` client config (`pkg/mcp/config.go:60`):

```jsonc
"mcpServe": {
  "expose": [
    {"kind": "persona", "name": "explore"},
    {"kind": "tool", "name": "repo_map"}
  ]
}
```

Empty/absent = feature dormant, nothing exposed, `mcp-serve` refuses to
start with a clear "nothing configured to expose" error rather than silently
listening with an empty tool list. Unknown tool/persona names in the list
fail at startup (fast, loud), not at first call.

---

## 5. Work items

**MCP-1 — Server-side tool adapter.**
`serve_adapter.go`: `adaptTool` + `resultToMCP`. *Accept:* an in-process
`mcpsdk` test client calling a wrapped evva tool (e.g. `repo_map`) through
`AddTool`/`server.Run` round-trips correctly, including the error path
(`tools.Result` error → `CallToolResult.IsError`).

**MCP-2 — Persona adapter.**
`serve_persona.go`: `adaptPersona`, `PersonaArgs`/`PersonaResult`, fresh
session per call, untrusted-content wrapping on the incoming prompt (§4.2).
*Accept:* an MCP call to `evva_explore` with a prompt runs a real headless
agent loop and returns its final answer; a call while the loop is mid-flight
on a second concurrent call doesn't corrupt state (each call gets its own
`agent.New`).

**MCP-3 — `mcp-serve` subcommand + stdio + allowlist.**
`cmd/evva/mcpserve.go`, `mcpServe` settings schema + validation (§4.4),
stdio transport wired end to end. *Accept:* `evva mcp-serve --transport
stdio` launched as a subprocess (e.g. from a small Go test harness acting as
an MCP client) lists exactly the configured tools/personas and can call one;
nothing-configured refuses to start; an unknown name in `expose` fails at
startup.

**MCP-4 — Streamable HTTP transport + auth.**
`NewStreamableHTTPHandler` mounted, bearer-token auth reusing the RP-15
pattern, `--transport http --addr`. *Accept:* a remote MCP client can
connect over HTTP, auth is enforced (401 without a token), loopback-only by
default matching the swarm webapi's existing default.

**MCP-5 — Docs + version + changelog.**
`docs/contributing/extending.md` gains the server-mode section alongside
its existing client-only section (§655-771 today); user-guide "Using evva
from Claude Desktop / another MCP client" (en + zh-tw); `CHANGELOG.md`;
`pkg/version/version.go`. Flag the `pkg/mcp` stability-tier question (§8)
for the operator rather than deciding it unilaterally.

Sequencing: `MCP-1 → MCP-2 → MCP-3 → MCP-4 → MCP-5` (HTTP/auth is the only
piece with real new surface area; the adapters are mechanical mirrors of
code that already shipped and passed review).

---

## 6. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Trust-boundary inversion — the client side already classifies "should I trust a remote server's tool"; server mode is the mirror question, "should I let a remote caller invoke my tools" | Nothing exposed by default; persona-only (not raw dangerous tools) in v1; incoming prompts wrapped as untrusted content (§4.2, RP-21 precedent) so the persona's own permission mode is still the real gate |
| A long-running persona call doesn't fit MCP's typical fast call/response expectation | v1 is synchronous with a documented bounded timeout; streaming/resumption via `StreamableHTTPOptions.EventStore` is a scoped-out fast-follow, not this wave |
| `pkg/mcp` is already "Experimental" — this adds a second experimental surface to one package | Don't bundle a stability-tier promotion into this PRD; that's `docs/contributing/sdk-stability.md`'s call, made separately once both directions have soaked |
| Confusing MCP server mode with A2A (agent-to-agent) — different protocols, different problems | §2 non-goals draw the line explicitly; this PRD is MCP-only |
| Exposing the wrong thing by config typo | Startup-time validation against a known tool/persona registry, not call-time discovery (§4.4) |

---

## 7. Open questions

1. **Resumable persona sessions** (a second call continues the same
   conversation rather than always starting fresh)? Recommend defer — v1
   ships fresh-session-per-call; revisit once there's a real caller asking
   for continuity.
2. **Which personas ship exposable by default in examples** (e.g.
   `examples/full-host`)? Recommend none — the allowlist stays opt-in even
   in example configs, so the default posture is always "nothing exposed."
3. **Does server mode change `pkg/mcp`'s stability tier?** Recommend the
   operator decide at implementation time, not presume it here (§6).

---

## 8. Rollout

1. `MCP-1..5` via `feature/mcp-server-mode` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation.
3. Beta validation: connect a real external MCP client (e.g. Claude
   Desktop's `mcpServers` config pointed at `evva mcp-serve --transport
   stdio`) and confirm a persona call round-trips end to end; separately
   validate the HTTP transport with a bearer token from a non-loopback
   client rejected without one.
4. `release` promotes.
