# Phase MCP — Implementation Plan (ships as `v1.6.0`)

> **Audience:** senior engineers implementing this phase.
> **Status:** ready to build.
> **Filename:** `v1-6-mcp.md` (matches the release tag — the file was
> renamed from `v1-3-mcp.md` so contributors scanning the directory see
> the actual shipping order).
> **Target release:** `v1.6.0` (additive, minor bump under the Stable-tier
> promise — package surface is **Experimental** for the first minor, see §5).
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.3 — MCP client support*
> (CLAUDE.md keeps phase numbers stable; only release tags follow shipping
> order).
> **Sequencing:** the **phase identifier** stays "v1.3" in `CLAUDE.md` and
> in cross-references that talk about *what* is being built. The **release
> tag** is `v1.6.0` because shipping order moved v1.4 (bundled skills) and
> v1.2 (OpenAI provider) ahead of MCP — the chronology of tags is
> v1.0 → v1.1 → v1.4 → v1.5 → v1.6. When this doc says "v1.6" it means
> the release; when it says "this phase" or "Phase MCP" it means the body
> of work.

---

## 1. TL;DR — what this phase actually is

MCP (Model Context Protocol) is the headline net-new capability of the
post-v1.0 arc — it lets evva consume tools and resources from any
third-party MCP server (filesystem, GitHub, Slack, Notion, custom internal
servers) without per-server Go code changes. Today evva has **zero MCP
runtime** and **partial MCP awareness**:

- `pkg/tools/name.go` declares no `MCP*` constants; nothing in
  `internal/toolset/builtins.go` knows about MCP.
- `internal/agent/agent.go` has no MCP loader; `ToolState` has no MCP slot.
- BUT — the deferred-tool + `tool_search` machinery was **designed for MCP
  from day one** and contains two production-ready seams waiting for content:
  1. `internal/tools/meta/toolsearch.go:200-216` has an `mcp__server` prefix
     fast-path that returns every tool in a namespace.
  2. `internal/tools/meta/fuzzy.go:34-45` parses `mcp__<server>__<action>`
     names with the right split rules and applies an MCP-prefix scoring
     boost.
  Neither has any tools to surface today — both are commented "Unused
  today (evva has no MCP tools) but kept so Phase 13 doesn't reintroduce
  the logic."

This phase delivers, in order:

1. **A public `pkg/mcp` client package** wrapping the official
   `github.com/modelcontextprotocol/go-sdk` (Apache 2.0, v1.6.x stable
   as of May 2026). The wrapper:
   - Loads MCP server configs from `settings.json` (same file as hooks).
   - Connects to every configured server in parallel (timeout-bounded).
   - Surfaces per-server status: `connected`, `pending`, `failed`,
     `needs-auth`, `disabled`.
   - Holds the live `mcp.ClientSession` per server for the tool-call
     dispatch path.
   - Supports two transports: **stdio** (subprocess) and **Streamable
     HTTP** (the 2025-03-26 spec transport that replaces the older
     HTTP+SSE). The legacy SSE-only transport, WebSocket, SSE-IDE, SDK,
     and `claudeai-proxy` transports are out of scope (§6).
2. **Dynamic tool registration into the existing deferred channel.**
   Every discovered MCP tool becomes a `ToolName` of the form
   `mcp__<server>__<tool>` (lowercased + char-sanitized per ref's
   normalization), registered on `pubtoolset.DefaultRegistry()` against a
   factory that captures the right MCP session. The names land in the
   per-agent `deferredAllowlist` BEFORE the system prompt is computed, so
   they appear in the `<available-deferred-tools>` block on turn 1 and
   the model can locate them via `tool_search` (the `mcp__server` fast
   path + keyword search both work out of the box).
3. **Four meta tools ported from `ref/src/tools/`:**
   - `ListMcpResourcesTool` — `list_mcp_resources` (deferred)
   - `ReadMcpResourceTool` — `read_mcp_resource` (deferred)
   - `McpAuthTool` — `mcp__<server>__authenticate` (one per `needs-auth`
     server, registered dynamically)
   - The dynamic MCP tool itself (already covered by 2).
4. **OAuth flow** via the SDK's `auth.AuthorizationCodeHandler`.
   `pkg/mcp` defines a narrow `OAuthPromptFn` callback (no dependency
   on `internal/question`); the host adapter in `internal/agent` wires
   that callback to `question.Broker.Ask` so the user sees the auth
   URL via `ask_user_question` and confirms when they've finished
   in-browser. The SDK manages the local callback server, token
   refresh, and the auth-code capture from the redirect.
5. **Permission, hooks, and subagent integration** — all three compose
   for free because:
   - `permission.Store.Decide(toolName, ...)` already takes an arbitrary
     name string; `mcp__server__tool` slots in.
   - `pkg/hooks` PreToolUse/PostToolUse hooks receive `tool_name` as a
     string — MCP names flow through unchanged.
   - `spawn.go` already inherits the MCP manager pointer (via a new
     `WithMcpManager` option mirroring `WithHookRegistry` /
     `WithSkillRegistry`); subagents share the parent's live sessions
     instead of re-connecting.

**Why depend on the official Go SDK rather than hand-rolling JSON-RPC.**
The MCP spec is a moving target (2024-11-05 → 2025-03-26 → 2025-11-25
in 12 months). The protocol layer alone (JSON-RPC framing, session
management, SSE resumability, stdio newline-delimited messages, OAuth
authorization-code flow, session-id header handling, batch responses, 404
session-expired retry semantics) would dwarf the rest of this phase. The
official SDK has shipped v1.0 and is now at v1.6.x stable, is maintained
by the modelcontextprotocol org in collaboration with Google, has battle-
tested JSON-RPC internals borrowed from `gopls`, and tracks the spec.
Ref's TypeScript implementation depends on the official TS SDK for the
identical reason. Total transitive deps added (assessed via
`go mod why` against the current `go.mod`): the SDK module itself, plus
its small set of stdlib-adjacent helpers — no provider clients, no UI
toolkit, no analytics. **Vendor it; do not fork it.**

The first cut deliberately keeps the public surface narrow
(**Experimental** tier) and the scope tight: prompts, sampling, roots,
elicitation, plugin-provided servers, and SSE-IDE/WebSocket/SDK
transports are explicitly out (§6). v1.6 lands as "MCP works for real-
world stdio and HTTP servers with permission + hook composition" — every
ambitious refinement is a follow-up phase.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 evva's existing MCP-awareness (re-use, do not change)

| Site | What it does | Phase MCP dependency |
| --- | --- | --- |
| `internal/tools/meta/toolsearch.go:200-216` | `mcp__server` prefix fast path — `tool_search` queries like `mcp__filesystem` return every namespaced tool. | **Active code** (not commented out); the doc-comment "Unused today (evva has no MCP tools)" describes the runtime state — the `for _, d := range all` loop runs, just over an empty MCP slice. Re-used as-is once Task 3.4 populates the deferred allowlist with MCP names. The test at `toolsearch_test.go:240-256` already validates against synthesized `mcp__notion__search` / `mcp__github__list_repos` descriptors. **Action item for this phase:** remove the stale "Unused today" comment when Task 4.3 lands MCP names in `DeferredNames`, so future readers don't misinterpret "unused" as "dead". |
| `internal/tools/meta/fuzzy.go:34-45` | `parseToolName` splits MCP names on both `__` and `_` and applies a scoring boost (`scoreNamePartExactMcp = 12` vs `scoreNamePartExact = 10`). | Re-used as-is. |
| `internal/agent/sysprompt/main_agent.go:80-96` `mainDeferredToolsSection` | Renders `<available-deferred-tools>` as one tool name per line — accepts any string, no allowlist. | Re-used as-is. MCP names appear here automatically once they land in the profile's `DeferredTools`. |
| `internal/tools/meta/toolsearch.go:81-154` | `tool_search` returns matched tools' full JSONSchema in a `<functions>` block, fetched via `lookup.Describe(name)`. | Re-used as-is. The Describe path needs a new branch for MCP names (Task 4.2). |
| `internal/agent/agent.go:417 SetDeferredLookup(a)` | Installs `*Agent` as the lookup implementing `meta.DeferredLookup`. `DeferredNames()` reads from `a.deferredAllowlist`. | The allowlist gets populated with MCP names during profile build (Task 4.3). |
| `internal/agent/tools.go:27 ResolveTool` + `tools.go:86 MarkDiscovered` | Lazy-build a deferred tool via `toolset.Build(name, state)` and cache it in `a.active`. The build goes through `pubtoolset.DefaultRegistry().Build`. | MCP tools register dynamic factories on the same `DefaultRegistry` (Task 3.4). |
| `pkg/toolset/registry.go:44 Register` | Idempotent registration of name → factory. | Used by the MCP manager to register dynamic `mcp__*` factories at startup. Names are runtime-discovered so duplicates only happen on a config reload (see §5 "Hot-reload" — out of scope for this phase). |
| `internal/toolset/toolset.go:248 SkillRegistry / SetSkillRegistry` | Pattern for "one optional registry per ToolState, late-bound, nil-safe". | **The exact pattern Phase MCP mirrors** for `McpManager / SetMcpManager`. |
| `pkg/hooks/loader.go:Load(workdir, evvaHome)` | Loads `.evva/settings.json` (project) and `<evvaHome>/settings.json` (user). | The MCP loader piggybacks on these exact two files under a new `mcpServers` block — one file for all evva config. |
| `pkg/agent/options.go:152 WithHookRegistry` | SDK opt-in for hosts that build the registry themselves. | Phase MCP adds the parallel `WithMcpManager` option. |
| `internal/agent/spawn.go:80-87` | Subagent inheritance of `WithHookRegistry / WithPermissionStore / WithQuestionBroker`. | Add one line: `WithMcpManager(a.mcpManager)`. |
| `internal/agent/state_machine.go:359 permissionGateWithOverride` | The permission gate. Accepts an arbitrary `tools.Call.Name` string. | MCP tool names flow through unchanged — permission rules of the form `mcp__server__tool` work today (cross-check: `pkg/permission/rule.go` regex). |

### 2.2 Official MCP Go SDK (the dependency this phase adds)

| Symbol | Role |
| --- | --- |
| Module `github.com/modelcontextprotocol/go-sdk` (v1.6.x, Apache 2.0) | The whole protocol layer. Maintained by Google + the modelcontextprotocol org. |
| `mcp.NewClient(impl, options) *Client` | Constructs a logical client (one per server is the typical pattern). |
| `mcp.Client.Connect(ctx, transport, opts) (*ClientSession, error)` | Performs the JSON-RPC `initialize` handshake. |
| `mcp.CommandTransport{Command: *exec.Cmd}` | Stdio transport — spawns a subprocess and pipes JSON-RPC over stdin/stdout. |
| `mcp.StreamableClientTransport{Endpoint, OAuthHandler, ...}` | The 2025-03-26 Streamable HTTP transport (POST + optional SSE upgrade, `Mcp-Session-Id` header handling, resumability). |
| `ClientSession.ListTools(ctx, *ListToolsParams) (*ListToolsResult, error)` | Pulls the tool catalog. |
| `ClientSession.CallTool(ctx, *CallToolParams) (*CallToolResult, error)` | Invokes a tool. Supports per-call progress tokens. |
| `ClientSession.ListResources` / `ReadResource` / `ListResourceTemplates` | Resource APIs for `list_mcp_resources` / `read_mcp_resource`. |
| `ClientSession.Close() / Wait()` | Lifecycle. |
| `auth.AuthorizationCodeHandler` (in `auth` subpackage) | OAuth 2.1 authorization-code flow with PKCE; handles local callback server, token refresh. Plug into `StreamableClientTransport.OAuthHandler`. |
| `ClientOptions.{ProgressNotificationHandler, LoggingMessageHandler, CreateMessageHandler, ElicitationHandler, RootsListChangedHandler}` | Callbacks for server-initiated requests. Phase MCP wires only `ProgressNotificationHandler` (log only) and `LoggingMessageHandler` (forward to agent logger). Sampling / Elicitation / Roots are §6 out-of-scope. |

### 2.3 ref/ MCP code mapped to this phase

The ref implementation is **~14,200 lines** across `services/mcp/`,
`utils/mcp*.ts`, four tool packages, and the TUI panel — most of it is
product glue this phase doesn't need. The table below maps what's relevant:

| ref file | Role | Phase MCP disposition |
| --- | --- | --- |
| `services/mcp/types.ts` (258) | Zod schemas for every server type + Connected/Failed/NeedsAuth/Pending/Disabled status union. | **Port** the union + the stdio/HTTP config types. Drop SSE / SSE-IDE / WS / WS-IDE / SDK / claudeai-proxy / XAA schemas. |
| `services/mcp/normalization.ts` (23) | `normalizeNameForMCP` — char-sanitize server/tool names against `^[a-zA-Z0-9_-]{1,64}$`. | **Port verbatim** — tiny, correct, used everywhere. |
| `services/mcp/mcpStringUtils.ts` (106) | `mcpInfoFromString`, `getMcpPrefix`, `buildMcpToolName`, `getMcpDisplayName`. | **Port verbatim** — also tiny, also correct. |
| `services/mcp/envExpansion.ts` (38) | `${VAR}` and `${VAR:-default}` expansion in stdio config command/args/env. | **Port verbatim** — one regex. |
| `services/mcp/client.ts` (3348) | The bulk of ref's MCP: `connectToServer`, `fetchToolsForClient`, `fetchResourcesForClient`, `ensureConnectedClient`, `reconnectMcpServerImpl`, `callMCPToolWithUrlElicitationRetry`, dozens of cache/auth helpers. | **Re-implement in ~600 lines** of `pkg/mcp/client.go` + `manager.go`. The official Go SDK absorbs ~2500 LOC of ref's protocol layer; we only need the policy layer (connect orchestration, lifecycle, tool→Go-tool wrapping, retry-on-session-expired). |
| `services/mcp/config.ts` (1578) | Settings loading + writing for ref's seven config scopes (local/user/project/dynamic/enterprise/claudeai/managed), plus plugin servers and `.mcp.json` writing. | **Re-implement narrow**: two scopes (project = `.evva/settings.json`, user = `<APP_HOME>/settings.json`), no enterprise/managed/plugin paths, no writer (the model only reads; the user edits manually or via the `setup-hooks`-style "settings editor" skill). |
| `services/mcp/auth.ts` (2465) | OAuth client + token storage + step-up detection + ClaudeAuthProvider + cache. | **Delegate to the SDK's `auth.AuthorizationCodeHandler`.** v1.6 ships a thin Go shim (`pkg/mcp/oauth.go`) that surfaces the auth URL through a narrow `OAuthPromptFn` callback the host installs — the bundled `internal/agent` adapter routes it to `question.Broker.Ask` ("Open this URL, then click 'I'm done'"). Token disk persistence is out of scope for the first cut (§6). |
| `tools/MCPTool/MCPTool.ts` (77) | Template tool object; ref's `fetchToolsForClient` clones it and overrides `name`, `call`, `description`. | **No direct port.** Each MCP tool in evva is its own `tools.Tool` value built by the dynamic factory in Task 3.4. |
| `tools/MCPTool/classifyForCollapse.ts` (604) | UI-only — classifies long results as "search/read" for transcript collapse. | **Skip.** UI concern; the TUI can opt into a future port. |
| `tools/McpAuthTool/McpAuthTool.ts` (215) | Pseudo-tool surfaced for `needs-auth` servers; calls back into auth + reconnect. | **Port** (~120 lines in Go), simplified — no in-browser auto-open, no XAA. |
| `tools/ListMcpResourcesTool/*` (123 + 20) | `list_mcp_resources` deferred tool. | **Port** (~80 lines). |
| `tools/ReadMcpResourceTool/*` (158 + 16) | `read_mcp_resource` deferred tool, with binary blob persistence. | **Port simpler**: text-content directly; binary blobs saved via a single `os.WriteFile` under `<APP_HOME>/mcp-blobs/`, path returned to the model. No `maybeResizeAndDownsampleImageBuffer`. |
| `utils/mcpValidation.ts` (208) | Result-size truncation + content-type validation. | **Port the truncation policy** (default 100k chars per result, like ref); skip the rest. |
| `utils/mcpOutputStorage.ts` (189) | Binary blob persistence helpers. | **Reuse minimal shape:** one helper that writes bytes to a path under `<APP_HOME>/mcp-blobs/`, returns `(path, size, error)`. Drop the mime-derived filename logic in this phase; use a `crypto/rand` filename (see §3.5). |
| `utils/mcpWebSocketTransport.ts` (200) | Custom WebSocket transport. | **Skip** — not in current MCP spec; only Anthropic IDE/dev use case. |
| `utils/mcpInstructionsDelta.ts` (130) | Adds server-supplied "instructions" to the system prompt. | **Skip.** A future minor may add when usage data shows servers actually ship useful `instructions`. |
| `services/mcp/MCPConnectionManager.tsx` (full file size unread, .tsx) | React hooks for the TUI's `/mcp` panel. | **Skip.** TUI concern; not in scope for this phase. |
| `services/mcp/{claudeai,xaa,xaaIdpLogin,vscodeSdkMcp,InProcessTransport,SdkControlTransport}.ts` | Anthropic-specific (claude.ai connectors, XAA, VSCode IDE integration). | **Skip — all out of scope.** |
| `commands/mcp/mcp.tsx`, `cli/handlers/mcp.tsx`, `entrypoints/mcp.ts` | TUI panel + CLI subcommand for listing/managing servers. | **Skip.** The user edits settings.json directly; the `setup-hooks` v1.4 skill is the documented model-driven authoring path (a sibling `setup-mcp` skill could ship later). |
| `migrations/migrateEnableAllProjectMcpServersToSettings.ts` | One-time settings migration. | **Skip** — evva never had the old shape. |

The 14k → ~1.2k port ratio is driven entirely by Go-SDK substitution and
scope tightening — no shortcut on correctness.

---

## 3. Goal & acceptance criteria

**Goal:** a user configures one or more MCP servers in
`.evva/settings.json` (or the global `<APP_HOME>/settings.json`); on
next session start, every discovered tool appears in the
`<available-deferred-tools>` block as `mcp__<server>__<tool>`, can be
loaded with `tool_search`, runs against the live MCP server through the
permission gate + hook engine, and returns results the model can act
on. Server status (connected / failed / needs-auth) is visible in the
agent logger; resource list/read works for servers that expose
resources; HTTP servers requiring OAuth trigger an interactive auth
prompt to the user.

Ship is complete when **all** of these pass:

- **A1 — Stdio server discovery.** Configure
  `@modelcontextprotocol/server-filesystem` (or any reference stdio
  server) under `mcpServers`; on agent start, the manager spawns the
  subprocess, completes `initialize`, calls `tools/list`, registers
  every returned tool under `mcp__<server>__<tool>` on
  `pubtoolset.DefaultRegistry()`, and appends those names to the Main
  profile's `DeferredTools` slice.
- **A2 — Tools appear in the prompt.** The rendered system prompt's
  `<available-deferred-tools>` block lists every discovered MCP tool by
  name, with no schemas inlined.
- **A3 — `tool_search` finds them.** `tool_search` with
  `query: "select:mcp__filesystem__read_file"` returns the tool's
  description + JSON schema in the `<functions>` block; keyword search
  (`query: "filesystem read"`) ranks the same tool highly via the MCP
  prefix boost in `fuzzy.go`.
- **A4 — Tool invocation round-trips.** The model invokes
  `mcp__filesystem__read_file` with valid args; the manager's
  `CallTool` reaches the server; the result content lands as a
  `tools.Result.Content` string the model receives next turn.
  Multimodal results (text + image blocks) round-trip correctly:
  text becomes `Content`, images become `ContentBlocks` (using
  `tools.NewImageResult` / `ContentBlockImage`).
- **A5 — Streamable HTTP transport.** Same A1–A4 against a Streamable
  HTTP MCP server (e.g. an Express-based test fixture). Test fixture
  ships under `internal/mcp/testdata/`.
- **A6 — Permission gate.** A `mcp__filesystem__write_file` call
  triggers the permission gate's `BehaviorAsk` (default for unknown
  tools); approving once allows the call; adding a permission rule
  `mcp__filesystem__write_file` to `<APP_HOME>/permission-rules.json`
  short-circuits future asks. Deny rules block.
- **A7 — Hook composition.** A `PreToolUse` hook with
  `matcher: "mcp__filesystem__*"` fires before MCP tool calls and can
  block, mutate `updatedInput`, or override the permission decision —
  identical contract to built-in tools.
- **A8 — Resources.** Configure a server that exposes resources
  (`@modelcontextprotocol/server-filesystem` does); `list_mcp_resources`
  returns the resource catalog with `server` field set; `read_mcp_resource`
  returns text content directly. Binary blobs are persisted to
  `<APP_HOME>/mcp-blobs/<uuid>` and the path is returned to the model.
- **A9 — OAuth flow (HTTP servers).** Configure an HTTP server that
  responds 401 on first connect; the manager flags status `needs-auth`;
  a dynamically-registered `mcp__<server>__authenticate` tool appears
  in the deferred catalog; invoking it triggers
  `auth.AuthorizationCodeHandler`, which calls into the installed
  `mcp.OAuthPromptFn` carrying the auth URL; the host adapter
  (`internal/agent/mcp_wiring.go:mcpPromptViaQuestion`) forwards that
  to `question.Broker.Ask`; when the user confirms (after completing
  the in-browser flow), the manager reconnects, swaps in the real
  tool factories, and the model's next turn sees the real toolset.
- **A10 — Failure tolerance.** A misconfigured server (bad command,
  unreachable URL, missing env var) does NOT block agent startup. The
  manager logs the failure, the server appears with status `failed` in
  `Manager.Status()`, and the agent runs to completion using whatever
  servers DID connect. Other configured servers connect concurrently
  (no head-of-line blocking).
- **A11 — Reconnect on session-expired.** When a tool call returns
  HTTP 404 with JSON-RPC error code -32001 (per spec, MCP session
  expired), the manager invalidates the cached session, re-runs
  `initialize`, and retries the call **once**. A second failure surfaces
  as a tool error.
- **A12 — Subagent inheritance.** A spawned subagent (Explore / General
  / Plan) shares the parent's `*mcp.Manager` — MCP tools are reachable
  from the subagent via the same `mcp__<server>__<tool>` names without
  re-connecting. (Subagent prompts continue NOT to advertise the MCP
  catalog by default — they're deferred, and the parent's prompt is the
  one the model sees for the main loop. A subagent only sees MCP names
  if its own profile's `DeferredTools` is populated with them, which is
  Task 5 sub-bullet — opt-in per profile.)
- **A13 — Disabled servers.** A server entry with `"disabled": true`
  appears in `Manager.Status()` as `disabled` and contributes zero
  tools; no subprocess is spawned and no HTTP request is sent.
- **A14 — Zero-cost when unused.** Booting evva with no `mcpServers`
  block (or an empty block) does NOT import the SDK lazily, but the
  added compile-time dependency is fine (the SDK is small — see the
  dependency audit in Task 0 for the exact transitive graph). The
  runtime cost is one no-op `mcp.Load(workdir, evvaHome) →
  (*Manager{empty}, nil)` call at startup, and zero subprocess/HTTP
  activity thereafter.
- **A15 — Tests + version.** `go test ./...` green. New tests cover
  config parsing, env expansion, name normalization, tool wrapping,
  resource read (text + blob), reconnect-on-session-expired, permission
  composition, hook composition, subagent inheritance, OAuth happy-path
  (with a stub server), and **`TestErrorMatchers_PinSDKShape`** (the
  canary against SDK error-format drift — §7).
  `pkg/version.Version` bumped to `"1.6.0"`. CHANGELOG entry +
  `docs/sdk-stability.md` row for `pkg/mcp` at **Experimental** tier.
- **A16 — `/profile` switch preserves MCP tools.** Connect a stub MCP
  server, then call `Agent.SwitchProfile` to a different main-tier
  persona. The new profile's `DeferredNames()` still includes every
  `mcp__<server>__<tool>` name; `tool_search` continues to resolve
  their schemas; an `Execute` against the same MCP factory works
  without re-connecting (the `*mcp.Manager` lives on `ToolState`,
  which survives the profile swap). Pin with
  `internal/agent/mcp_switch_profile_test.go` (Task 7).

---

## 4. Work breakdown (ordered)

The phase is large; tasks are sized to be doable independently and
mergeable as separate commits.

### Task 0 — Add the SDK dependency

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.6.x
go mod tidy
```

Pin to the latest **patch** release of `v1.6` at PR time. Audit the
resulting `go.sum` additions — every new module entry should be either
(a) the SDK itself, or (b) a `golang.org/x/...` helper the SDK already
required transitively. If anything else lands, investigate why and
document in the PR.

**No third-party deps added beyond the SDK.** In particular: `pkg/mcp`
does NOT depend on `github.com/google/uuid` or any other UUID library.
The blob-persistence path in `result.go` uses `crypto/rand` +
`encoding/hex` (both stdlib) to generate a 16-byte filename — see the
`randomBlobName` helper in §3.5. `crypto/rand` is already a transitive
dependency (the SDK uses it for OAuth PKCE), so this adds zero new
modules to `go.sum`. If a future bundled tool genuinely needs proper
UUIDv4, the same minimal helper extends to that case; we re-evaluate
pulling in `google/uuid` when that case actually arises.

The first PR commit message should record the SDK's commit SHA and any
known-issue links from
`https://github.com/modelcontextprotocol/go-sdk/issues` that this phase will
work around (current candidates: #579 HTTP error verbosity, #591 OAuth
handler proposal — both Experimental SDK behaviors we may need to wrap).

### Task 1 — Create `pkg/mcp` package skeleton

New public package — sibling of `pkg/hooks`, `pkg/skill`. Stable-candidate
surface; ships as **Experimental** in v1.6.

**Layout:**

```
pkg/mcp/
├── doc.go                   # package overview
├── types.go                 # ServerConfig, ServerStatus, ToolMeta, ResourceMeta
├── config.go                # Load(workdir, evvaHome) (*Config, []Warning)
├── normalize.go             # normalize tool/server names (port from ref normalization.ts)
├── envexpand.go             # ${VAR} / ${VAR:-default} expansion (port from envExpansion.ts)
├── stringutils.go           # mcp__<server>__<tool> builders/parsers (port from mcpStringUtils.ts)
├── manager.go               # Manager — holds all sessions, runs connects in parallel, exposes status
├── client.go                # internal wrappers around mcp.ClientSession (tool call, resource list/read, reconnect)
├── transport.go             # buildStdioTransport, buildStreamableHTTPTransport
├── oauth.go                 # SDK auth.AuthorizationCodeHandler shim + OAuthPrompt / OAuthPromptFn types (no internal/question dep)
├── result.go                # mcp.CallToolResult → tools.Result conversion (text/image blocks, blob persistence, truncation)
├── *_test.go                # unit tests next to each file
└── testdata/
    ├── settings.json
    ├── stdio-echo-server.go # tiny in-process server for integration tests
    └── http-echo-server.go  # ...
```

**`doc.go`** sets the package contract:

```go
// Package mcp implements evva's Model Context Protocol client. It loads
// MCP server configurations from settings.json, connects to each
// configured server via the official modelcontextprotocol/go-sdk, and
// surfaces discovered tools and resources so the agent can register them
// dynamically into the deferred-tool channel.
//
// The package is Experimental — public types may change in a minor
// version (see docs/sdk-stability.md). Stabilization candidate for v1.7
// or later once downstream consumers have exercised the surface.
//
// Architectural seam:
//
//   - The host calls mcp.Load(workdir, evvaHome) once at boot to build a
//     *Manager. The Manager opens connections concurrently and is safe
//     to call before any agent exists.
//   - The host passes the *Manager into agent.New via WithMcpManager;
//     internal/agent installs it on the per-agent ToolState and registers
//     a dynamic factory per discovered tool on pubtoolset.DefaultRegistry.
//   - When the model invokes mcp__server__tool, agent.ResolveTool builds
//     the tool through the dynamic factory, which captures the Manager's
//     session for that server.
//
// Subagents inherit the parent's *Manager — no re-connection, no
// session duplication. The Manager is the single source of truth for
// every MCP interaction in the agent tree.
//
// Transports supported: stdio (subprocess) and Streamable HTTP
// (2025-03-26 spec). SSE-only, WebSocket, SDK, claudeai-proxy, SSE-IDE,
// and WS-IDE transports are deliberately out of scope — see
// docs/roadmap/v1/v1-3-mcp.md §6.
package mcp
```

### Task 2 — Settings + types + normalization (Tasks 2.1 / 2.2 / 2.3)

#### 2.1 `types.go`

```go
package mcp

import (
    "time"
)

// TransportType is the wire-level transport kind.
type TransportType string

const (
    TransportStdio          TransportType = "stdio"
    TransportStreamableHTTP TransportType = "http"
)

// ServerConfig is the parsed shape of one entry under mcpServers.
// Mirrors ref/src/services/mcp/types.ts McpStdioServerConfigSchema +
// McpHTTPServerConfigSchema, simplified.
type ServerConfig struct {
    Name      string            // map key from settings.json
    Type      TransportType
    Disabled  bool              // "disabled": true skips connect

    // Stdio fields
    Command string              // required when Type == TransportStdio
    Args    []string
    Env     map[string]string   // ${VAR} / ${VAR:-default} expansion happens at Load

    // HTTP fields
    URL     string              // required when Type == TransportStreamableHTTP
    Headers map[string]string

    // Common
    Timeout time.Duration       // connect timeout; default 30s; max 600s
    Scope   ConfigScope         // Project | User — for telemetry/logging only
}

// ConfigScope identifies where a server config was loaded from. Mirrors
// hooks/skills sourcing — workdir overrides user.
type ConfigScope string

const (
    ScopeUser    ConfigScope = "user"    // <APP_HOME>/settings.json
    ScopeProject ConfigScope = "project" // <workdir>/.evva/settings.json
)

// ServerStatus is the live runtime state of one server's connection.
type ServerStatus string

const (
    StatusConnected ServerStatus = "connected"
    StatusPending   ServerStatus = "pending"     // Connect in flight
    StatusFailed    ServerStatus = "failed"      // Connect returned err; tools=0
    StatusNeedsAuth ServerStatus = "needs-auth"  // HTTP 401; auth tool offered
    StatusDisabled  ServerStatus = "disabled"
)

// ServerState is what Manager.Status() returns per server.
type ServerState struct {
    Name         string
    Config       ServerConfig
    Status       ServerStatus
    Error        string    // populated for StatusFailed / StatusNeedsAuth
    ToolCount    int       // number of tools discovered (0 unless Connected)
    ResourceCount int      // 0 unless server advertises resources/list capability
    ConnectedAt  time.Time // zero unless Connected
}
```

#### 2.2 `normalize.go` (port ref `normalization.ts` verbatim)

```go
package mcp

import "regexp"

var invalidNameChar = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// NormalizeName maps an arbitrary server or tool name into the API-safe
// pattern ^[a-zA-Z0-9_-]{1,64}$. Replaces every invalid character with
// an underscore. Direct port of ref/src/services/mcp/normalization.ts:
// normalizeNameForMCP, minus the claude.ai-specific underscore-collapse
// branch (we don't ship claude.ai-proxy servers).
func NormalizeName(name string) string {
    return invalidNameChar.ReplaceAllString(name, "_")
}
```

#### 2.3 `stringutils.go` (port ref `mcpStringUtils.ts`)

```go
package mcp

import "strings"

// ToolNamePrefix returns "mcp__<server>__" for the normalized server.
func ToolNamePrefix(server string) string {
    return "mcp__" + NormalizeName(server) + "__"
}

// BuildToolName returns "mcp__<server>__<tool>" with both names
// normalized. Inverse of ParseToolName.
func BuildToolName(server, tool string) string {
    return ToolNamePrefix(server) + NormalizeName(tool)
}

// ToolNameInfo is the parsed shape: server + tool, or nil if name is
// not a valid mcp__ prefixed identifier.
type ToolNameInfo struct {
    Server string
    Tool   string
}

// ParseToolName extracts server + tool from a mcp__<server>__<tool>
// string. Returns nil if name lacks the prefix or has no tool segment.
// Known limitation: if a server name contains "__", parsing reports
// the first segment as the server. Server names with double-underscore
// are rare in practice (ref has the same limitation).
func ParseToolName(name string) *ToolNameInfo {
    parts := strings.Split(name, "__")
    if len(parts) < 3 || parts[0] != "mcp" || parts[1] == "" {
        return nil
    }
    return &ToolNameInfo{
        Server: parts[1],
        Tool:   strings.Join(parts[2:], "__"),
    }
}
```

#### 2.4 `envexpand.go` (port ref `envExpansion.ts`)

```go
package mcp

import (
    "os"
    "regexp"
    "strings"
)

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ExpandEnv expands ${VAR} and ${VAR:-default} references in s using the
// process environment. Returns the expanded string and a slice of any
// referenced variables that were unset and had no default. Direct port
// of ref/src/services/mcp/envExpansion.ts:expandEnvVarsInString.
func ExpandEnv(s string) (expanded string, missing []string) {
    expanded = envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
        inner := strings.TrimPrefix(strings.TrimSuffix(match, "}"), "${")
        name, def, hasDef := strings.Cut(inner, ":-")
        if v, ok := os.LookupEnv(name); ok {
            return v
        }
        if hasDef {
            return def
        }
        missing = append(missing, name)
        return match
    })
    return expanded, missing
}
```

Tests assert: `${HOME}` → `os.Getenv("HOME")`; `${NOT_SET:-fallback}` →
`"fallback"`; `${NOT_SET}` → `${NOT_SET}` literal + `["NOT_SET"]` in
missing; nested braces handled (`${A:-${B}}` is allowed — note inner
`${B}` is preserved literally per ref, not recursively expanded).

#### 2.5 `config.go`

```go
package mcp

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"
)

// Warning is a non-fatal load issue. Mirrors hooks.Warning shape so
// callers surface MCP warnings the same way they surface hook ones.
type Warning struct {
    Path string
    Err  error
}

func (w Warning) Error() string {
    if w.Path == "" { return w.Err.Error() }
    return fmt.Sprintf("%s: %v", w.Path, w.Err)
}

// Config is the merged + normalized server list ready for Manager.Connect.
type Config struct {
    Servers []ServerConfig
}

// fileShape is the JSON shape under the "mcpServers" key. Each map
// entry is one server; the key is the server name. We accept both:
//
//   { "mcpServers": { "fs": { "command": "...", "args": [...] } } }
//
// and (Claude Code-compatible) per-server "type":
//
//   { "mcpServers": { "fs": { "type": "stdio", "command": "...", ... } } }
type fileShape struct {
    McpServers map[string]rawServer `json:"mcpServers"`
}

type rawServer struct {
    Type     string            `json:"type"`
    Disabled bool              `json:"disabled"`
    Command  string            `json:"command"`
    Args     []string          `json:"args"`
    Env      map[string]string `json:"env"`
    URL      string            `json:"url"`
    Headers  map[string]string `json:"headers"`
    Timeout  int               `json:"timeout"` // seconds
}

// Load reads .evva/settings.json (project) and <evvaHome>/settings.json
// (user), merges the mcpServers blocks (project wins on name collision),
// expands env vars, and returns the normalized config + non-fatal
// warnings. Missing files are not errors. Malformed entries become
// Warnings; the rest of the file still loads.
func Load(workdir, evvaHome string) (*Config, []Warning) {
    cfg := &Config{}
    var warns []Warning
    byName := map[string]ServerConfig{}

    if evvaHome != "" {
        path := filepath.Join(evvaHome, "settings.json")
        ws := loadOne(path, ScopeUser, byName)
        warns = append(warns, ws...)
    }
    if workdir != "" {
        path := filepath.Join(workdir, ".evva", "settings.json")
        ws := loadOne(path, ScopeProject, byName)
        warns = append(warns, ws...)
    }

    for _, s := range byName {
        cfg.Servers = append(cfg.Servers, s)
    }
    return cfg, warns
}

// loadOne parses one settings.json, validates each server entry, expands
// env vars, and writes resulting ServerConfig values into byName.
// Project scope overwrites User scope entries by name (called second).
func loadOne(path string, scope ConfigScope, byName map[string]ServerConfig) []Warning {
    raw, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil
        }
        return []Warning{{Path: path, Err: err}}
    }
    var shape fileShape
    if err := json.Unmarshal(raw, &shape); err != nil {
        return []Warning{{Path: path, Err: fmt.Errorf("invalid json: %w", err)}}
    }

    var warns []Warning
    for name, rs := range shape.McpServers {
        cfg, ws := normalizeServer(path, name, scope, rs)
        warns = append(warns, ws...)
        if cfg == nil {
            continue
        }
        byName[name] = *cfg
    }
    return warns
}

// normalizeServer validates one rawServer entry. Returns nil and a
// Warning when the entry is unusable (missing required fields, bad
// type, invalid timeout). Env-var expansion failures are warnings but
// don't drop the entry — the server starts with the literal value and
// is likely to fail at connect, which is a more actionable error.
func normalizeServer(path, name string, scope ConfigScope, rs rawServer) (*ServerConfig, []Warning) {
    var warns []Warning
    cfg := &ServerConfig{
        Name:     name,
        Disabled: rs.Disabled,
        Scope:    scope,
        Headers:  rs.Headers,
    }

    t := strings.ToLower(strings.TrimSpace(rs.Type))
    // Default: if command is set → stdio; if url is set → http.
    if t == "" {
        if rs.Command != "" { t = "stdio" } else if rs.URL != "" { t = "http" }
    }
    switch t {
    case "stdio":
        if rs.Command == "" {
            warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: stdio requires command", name)})
            return nil, warns
        }
        cfg.Type = TransportStdio
        cfg.Command = rs.Command
        cfg.Args = rs.Args
        cfg.Env = map[string]string{}
        for k, v := range rs.Env {
            exp, missing := ExpandEnv(v)
            if len(missing) > 0 {
                warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s.env.%s: missing %v", name, k, missing)})
            }
            cfg.Env[k] = exp
        }
        // Also expand command + args
        if expCmd, missing := ExpandEnv(rs.Command); len(missing) == 0 {
            cfg.Command = expCmd
        } // else keep literal; connect will error if it matters
        cfg.Args = make([]string, len(rs.Args))
        for i, a := range rs.Args {
            ea, _ := ExpandEnv(a)
            cfg.Args[i] = ea
        }
    case "http":
        if rs.URL == "" {
            warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: http requires url", name)})
            return nil, warns
        }
        cfg.Type = TransportStreamableHTTP
        cfg.URL = rs.URL
    default:
        warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: unknown type %q (want \"stdio\" or \"http\")", name, rs.Type)})
        return nil, warns
    }

    if rs.Timeout != 0 {
        if rs.Timeout < 1 || rs.Timeout > 600 {
            warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: timeout %d out of range [1,600]", name, rs.Timeout)})
        } else {
            cfg.Timeout = time.Duration(rs.Timeout) * time.Second
        }
    }
    if cfg.Timeout == 0 {
        cfg.Timeout = 30 * time.Second
    }
    return cfg, warns
}
```

Tests assert: missing file (no error); malformed JSON (Warning, no
panic); both transports parse correctly; type-default inference
(`command` → stdio); env expansion happens at Load; bad timeout
(>600) warns; project scope overrides user.

### Task 3 — Manager + client (the runtime heart)

#### 3.1 `transport.go`

```go
package mcp

import (
    "fmt"
    "os/exec"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildStdioTransport returns an SDK transport that spawns the
// configured subprocess. Env is merged with the parent process env
// (so things like PATH stay available) — explicit entries override
// inherited ones.
func buildStdioTransport(c ServerConfig) (mcpsdk.Transport, error) {
    if c.Command == "" {
        return nil, fmt.Errorf("stdio transport: command is empty")
    }
    cmd := exec.Command(c.Command, c.Args...)
    cmd.Env = mergeEnv(c.Env)
    return &mcpsdk.CommandTransport{Command: cmd}, nil
}

// buildStreamableHTTPTransport returns an SDK transport configured for
// the 2025-03-26 Streamable HTTP transport. OAuth handler is nil here;
// the Manager attaches one via oauth.go once a 401 is observed (the
// SDK calls the handler lazily on the first auth-required request).
func buildStreamableHTTPTransport(c ServerConfig, oauth *OAuthHandler) (mcpsdk.Transport, error) {
    if c.URL == "" {
        return nil, fmt.Errorf("http transport: url is empty")
    }
    t := &mcpsdk.StreamableClientTransport{Endpoint: c.URL}
    if oauth != nil { t.OAuthHandler = oauth.SDKHandler() }
    // Headers attached via a wrapping HTTPClient — see Task 3.2 NewClient.
    return t, nil
}

func mergeEnv(extra map[string]string) []string {
    base := os.Environ()
    for k, v := range extra { base = append(base, k+"="+v) }
    return base
}
```

#### 3.2 `client.go`

```go
package mcp

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "strings"
    "sync"
    "time"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client wraps one SDK ClientSession with the lifecycle policy this phase
// needs: lazy re-connect on session-expired, lock-protected swap of
// the underlying session, and a small set of convenience methods that
// the dynamic tool factories call.
type Client struct {
    Name   string
    Config ServerConfig

    mu      sync.RWMutex
    session *mcpsdk.ClientSession // may be replaced after reconnect
    status  ServerStatus
    lastErr error
    tools   []*mcpsdk.Tool        // result of last tools/list
    caps    *mcpsdk.ServerCapabilities

    logger   *slog.Logger
    oauth    *OAuthHandler // nil for stdio
    evvaHome string        // threaded to ConvertResult for blob persistence; "" disables blob writes
}

// connect runs the initial Connect + initialize handshake. Caller
// holds c.mu.
func (c *Client) connect(ctx context.Context) error {
    var transport mcpsdk.Transport
    var err error
    switch c.Config.Type {
    case TransportStdio:
        transport, err = buildStdioTransport(c.Config)
    case TransportStreamableHTTP:
        transport, err = buildStreamableHTTPTransport(c.Config, c.oauth)
    default:
        return fmt.Errorf("unknown transport %q", c.Config.Type)
    }
    if err != nil { return err }

    impl := &mcpsdk.Implementation{Name: "evva", Version: "1.6.0"}
    sdkClient := mcpsdk.NewClient(impl, &mcpsdk.ClientOptions{
        // Log progress and server-logs to the agent logger; no UI surface in this phase.
        ProgressNotificationHandler: c.logProgress,
        LoggingMessageHandler:       c.logServerLog,
        // Sampling, Elicitation, Roots, CreateMessage* — out of scope §6.
    })

    timeout := c.Config.Timeout
    if timeout == 0 { timeout = 30 * time.Second }
    cctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    session, err := sdkClient.Connect(cctx, transport, nil)
    if err != nil {
        if isAuthError(err) {
            c.status = StatusNeedsAuth
            c.lastErr = err
            return nil // not a hard failure — auth tool will offer recovery
        }
        c.status = StatusFailed
        c.lastErr = err
        return err
    }
    c.session = session
    c.status = StatusConnected
    c.caps = session.InitializeResult().Capabilities
    return nil
}

// ListTools fetches the server's tool catalog. Returns nil on
// disconnected/failed/auth-needed states.
func (c *Client) ListTools(ctx context.Context) ([]*mcpsdk.Tool, error) {
    c.mu.RLock()
    if c.status != StatusConnected || c.session == nil {
        defer c.mu.RUnlock()
        return nil, nil
    }
    s := c.session
    c.mu.RUnlock()

    res, err := s.ListTools(ctx, nil)
    if err != nil { return nil, err }
    c.mu.Lock()
    c.tools = res.Tools
    c.mu.Unlock()
    return res.Tools, nil
}

// CallTool invokes a tool, retrying once on session-expired.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcpsdk.CallToolResult, error) {
    for attempt := 0; attempt < 2; attempt++ {
        c.mu.RLock()
        s := c.session
        c.mu.RUnlock()
        if s == nil { return nil, errors.New("mcp: no active session") }

        result, err := s.CallTool(ctx, &mcpsdk.CallToolParams{
            Name:      name,
            Arguments: args,
        })
        if err == nil { return result, nil }
        if attempt == 0 && isSessionExpired(err) {
            c.logger.Info("mcp: session expired, reconnecting", "server", c.Name)
            if reErr := c.reconnect(ctx); reErr != nil {
                return nil, fmt.Errorf("reconnect after session-expired: %w", reErr)
            }
            continue
        }
        if isAuthError(err) {
            c.mu.Lock()
            c.status = StatusNeedsAuth
            c.mu.Unlock()
        }
        return nil, err
    }
    return nil, errors.New("mcp: unreachable retry loop")
}

// reconnect tears down the current session and runs Connect again.
// Caller does NOT hold c.mu.
func (c *Client) reconnect(ctx context.Context) error {
    c.mu.Lock()
    defer c.mu.Unlock()
    if c.session != nil {
        _ = c.session.Close()
        c.session = nil
    }
    return c.connect(ctx)
}

// isSessionExpired detects MCP session-not-found errors per the spec:
// HTTP 404 + JSON-RPC code -32001.
//
// WARNING: string-match on err.Error(). The SDK does not expose
// structured HTTP error types as of v1.6.x (tracked at
// https://github.com/modelcontextprotocol/go-sdk/issues/579). If the
// SDK changes its error-message formatting between minor versions,
// this matcher silently misclassifies — which is why
// TestErrorMatchers_PinSDKShape (Task 7) constructs the SDK's current
// error shape from a stub transport and asserts both matchers fire.
// That test is the canary: a red TestErrorMatchers means the SDK's
// error format moved and the matcher needs updating.
//
// Until #579 lands, the hot-path cost of the string scan is
// negligible (called only on CallTool error, retry once).
func isSessionExpired(err error) bool {
    if err == nil { return false }
    s := err.Error()
    return strings.Contains(s, "404") && strings.Contains(s, "-32001")
}

// isAuthError detects 401/403 from HTTP transports. Same string-match
// caveat as isSessionExpired above — pinned by TestErrorMatchers_PinSDKShape.
func isAuthError(err error) bool {
    if err == nil { return false }
    s := err.Error()
    return strings.Contains(s, "401") || strings.Contains(s, "403") ||
        strings.Contains(s, "Unauthorized") || strings.Contains(s, "auth required")
}

func (c *Client) logProgress(ctx context.Context, p *mcpsdk.ProgressNotificationClientRequest) {
    c.logger.Debug("mcp.progress", "server", c.Name, "progress", p.Params.Progress, "total", p.Params.Total)
}

func (c *Client) logServerLog(ctx context.Context, p *mcpsdk.LoggingMessageRequest) {
    c.logger.Info("mcp.server_log", "server", c.Name, "level", p.Params.Level, "msg", p.Params.Data)
}
```

#### 3.3 `manager.go`

```go
package mcp

import (
    "context"
    "io"
    "log/slog"
    "sort"
    "sync"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager holds every Client and is the seam internal/agent threads
// through ToolState. Safe for concurrent use.
type Manager struct {
    mu       sync.RWMutex
    clients  map[string]*Client // keyed by server name
    logger   *slog.Logger
    evvaHome string             // resolved app-home dir, threaded to ConvertResult for blob persistence
    prompt   OAuthPromptFn      // host-installed OAuth prompt, may be nil (HTTP-auth servers will fail with a clear error)
}

// OpenOptions carries the host-supplied dependencies the Manager needs
// at construction time. Every field is optional and has a defined nil/
// zero behavior — this keeps Open's signature stable as the option set
// grows in future minors.
type OpenOptions struct {
    // Logger receives mcp.* slog entries (connect attempts, server
    // logs, progress notifications). nil yields a discard logger.
    Logger *slog.Logger

    // EvvaHome is the resolved per-user home dir (cfg.AppHome) used
    // for binary-blob persistence under <EvvaHome>/mcp-blobs. Empty
    // string disables blob persistence — read_mcp_resource will
    // surface a "blob received but no EvvaHome configured" note in
    // place of a path.
    EvvaHome string

    // OAuthPrompt is called when an HTTP MCP server requires OAuth
    // authorization. The host installs an implementation that
    // surfaces the auth URL to the user (e.g.
    // internal/agent.mcpPromptViaQuestion adapts it to
    // question.Broker). nil disables OAuth — HTTP servers that
    // return 401 will land in StatusNeedsAuth and the per-server
    // authenticate tool will surface a clear "no prompt callback
    // installed" error if the model invokes it.
    OAuthPrompt OAuthPromptFn
}

// NewManager returns an empty Manager configured from opts. Callers
// populate via Connect or use the convenience constructor Open.
func NewManager(opts OpenOptions) *Manager {
    lg := opts.Logger
    if lg == nil {
        lg = slog.New(slog.NewTextHandler(io.Discard, nil))
    }
    return &Manager{
        clients:  map[string]*Client{},
        logger:   lg,
        evvaHome: opts.EvvaHome,
        prompt:   opts.OAuthPrompt,
    }
}

// Open is the one-call constructor for the typical host flow: build a
// Manager, run Connect for every non-disabled server in parallel,
// return the result. ctx scopes each per-server connect; manager-wide
// lifetime is bound to the agent's RootContext (the caller passes
// rootCtx as ctx when running the agent).
//
// Returns warnings from the SDK / per-server connect failures alongside
// the Manager — connection failures do not abort the function.
func Open(ctx context.Context, cfg *Config, opts OpenOptions) (*Manager, []Warning) {
    m := NewManager(opts)
    if cfg == nil || len(cfg.Servers) == 0 { return m, nil }

    var (
        wg    sync.WaitGroup
        wmu   sync.Mutex
        warns []Warning
    )
    for _, sc := range cfg.Servers {
        if sc.Disabled {
            m.add(&Client{
                Name: sc.Name, Config: sc, status: StatusDisabled,
                logger: m.logger, evvaHome: m.evvaHome,
            })
            continue
        }
        sc := sc
        wg.Add(1)
        go func() {
            defer wg.Done()
            c := &Client{
                Name: sc.Name, Config: sc, logger: m.logger,
                evvaHome: m.evvaHome, status: StatusPending,
            }
            if sc.Type == TransportStreamableHTTP {
                c.oauth = NewOAuthHandler(sc.Name, m.logger, m.prompt)
            }
            if err := c.connect(ctx); err != nil {
                wmu.Lock()
                warns = append(warns, Warning{Path: sc.Name, Err: err})
                wmu.Unlock()
            }
            m.add(c)
        }()
    }
    wg.Wait()

    // Pull tools for connected servers in parallel — same fan-out shape.
    for _, c := range m.list() {
        c := c
        if c.status != StatusConnected { continue }
        wg.Add(1)
        go func() {
            defer wg.Done()
            if _, err := c.ListTools(ctx); err != nil {
                wmu.Lock()
                warns = append(warns, Warning{Path: c.Name, Err: err})
                wmu.Unlock()
            }
        }()
    }
    wg.Wait()
    return m, warns
}

func (m *Manager) add(c *Client) {
    m.mu.Lock()
    m.clients[c.Name] = c
    m.mu.Unlock()
}

// Client returns the named client or nil.
func (m *Manager) Client(name string) *Client {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.clients[name]
}

// Status returns a snapshot of every server's runtime state, sorted by
// name for stable output (used by Manager.LogStatus and any future
// /mcp UI panel).
func (m *Manager) Status() []ServerState {
    list := m.list()
    out := make([]ServerState, 0, len(list))
    for _, c := range list {
        c.mu.RLock()
        out = append(out, ServerState{
            Name: c.Name, Config: c.Config, Status: c.status,
            Error: errString(c.lastErr), ToolCount: len(c.tools),
        })
        c.mu.RUnlock()
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out
}

func (m *Manager) list() []*Client {
    m.mu.RLock()
    out := make([]*Client, 0, len(m.clients))
    for _, c := range m.clients { out = append(out, c) }
    m.mu.RUnlock()
    return out
}

// DiscoveredToolNames returns every mcp__<server>__<tool> name across
// all connected servers, sorted. Called by internal/agent at profile
// build time to extend the deferred allowlist.
func (m *Manager) DiscoveredToolNames() []string {
    var out []string
    for _, c := range m.list() {
        c.mu.RLock()
        if c.status == StatusConnected {
            for _, t := range c.tools {
                out = append(out, BuildToolName(c.Name, t.Name))
            }
        }
        // needs-auth servers expose one auth tool each (the McpAuthTool factory).
        if c.status == StatusNeedsAuth {
            out = append(out, BuildToolName(c.Name, "authenticate"))
        }
        c.mu.RUnlock()
    }
    sort.Strings(out)
    return out
}

// Shutdown closes every active session. Idempotent and safe to call
// multiple times; bound to the agent's RootContext cancel.
func (m *Manager) Shutdown() {
    for _, c := range m.list() {
        c.mu.Lock()
        if c.session != nil { _ = c.session.Close(); c.session = nil }
        c.mu.Unlock()
    }
}

func errString(e error) string {
    if e == nil { return "" }
    return e.Error()
}
```

#### 3.4 Dynamic tool factory registration

When the Manager has fetched tools for a server, every tool needs a
`pubtoolset.ToolFactory` registered on `DefaultRegistry()` so the agent
can `Build()` it on demand.

This is the **critical new wiring** — add to `manager.go` after the
`Open` body:

```go
// RegisterFactories registers a pubtoolset.ToolFactory for every tool
// discovered across every connected server, keyed by the qualified
// mcp__<server>__<tool> name. Idempotent: re-calling on a registry
// that already has an entry is a no-op (Register returns "duplicate"
// which we silently absorb — same instance, same factory, same
// behavior). Called by internal/agent during New, after Manager.Open
// completes, before profile.DeferredTools is finalized.
func (m *Manager) RegisterFactories(reg *pubtoolset.Registry) {
    for _, c := range m.list() {
        c.mu.RLock()
        defer c.mu.RUnlock()
        // Real tools
        for _, t := range c.tools {
            t := t
            name := tools.ToolName(BuildToolName(c.Name, t.Name))
            client := c
            _ = reg.Register(name, func(_ tools.State) (tools.Tool, error) {
                return newMcpTool(client, t), nil
            })
        }
        // Per-server auth tool (one-shot — only register if needs-auth)
        if c.status == StatusNeedsAuth {
            name := tools.ToolName(BuildToolName(c.Name, "authenticate"))
            client := c
            _ = reg.Register(name, func(_ tools.State) (tools.Tool, error) {
                return newMcpAuthTool(client), nil
            })
        }
    }
}
```

`newMcpTool` (also in `client.go`) builds a `tools.Tool` value whose
`Name()` / `Description()` / `Schema()` come from the SDK Tool, and
whose `Execute` calls `client.CallTool` and converts the result via
`result.go`:

```go
func newMcpTool(c *Client, sdkTool *mcpsdk.Tool) tools.Tool {
    schemaBytes, _ := json.Marshal(sdkTool.InputSchema)
    name := BuildToolName(c.Name, sdkTool.Name)
    return &mcpToolImpl{
        name:    name,
        desc:    sdkTool.Description,
        schema:  schemaBytes,
        call: func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
            var args map[string]any
            if len(raw) > 0 {
                if err := json.Unmarshal(raw, &args); err != nil {
                    return tools.Result{IsError: true, Content: "mcp: decode args: " + err.Error()}, nil
                }
            }
            res, err := c.CallTool(ctx, sdkTool.Name, args)
            if err != nil {
                return tools.Result{IsError: true, Content: "mcp: call failed: " + err.Error()}, nil
            }
            return ConvertResult(res, c.Name, sdkTool.Name, c.evvaHome)
        },
    }
}

type mcpToolImpl struct {
    name, desc string
    schema     json.RawMessage
    call       func(context.Context, json.RawMessage) (tools.Result, error)
}

func (t *mcpToolImpl) Name() string                  { return t.name }
func (t *mcpToolImpl) Description() string           { return t.desc }
func (t *mcpToolImpl) Schema() json.RawMessage       { return t.schema }
func (t *mcpToolImpl) Execute(ctx context.Context, lgr *slog.Logger, in json.RawMessage) (tools.Result, error) {
    return t.call(ctx, in)
}
```

#### 3.5 `result.go` — SDK result → `tools.Result`

```go
package mcp

import (
    "crypto/rand"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/johnny1110/evva/pkg/tools"
)

const maxResultChars = 100_000 // mirrors ref MCPTool.maxResultSizeChars

// ConvertResult turns an SDK CallToolResult into the agent's tools.Result.
// Text blocks concatenate into Content; image blocks become ContentBlocks
// via tools.NewImageResult equivalents; binary resource blobs are
// persisted to disk under <evvaHome>/mcp-blobs/<random> and replaced
// with a "[binary saved at <path>]" line.
//
// evvaHome is the resolved cfg.AppHome — passed in so this function
// stays a pure conversion helper (no env reads, no global state). When
// evvaHome is empty, blob persistence is disabled and the conversion
// emits a "[binary received but no AppHome configured]" note instead.
// This is the only behavioral path that touches the filesystem.
func ConvertResult(r *mcpsdk.CallToolResult, server, tool, evvaHome string) (tools.Result, error) {
    if r == nil { return tools.Result{}, errors.New("mcp: nil result") }

    var (
        textBuf strings.Builder
        blocks  []tools.ContentBlock
    )
    for _, item := range r.Content {
        switch c := item.(type) {
        case *mcpsdk.TextContent:
            textBuf.WriteString(c.Text)
            textBuf.WriteString("\n")
        case *mcpsdk.ImageContent:
            blocks = append(blocks, tools.ContentBlock{
                Type: tools.ContentBlockImage,
                Image: &tools.ImageBlock{
                    MIMEType:     c.MIMEType,
                    Base64Data:   c.Data,
                    OriginalSize: int64(len(c.Data)),
                },
            })
        case *mcpsdk.EmbeddedResource:
            if evvaHome == "" {
                fmt.Fprintf(&textBuf, "[binary content from %s/%s received but blob persistence disabled (no AppHome configured)]\n", server, tool)
                break
            }
            if path, size, err := persistResourceBlob(c, evvaHome); err == nil {
                fmt.Fprintf(&textBuf, "[binary content saved at %s, %d bytes]\n", path, size)
            } else {
                fmt.Fprintf(&textBuf, "[binary content not saved: %v]\n", err)
            }
        }
    }

    content := textBuf.String()
    if n := len(content); n > maxResultChars {
        content = content[:maxResultChars] + fmt.Sprintf("\n\n[truncated %d chars]", n-maxResultChars)
    }
    return tools.Result{
        Content:       content,
        ContentBlocks: blocks,
        IsError:       r.IsError,
    }, nil
}

// persistResourceBlob writes a binary blob to <evvaHome>/mcp-blobs/<random>
// and returns the path + byte count. evvaHome MUST be non-empty (caller
// gates on that). The blob dir is created on first use with mode 0700.
//
// Filename is a 16-byte cryptographic-random hex string — sufficient
// uniqueness for the lifetime of an evva session without pulling in a
// UUID library. crypto/rand is already a transitive dep elsewhere in
// the project (the SDK uses it for OAuth PKCE), so this adds no new
// module to go.sum.
func persistResourceBlob(r *mcpsdk.EmbeddedResource, evvaHome string) (string, int, error) {
    blobDir := filepath.Join(evvaHome, "mcp-blobs")
    if err := os.MkdirAll(blobDir, 0o700); err != nil { return "", 0, err }

    if r.Resource == nil || r.Resource.Blob == "" {
        return "", 0, errors.New("no blob content")
    }
    raw, err := base64.StdEncoding.DecodeString(r.Resource.Blob)
    if err != nil { return "", 0, err }

    name, err := randomBlobName()
    if err != nil { return "", 0, err }
    path := filepath.Join(blobDir, name)
    if err := os.WriteFile(path, raw, 0o600); err != nil { return "", 0, err }
    return path, len(raw), nil
}

// randomBlobName returns a 32-char lowercase-hex filename backed by 16
// bytes of crypto/rand. Used in place of a UUID library — collision
// odds at evva session scale are vanishingly small (≈ birthday bound
// at 2^64 entries before a 50% collision).
func randomBlobName() (string, error) {
    var b [16]byte
    if _, err := rand.Read(b[:]); err != nil { return "", err }
    return hex.EncodeToString(b[:]), nil
}
```

### Task 4 — Wire the Manager into the agent

#### 4.1 ToolState slot

`internal/toolset/toolset.go` — add a slot mirroring `skillRegistry`:

```go
// mcpManager holds the discovered MCP server connections. Installed
// once at startup by the host (cmd/evva) or auto-loaded by agent.New;
// read by every dynamic mcp__<server>__<tool> factory + by the
// list_mcp_resources / read_mcp_resource tools. Subagents inherit the
// pointer via agent.WithMcpManager so MCP tools are reachable from a
// subagent's tool dispatch.
mcpManager *mcp.Manager
```

Plus accessors:

```go
func (s *ToolState) McpManager() *mcp.Manager   { return s.mcpManager }
func (s *ToolState) SetMcpManager(m *mcp.Manager) { s.mcpManager = m }
```

Import added at the file's import block. **Note:** this creates a new
`internal/toolset → pkg/mcp` dependency; `pkg/mcp` deliberately has no
back-reference to `internal/*`.

#### 4.2 Agent option + auto-load

`internal/agent/options.go` — add the option:

```go
func WithMcpManager(m *mcp.Manager) Option {
    return func(a *Agent) { a.toolState.SetMcpManager(m) }
}
```

`pkg/agent/options.go` — public re-export following the
`WithHookRegistry` pattern.

`internal/agent/agent.go` — beside the skill auto-load block (~line
310), add:

```go
// Auto-load the MCP manager if no override was injected via
// WithMcpManager. Mirrors loadDiskSkillRegistry / hooks.Load: one disk
// read at startup, no cost when nothing is configured. The cfg-derived
// settings.json paths are the same files hooks.Load reads, so users
// have one place to manage both.
//
// OAuthPrompt is installed as a late-bound closure: the question.Broker
// doesn't exist yet (wireBrokers runs further down). Reading
// a.toolState.QuestionBroker() at OAuth-time is safe because the broker
// is set well before any HTTP server can return a 401 (boot is
// synchronous; OAuth only fires on a tool-call event).
if a.toolState.McpManager() == nil {
    cfg, warns := mcp.Load(a.cfg.WorkDir, a.cfg.AppHome)
    for _, w := range warns {
        lgr.Warn("mcp: config", "msg", w)
    }
    mgr, openWarns := mcp.Open(a.rootCtx, cfg, mcp.OpenOptions{
        Logger:      lgr,
        EvvaHome:    a.cfg.AppHome,
        OAuthPrompt: mcpPromptViaQuestion(func() question.Broker { return a.toolState.QuestionBroker() }),
    })
    for _, w := range openWarns {
        lgr.Warn("mcp: connect", "server", w.Path, "err", w.Err)
    }
    // Register dynamic factories before profile is finalized below so
    // the discovered names can land in profile.DeferredTools.
    mgr.RegisterFactories(pubtoolset.DefaultRegistry())
    a.toolState.SetMcpManager(mgr)
}
```

Plus a `Shutdown()` extension: cancel the rootCtx already cleans up the
SDK sessions because each `client.go.session.Close()` is invoked via
defer on context-canceled SDK internals. Explicit `mgr.Shutdown()` is
added to the `Agent.Shutdown()` path for completeness so subprocess MCP
servers exit cleanly without waiting for stdin EOF.

The `mcpPromptViaQuestion` adapter lives in `internal/agent/mcp_wiring.go`
(new file alongside `internal/agent/skills.go`) — see §4.6 for the
~30-line body. The wiring exists in `internal/agent` (not in `pkg/mcp`)
specifically so `pkg/mcp` stays free of any `internal/question` import.

#### 4.3 Profile-time deferred name injection

`internal/agent/profiles.go` `Main(...)` — after the `deferredTools`
slice is built, append discovered MCP names IF the manager is reachable
at this point. Manager is on `toolState`; `Main` doesn't currently take
a `*ToolState` (it receives `cfg`). Pass it through OR call into the
manager via a thin reader:

```go
// In Main(), after deferredTools is built:
if mgr := mcpManagerFromCfg(cfg); mgr != nil {
    for _, name := range mgr.DiscoveredToolNames() {
        deferredTools = append(deferredTools, tools.ToolName(name))
    }
}
```

`mcpManagerFromCfg` doesn't exist yet — needs introduction. **Cleaner
seam:** the MCP names ride on a new field `Profile.ExtraDeferred []tools.ToolName`
that the agent populates from `ToolState.McpManager()` AFTER profile
construction but BEFORE the sysprompt is computed. This avoids
threading `*ToolState` into the profile constructor, which would couple
two layers that have stayed separate so far.

Implement as: in `agent.go` `New`, between `loadDiskSkillRegistry` and
the sysprompt build (which today happens inside `profile.Main`),
restructure so the profile is constructed in two phases:

1. `profile := agent.Main(cfg, ...)` builds tool lists + LLM options
   only — no sysprompt build inside.
2. Compute `deferredNames = profile.DeferredTools + mgr.DiscoveredToolNames()`.
3. Call a new `sysprompt.BuildMainPrompt(ctx, deferredNames)` that
   replaces the inline `sysprompt.MainAgent.BuildSystemPrompt(ctx)`
   call with one that takes the final deferred set.

This is the **only invasive restructure** in this phase — every other change
is additive. The simplification is real: the sysprompt today is rebuilt
on `SwitchProfile`, so threading the manager through the profile
constructor would require a parallel API. A two-phase build keeps the
public seam (`profile.Main`) the same and lets the agent attach the
final deferred list.

The deferred allowlist and `<available-deferred-tools>` block both
update from this combined set; everything downstream (`tool_search`'s
`mcp__server` fast path, fuzzy scoring, `MarkDiscovered`) already works.

#### 4.4 Three new built-in tool registrations

`internal/toolset/builtins.go` — register the three statically-named
MCP meta tools (the per-server dynamic factories are registered by
`Manager.RegisterFactories`):

```go
// list_mcp_resources — deferred
pubtoolset.DefaultRegistry().MustRegister(tools.LIST_MCP_RESOURCES, func(s tools.State) (tools.Tool, error) {
    ts := s.(*ToolState)
    return mcp.NewListResourcesTool(ts.McpManager()), nil
})

// read_mcp_resource — deferred
pubtoolset.DefaultRegistry().MustRegister(tools.READ_MCP_RESOURCE, func(s tools.State) (tools.Tool, error) {
    ts := s.(*ToolState)
    return mcp.NewReadResourceTool(ts.McpManager()), nil
})
```

And **`pkg/tools/name.go`** gets new constants:

```go
// MCP resource tools — deferred meta tools that work across any
// configured MCP server. Per-server tools and per-server auth tools are
// registered dynamically by the Manager and follow the
// mcp__<server>__<tool> naming convention.
const (
    LIST_MCP_RESOURCES ToolName = "list_mcp_resources"
    READ_MCP_RESOURCE  ToolName = "read_mcp_resource"
)
```

These names land in the Main profile's `DeferredTools` slice alongside
the existing deferred set (`internal/agent/profiles.go:143-153`).

#### 4.5 Implementations of the two resource tools (in `pkg/mcp/`)

```go
// list_mcp_resources — pkg/mcp/tools_resources.go
package mcp

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log/slog"
    "sort"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/johnny1110/evva/pkg/tools"
)

type listResourcesTool struct{ m *Manager }

func NewListResourcesTool(m *Manager) tools.Tool { return &listResourcesTool{m: m} }

func (t *listResourcesTool) Name() string { return string(tools.LIST_MCP_RESOURCES) }

func (t *listResourcesTool) Description() string {
    return "List available resources from configured MCP servers. Each resource includes uri/name/mimeType/description plus a `server` field showing which server it came from. Optional `server` arg filters to one."
}

func (t *listResourcesTool) Schema() json.RawMessage {
    return json.RawMessage(`{
        "type":"object",
        "additionalProperties":false,
        "properties":{"server":{"type":"string","description":"Optional server name to filter."}}
    }`)
}

// resourceEntry mirrors ref's ListMcpResourcesTool output shape so the
// model sees consistent fields regardless of which server returned them.
type resourceEntry struct {
    URI         string `json:"uri"`
    Name        string `json:"name"`
    MIMEType    string `json:"mimeType,omitempty"`
    Description string `json:"description,omitempty"`
    Server      string `json:"server"`
}

func (t *listResourcesTool) Execute(ctx context.Context, lgr *slog.Logger, raw json.RawMessage) (tools.Result, error) {
    if t.m == nil {
        return tools.Result{IsError: true, Content: "list_mcp_resources: no MCP manager configured"}, nil
    }
    var in struct {
        Server string `json:"server"`
    }
    if len(raw) > 0 {
        if err := json.Unmarshal(raw, &in); err != nil {
            return tools.Result{IsError: true, Content: fmt.Sprintf("list_mcp_resources: decode: %v", err)}, nil
        }
    }

    // Pick the clients to query: the one named in args, or every
    // connected client. Filtering happens here so a typo in `server`
    // is surfaced as a clear error rather than an empty result.
    targets := t.m.list()
    if in.Server != "" {
        var matched []*Client
        for _, c := range targets {
            if c.Name == in.Server { matched = append(matched, c) }
        }
        if len(matched) == 0 {
            available := make([]string, 0, len(targets))
            for _, c := range targets { available = append(available, c.Name) }
            sort.Strings(available)
            return tools.Result{
                IsError: true,
                Content: fmt.Sprintf("list_mcp_resources: server %q not found; available: %v", in.Server, available),
            }, nil
        }
        targets = matched
    }

    var entries []resourceEntry
    for _, c := range targets {
        c.mu.RLock()
        status := c.status
        session := c.session
        caps := c.caps
        c.mu.RUnlock()
        if status != StatusConnected || session == nil { continue }
        if caps == nil || caps.Resources == nil {
            // Server doesn't advertise resources/list — skip silently;
            // most servers expose tools without resources.
            continue
        }
        res, err := session.ListResources(ctx, nil)
        if err != nil {
            lgr.Warn("list_mcp_resources", "server", c.Name, "err", err)
            continue
        }
        for _, r := range res.Resources {
            entries = append(entries, resourceEntry{
                URI:         r.URI,
                Name:        r.Name,
                MIMEType:    r.MIMEType,
                Description: r.Description,
                Server:      c.Name,
            })
        }
    }

    if len(entries) == 0 {
        return tools.Result{Content: "No resources found. MCP servers may still provide tools even if they have no resources."}, nil
    }
    sort.Slice(entries, func(i, j int) bool {
        if entries[i].Server != entries[j].Server { return entries[i].Server < entries[j].Server }
        return entries[i].URI < entries[j].URI
    })
    body, _ := json.MarshalIndent(entries, "", "  ")
    return tools.Result{Content: string(body)}, nil
}

// read_mcp_resource — pkg/mcp/tools_resources.go
type readResourceTool struct{ m *Manager }

func NewReadResourceTool(m *Manager) tools.Tool { return &readResourceTool{m: m} }

func (t *readResourceTool) Name() string { return string(tools.READ_MCP_RESOURCE) }

func (t *readResourceTool) Description() string {
    return "Reads a specific resource from an MCP server. `server` (required): the MCP server name. `uri` (required): the resource URI to read. Text content returns inline; binary blobs are persisted under <APP_HOME>/mcp-blobs/ and the path is returned in place of the bytes."
}

func (t *readResourceTool) Schema() json.RawMessage {
    return json.RawMessage(`{
        "type":"object",
        "additionalProperties":false,
        "required":["server","uri"],
        "properties":{
            "server":{"type":"string","description":"The MCP server name."},
            "uri":{"type":"string","description":"The resource URI to read."}
        }
    }`)
}

func (t *readResourceTool) Execute(ctx context.Context, lgr *slog.Logger, raw json.RawMessage) (tools.Result, error) {
    if t.m == nil {
        return tools.Result{IsError: true, Content: "read_mcp_resource: no MCP manager configured"}, nil
    }
    var in struct {
        Server string `json:"server"`
        URI    string `json:"uri"`
    }
    if err := json.Unmarshal(raw, &in); err != nil {
        return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: decode: %v", err)}, nil
    }
    if in.Server == "" || in.URI == "" {
        return tools.Result{IsError: true, Content: "read_mcp_resource: server and uri are required"}, nil
    }
    c := t.m.Client(in.Server)
    if c == nil {
        return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: server %q not found", in.Server)}, nil
    }
    c.mu.RLock()
    status := c.status
    session := c.session
    caps := c.caps
    c.mu.RUnlock()
    if status != StatusConnected || session == nil {
        return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: server %q is not connected (status=%s)", in.Server, status)}, nil
    }
    if caps == nil || caps.Resources == nil {
        return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: server %q does not support resources", in.Server)}, nil
    }

    res, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: in.URI})
    if err != nil {
        return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: %v", err)}, nil
    }

    // Convert each content item. Text comes through inline; blobs go
    // to disk via persistResourceBlob (same helper ConvertResult uses
    // for embedded resources in tool-call results).
    type contentEntry struct {
        URI         string `json:"uri"`
        MIMEType    string `json:"mimeType,omitempty"`
        Text        string `json:"text,omitempty"`
        BlobSavedTo string `json:"blobSavedTo,omitempty"`
    }
    var out []contentEntry
    for _, item := range res.Contents {
        entry := contentEntry{URI: item.URI, MIMEType: item.MIMEType}
        switch {
        case item.Text != "":
            entry.Text = item.Text
        case item.Blob != "":
            if c.evvaHome == "" {
                entry.Text = "[binary content received but no AppHome configured]"
                break
            }
            embedded := &mcpsdk.EmbeddedResource{Resource: &mcpsdk.ResourceContents{Blob: item.Blob}}
            if path, size, perr := persistResourceBlob(embedded, c.evvaHome); perr == nil {
                entry.BlobSavedTo = path
                entry.Text = fmt.Sprintf("[binary content saved at %s, %d bytes]", path, size)
            } else {
                entry.Text = fmt.Sprintf("[binary content not saved: %v]", perr)
            }
        }
        out = append(out, entry)
    }
    if len(out) == 0 {
        return tools.Result{IsError: true, Content: "read_mcp_resource: resource returned no contents"}, nil
    }
    body, _ := json.MarshalIndent(struct {
        Contents []contentEntry `json:"contents"`
    }{Contents: out}, "", "  ")
    return tools.Result{Content: string(body)}, nil
}
```

`McpAuthTool` is the analog of ref's `McpAuthTool.ts:49-214`, simplified
for v1.6 (no XAA, no in-browser auto-open). One factory per server in
`needs-auth` status, registered dynamically by
`Manager.RegisterFactories`. When invoked, it calls into
`c.oauth.SDKHandler()` which fires the OAuth flow through the installed
`OAuthPromptFn` — same path described in §3 (oauth.go).

#### 4.6 Host-side OAuth adapter — `internal/agent/mcp_wiring.go`

New file. Holds the one piece of host glue that bridges `pkg/mcp`'s
host-agnostic `OAuthPromptFn` to evva's internal `question.Broker`.
The body is shown verbatim in the oauth.go block above (§3 OAuth flow
section). Keeping it in `internal/agent` is the load-bearing constraint
that lets `pkg/mcp` stay free of any `internal/*` dependency.

Tests for `mcpPromptViaQuestion` (in `mcp_wiring_test.go`):
- broker returns `Cancel` → adapter returns `mcp.OAuthCancelled`, nil.
- broker returns `I'm done` → adapter returns `mcp.OAuthCompleted`, nil.
- broker errors → adapter propagates as the second return.
- nil broker (lazy lookup returns nil) → adapter returns
  `OAuthCancelled` + a clear error.

### Task 5 — Subagent inheritance + permission/hook composition

#### 5.1 Subagent passthrough

`internal/agent/spawn.go` — add the line near the existing inherit
options (~line 87):

```go
WithMcpManager(a.toolState.McpManager()), // share the parent's live MCP sessions
```

Subagents do NOT advertise the MCP catalog by default — same posture as
skills (`AdvertiseSkills: false` on Explore/General/Plan). The MCP
factories are registered on `DefaultRegistry()` though, so a subagent
profile that opts in by listing `mcp__server__tool` names in its
`DeferredTools` slice (e.g. a custom disk persona with explicit MCP
access) will see them.

#### 5.2 Permission rules

No code change needed. `permission.Store.Decide(name, ...)` accepts an
arbitrary tool name; the rule grammar (`pkg/permission/rule.go`) is a
glob match on the wire name. Document under "Permission rules" in
`docs/extending.md` (Task 8) that rules of the form
`mcp__filesystem__write_file` work, as do globs:
`mcp__filesystem__*`, `mcp__*`.

The default permission mode for unknown tools is `BehaviorAsk` — MCP
tools inherit that. Users opt to permanent allow by adding rules.

#### 5.3 Hook integration

No code change needed. `pkg/hooks/dispatcher.go:FirePreToolUse` takes
`toolName string`; the MCP tool name flows through unchanged. The
`matcher` glob in settings.json works against MCP names:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "mcp__**__write_*",
      "hooks": [{"type": "command", "command": "..."}]
    }]
  }
}
```

This is **valuable** for ops use cases: an org can run a redaction or
audit hook on every MCP write across every server with one rule. Call
this out in `setup-hooks/SKILL.md` (a v1.4 skill) as a "v1.6 onwards"
example.

### Task 6 — OAuth flow

`pkg/mcp/oauth.go`:

**Package layering constraint.** `pkg/mcp` is a public package and the
package import direction in evva is strict: `pkg/*` MUST NOT import
anything under `internal/*`. The OAuth flow conceptually wants to
"ask the user a question," but `internal/question` is private — and
making the whole `question.Question` / `Option` / `Broker` /
`Response` surface public (option (a) from the reviewer's note) would
promote a UX primitive prematurely without a downstream consumer
asking for it. So `pkg/mcp` defines its own narrow callback shape and
the host (`internal/agent`) adapts it to a `question.Broker`:

```go
package mcp

import (
    "context"
    "errors"
    "fmt"
    "log/slog"

    "github.com/modelcontextprotocol/go-sdk/auth"
)

// OAuthPrompt is the data an MCP-driven OAuth flow needs to surface to
// the end user. The host receives this struct via OAuthPromptFn, shows
// it to the user however it sees fit (TUI dialog, ask_user_question,
// custom UI, headless allow-list), and returns whether the user
// completed the in-browser flow or cancelled.
//
// pkg/mcp deliberately does NOT import internal/question. Callers
// adapt the OAuthPromptFn signature into whatever question/broker
// shape their host uses; the bundled cmd/evva does this in
// internal/agent/mcp_wiring.go (Task 4.2).
type OAuthPrompt struct {
    Server  string // MCP server name from settings.json
    AuthURL string // URL the user must open in their browser
}

// OAuthPromptResult is the user's decision after seeing an OAuthPrompt.
type OAuthPromptResult int

const (
    // OAuthCompleted means the user reports the browser flow finished;
    // the SDK's local callback server has captured the code+state.
    OAuthCompleted OAuthPromptResult = iota
    // OAuthCancelled aborts the connect — the server stays needs-auth
    // until the user retries via the per-server authenticate tool.
    OAuthCancelled
)

// OAuthPromptFn is the seam the host installs to surface auth URLs to
// the user. Returning OAuthCancelled aborts the auth. Returning a
// non-nil error is treated as a transport failure — the connect fails
// and the server's status moves to failed.
type OAuthPromptFn func(ctx context.Context, prompt OAuthPrompt) (OAuthPromptResult, error)

// OAuthHandler wraps the SDK's auth.AuthorizationCodeHandler with one
// piece of evva-specific glue: the URL the SDK derives from the
// server's OAuth metadata is routed through promptFn so a human (or
// any other prompt sink the host chose) can confirm completion.
type OAuthHandler struct {
    serverName string
    logger     *slog.Logger
    // promptFn is the host-installed callback. The Manager passes a
    // closure that defers to ToolState.QuestionBroker() at call time,
    // so this slot stays non-nil even when the broker isn't wired yet
    // at agent construction (see Task 4.2 "Lazy broker on Handler").
    promptFn OAuthPromptFn
}

func NewOAuthHandler(server string, logger *slog.Logger, promptFn OAuthPromptFn) *OAuthHandler {
    return &OAuthHandler{serverName: server, logger: logger, promptFn: promptFn}
}

// SDKHandler returns the SDK-shaped handler we set on
// StreamableClientTransport.OAuthHandler.
func (h *OAuthHandler) SDKHandler() *auth.AuthorizationCodeHandler {
    handler, _ := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
        // The SDK manages the local callback server and picks a free
        // port automatically. Redirect URL must match what the server
        // expects; for the canonical OAuth 2.1 + PKCE flow with the
        // 2025-03-26 spec, callback at http://127.0.0.1:<port>/callback
        // is the convention.
        RedirectURL:              "http://127.0.0.1:0/callback",
        AuthorizationCodeFetcher: h.fetchCode,
    })
    return handler
}

func (h *OAuthHandler) fetchCode(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
    if h.promptFn == nil {
        return nil, errors.New("oauth: no prompt callback installed; cannot surface auth URL to user")
    }
    result, err := h.promptFn(ctx, OAuthPrompt{
        Server:  h.serverName,
        AuthURL: args.URL,
    })
    if err != nil {
        return nil, fmt.Errorf("oauth: prompt: %w", err)
    }
    if result == OAuthCancelled {
        return nil, errors.New("oauth: user cancelled auth")
    }
    // The SDK's callback server has stashed the code/state for us; the
    // SDK reads them once we return without an explicit code/state.
    // (As of SDK v1.6, AuthorizationCodeFetcher may either return the
    // code/state explicitly OR let the SDK handle the callback — verify
    // current behavior in the SDK README before implementing.)
    return &auth.AuthorizationResult{}, nil
}
```

**Host-side adapter (lives in `internal/agent/mcp_wiring.go`):**

```go
package agent

import (
    "context"
    "fmt"

    "github.com/johnny1110/evva/internal/question"
    "github.com/johnny1110/evva/pkg/mcp"
)

// mcpPromptViaQuestion adapts evva's internal question.Broker into the
// host-agnostic mcp.OAuthPromptFn shape pkg/mcp exposes. The closure
// captures brokerFn (not the Broker itself) so the indirection stays
// late-bound — the broker may not exist at agent construction time
// (see boot ordering note below).
func mcpPromptViaQuestion(brokerFn func() question.Broker) mcp.OAuthPromptFn {
    return func(ctx context.Context, p mcp.OAuthPrompt) (mcp.OAuthPromptResult, error) {
        b := brokerFn()
        if b == nil {
            return mcp.OAuthCancelled, fmt.Errorf("question broker not installed")
        }
        q := question.Question{
            Header: "MCP " + p.Server,
            Question: fmt.Sprintf(
                "Open this URL in your browser to authorize the %s MCP server:\n\n%s\n\nClick \"I'm done\" once you've completed the flow.",
                p.Server, p.AuthURL,
            ),
            Options: []question.Option{
                {Label: "I'm done", Description: "I completed the auth in my browser"},
                {Label: "Cancel", Description: "Don't connect this server right now"},
            },
        }
        resp, err := b.Ask(ctx, q)
        if err != nil {
            return mcp.OAuthCancelled, err
        }
        if resp.Selected == "Cancel" {
            return mcp.OAuthCancelled, nil
        }
        return mcp.OAuthCompleted, nil
    }
}
```

**Boot-ordering note (unchanged from the previous draft, just routed
through `OAuthPromptFn` instead of a broker pointer):** the question
broker is created inside `internal/agent.wireBrokers` (~line 425),
which runs AFTER the agent constructor has already opened MCP
connections. Two integration options were considered:

1. **Defer the manager Open** until after `wireBrokers` runs.
2. **Lazy late-bind** the prompt — pass `mcpPromptViaQuestion(func()
   question.Broker { return a.toolState.QuestionBroker() })` into the
   manager so the broker is read at OAuth-time, not connect-time.

Use option 2 — it keeps boot ordering unchanged and the broker is
naturally lazy (only matters when an HTTP server actually triggers a
401, which is a tool-call event, far after agent boot).

**Token persistence:** the SDK's `AuthorizationCodeHandler` handles
refresh tokens internally; for v1.6 we accept "tokens live in
SDK-managed memory only, re-auth on every session restart." Disk
persistence (mirroring ref's `auth.ts` cache) is §6 — defer until
users hit it.

### Task 7 — Tests

Place tests next to the code. this phase's test surface is larger than v1.1
because the SDK boundary and the dynamic-factory channel both need
careful coverage.

**`pkg/mcp/` unit tests:**
- `normalize_test.go` — `NormalizeName` round-trip on a sample set
  (lowercase ASCII passes through, dots / spaces / @ become `_`).
- `stringutils_test.go` — `BuildToolName` / `ParseToolName` invariants;
  empty inputs; the double-underscore-in-server limitation produces a
  documented split.
- `envexpand_test.go` — every branch from §2.4.
- `config_test.go` — every branch from §2.5.
- `result_test.go` — text concat, image block round-trip via
  `tools.NewImageResult`-shaped output, embedded resource → blob path
  with `evvaHome` set, and the "no AppHome configured" fallback note
  when `evvaHome` is empty. Asserts no blob is written to disk in the
  empty-`evvaHome` case.
- `oauth_test.go` — `OAuthPromptFn` round-trip: returning `OAuthCompleted`
  yields an `auth.AuthorizationResult{}`; returning `OAuthCancelled`
  surfaces a user-cancel error; returning a non-nil error from the
  prompt is propagated to the SDK as a transport error. The host
  adapter (`internal/agent/mcp_wiring_test.go`) covers the broker side.
- `errormatch_test.go` — **`TestErrorMatchers_PinSDKShape`**, the
  load-bearing canary for the string-match heuristics in
  `isSessionExpired` / `isAuthError`. Stand up a tiny HTTP server
  (`httptest`) that returns the exact shapes the SDK currently
  reports: `404` + body `{"error":{"code":-32001,"message":"Session
  not found"},...}` for session-expired, plain `401 Unauthorized` for
  auth-required. Drive a real `StreamableClientTransport` against
  it, capture the SDK's error value verbatim, and assert
  `isSessionExpired(err) == true` / `isAuthError(err) == true`. The
  test pins the SDK's current error format — when SDK upstream
  changes the message wording (see open issue #579), this test goes
  red and the matcher gets updated. **Without this test, an SDK minor
  version bump silently misclassifies retries and auth flows.**

**`pkg/mcp/integration_test.go`** (uses a tiny in-process test server):
- Build a `testdata/stdio-echo-server` Go binary that ships one
  `echo` tool. The test compiles it on first run (or via `go test
  -count=1`), spawns it as the stdio transport.
- Assertions: Open → 1 Connected server with 1 tool; `DiscoveredToolNames`
  returns `[mcp__echo__echo]`; `RegisterFactories` succeeds; calling the
  factory's `Execute` round-trips a string through the server.

**`internal/agent/mcp_integration_test.go`** — full-loop smoke test:
- Build an agent with a stub LLM that emits one `tool_search` call (to
  fetch the echo tool's schema) followed by an `mcp__echo__echo` call.
- Assert: the deferred allowlist includes the echo tool; `tool_search`
  returns the echo tool's schema; `Execute` round-trips and the model's
  next turn sees the result.

**`internal/agent/mcp_switch_profile_test.go`** — pin for **acceptance
criterion A16** (`/profile` switch preserves MCP tools). Build an
agent with the stub echo MCP server connected. Trigger `SwitchProfile`
to a different main-tier persona (use the registry pattern from
`internal/agent/agent_test.go` to seed a second disk persona that
overlaps the Main tool list). Assert:
- The new profile's `DeferredNames` still includes `mcp__echo__echo`
  (the two-phase prompt build re-overlays MCP names onto whatever
  `Profile.DeferredTools` the new persona declared).
- A subsequent `tool_search` for the MCP tool still returns its schema.
- A subsequent `Execute` against the MCP tool factory still works
  (no re-connect — the `*mcp.Manager` is shared across profile
  switches via `ToolState`).

**`internal/agent/sysprompt/` test addition:** assert that when the
agent is built with a non-empty manager, the rendered Main prompt's
`<available-deferred-tools>` block contains the `mcp__*` names. Use a
mocked manager that returns a fixed list.

**`internal/agent/mcp_wiring_test.go`** — covers `mcpPromptViaQuestion`
per §4.6: broker returns "I'm done" → adapter returns
`mcp.OAuthCompleted`; broker returns "Cancel" → `mcp.OAuthCancelled`;
broker errors → adapter propagates; nil broker (lazy lookup returns
nil) → adapter returns `OAuthCancelled` + a clear "broker not
installed" error.

### Task 8 — Docs + version

**`pkg/version/version.go`** — bump `Version` from `"1.5.0"` (the
expected post-v1.2 value) to `"1.6.0"`. If v1.5 has not shipped yet at
the time of this PR, coordinate with that PR's author or land both
bumps together.

**`CHANGELOG.md`** — `## [v1.6.0] — MCP client support` entry. Sketch:

```markdown
## [v1.6.0] — MCP client support

Ships evva's Model Context Protocol client. Configure MCP servers under
`mcpServers` in `.evva/settings.json` (project) or
`<APP_HOME>/settings.json` (user); every discovered tool appears as
`mcp__<server>__<tool>` in the deferred-tool catalog and is loadable via
`tool_search`. Tool calls compose with the permission gate and the
v1.1 hooks engine.

Sequencing: this is the Phase MCP work (v1.3 in CLAUDE.md). It follows v1.2 (OpenAI) in roadmap order; both
shipped after v1.4 (bundled skills) under the CTO directive that moved
v1.4 ahead. Roadmap phase numbers in CLAUDE.md remain stable; tag order
is v1.0 → v1.1 → v1.4 → v1.5 → v1.6.

### Added

- **`pkg/mcp`** — public Experimental-tier MCP client package. Exports
  `Config`, `ServerConfig`, `ServerStatus`, `Manager`, `Open`, `Load`,
  `NormalizeName`, `BuildToolName`, `ParseToolName`, `ExpandEnv`,
  `OAuthHandler`, `ConvertResult`, plus the public `NewListResourcesTool`
  / `NewReadResourceTool` factories.
- **`agent.WithMcpManager`** — SDK opt-in for hosts that construct the
  manager themselves. Auto-loaded by the one-call `agent.New` when
  omitted.
- **Two new deferred tools**: `list_mcp_resources`, `read_mcp_resource`.
- **Dynamic tool registration**: every discovered MCP tool registers a
  pubtoolset.DefaultRegistry factory under
  `mcp__<server>__<tool>` and lands in the per-agent deferred
  allowlist before the system prompt is built.
- **Transports**: stdio (subprocess) and Streamable HTTP (2025-03-26
  spec). SSE-only, WebSocket, SDK, SSE-IDE, WS-IDE, claudeai-proxy are
  out of scope (see docs/roadmap/v1/v1-3-mcp.md §6).
- **OAuth**: HTTP servers requiring OAuth surface an
  `mcp__<server>__authenticate` tool; invoking it prompts the user with
  the auth URL via the question broker and completes via the SDK's
  AuthorizationCodeHandler.

### Changed

- `internal/agent.New` now restructures the profile/sysprompt build
  into two phases so MCP-discovered names can extend `Profile.DeferredTools`
  before the prompt's `<available-deferred-tools>` block is rendered.
  No public API change.

### Notes

- Dependency added: `github.com/modelcontextprotocol/go-sdk` v1.6.x
  (Apache 2.0). The protocol layer (JSON-RPC, session-id header,
  resumability, OAuth flow) is delegated to the SDK; evva owns the
  policy layer (config loading, status tracking, dynamic factory
  registration, OAuth broker bridge, result conversion).
- Public surface ships at the **Experimental** stability tier — may
  flex in v1.7+ once downstream usage data arrives.
```

**`docs/sdk-stability.md`** — add a `pkg/mcp` row under Experimental:

> | `pkg/mcp` | MCP client (Model Context Protocol) — Manager, ServerConfig, OAuth bridge, result conversion. Wraps the official `modelcontextprotocol/go-sdk` for the protocol layer. Surface may flex as downstream usage exercises edge cases (transport quirks, OAuth token persistence, result-type expansion). |

**`docs/extending.md`** — new top-level section `## MCP servers`,
placed between `## Lifecycle hooks` and `## What you can't change`:

```markdown
## MCP servers

evva consumes Model Context Protocol servers as a source of tools and
resources. Configure them under `mcpServers` in the same settings.json
files that hold hooks:

- Project: `<workdir>/.evva/settings.json`
- User: `<APP_HOME>/settings.json` (typically `~/.evva/settings.json`)

### settings.json shape

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/work"],
      "env": {"NODE_OPTIONS": "--enable-source-maps"}
    },
    "github": {
      "type": "http",
      "url": "https://api.example.com/mcp",
      "headers": {"X-Custom-Header": "${MY_HEADER_VAR:-default}"}
    }
  }
}
```

Per-server fields:
- `type`: `"stdio"` or `"http"`. Inferred from `command` (→stdio) or
  `url` (→http) when omitted.
- `command`, `args`, `env`: stdio only. `${VAR}` and `${VAR:-default}`
  expansion happens at load time.
- `url`, `headers`: http only.
- `timeout`: connect timeout in seconds; default 30, max 600.
- `disabled`: skip this server entirely (no subprocess, no HTTP).

### Tool naming

Every discovered tool becomes `mcp__<server>__<tool>` in evva's tool
catalog (lowercased and char-sanitized). Permission rules and hooks
target this fully-qualified name:

```json
{
  "permissions": {
    "alwaysAllow": ["mcp__filesystem__read_file"],
    "deny": ["mcp__filesystem__delete_file"]
  },
  "hooks": {
    "PreToolUse": [{"matcher": "mcp__**__write_*", "hooks": [{"type":"command","command":"./audit.sh"}]}]
  }
}
```

### OAuth-protected HTTP servers

When an HTTP server returns 401 on initial connection, evva flags it as
`needs-auth` and registers a one-off `mcp__<server>__authenticate` tool.
The model invokes it on the user's behalf; evva prompts the user with
the auth URL (via the question broker) and waits for confirmation. Once
auth completes, evva reconnects and the server's real tools become
available.

### SDK usage

Downstream hosts that construct agents via `pkg/agent.NewWithProfile`
opt into MCP with `WithMcpManager`:

```go
cfg, warns := mcp.Load(workdir, evvaHome)
mgr, openWarns := mcp.Open(ctx, cfg, mcp.OpenOptions{
    Logger:   logger,
    EvvaHome: evvaHome,
    // OAuthPrompt: yourPromptFn,  // optional; nil disables OAuth flow.
    //                              // Hosts that want the bundled
    //                              // ask_user_question flow should
    //                              // build their own adapter that
    //                              // bridges OAuthPromptFn to whatever
    //                              // UI primitive they use.
})
mgr.RegisterFactories(pubtoolset.DefaultRegistry())

ag, _ := agent.NewWithProfile(prof,
    agent.WithMcpManager(mgr),
)
```

The one-call `pkg/agent.New` loads + opens the manager automatically,
including wiring `OAuthPrompt` to `internal/agent.mcpPromptViaQuestion`
so the bundled `ask_user_question` flow handles OAuth out of the box.
A nil manager is safe — the resource tools and dynamic factories just
have nothing to surface.

### Out of scope (v1.6 first cut)

The first cut deliberately omits sampling, prompts, roots, elicitation,
SSE-IDE / WebSocket / SDK transports, plugin-provided servers, and
disk-persisted OAuth tokens. See
`docs/roadmap/v1/v1-3-mcp.md` §6 for the full out-of-scope list and the
follow-up phase candidates.
```

**`docs/user-guide/en/user-guide.md`** + **zh-tw mirror** — new `## MCP
servers` (zh-tw: `## MCP 伺服器`) section: settings.json example, how to
list configured servers (read the logger output for "mcp: …" lines —
a `/mcp` panel ships in a future minor), how `/<tool_name>` invocation
works via tool_search. Link to `docs/extending.md#mcp-servers` for the
full reference.

**`CLAUDE.md`** — no roadmap edits beyond what v1.4 already noted.

---

## 5. Design decisions & risks (read before coding)

- **Depend on the official SDK; do not hand-roll JSON-RPC.** The
  protocol surface is too large and too live to maintain in our repo.
  The SDK is Apache 2.0, v1.6.x stable, maintained by Google + the
  modelcontextprotocol org, and tracks the spec. Total transitive
  weight is modest (Verified via `go mod why` against current `go.mod`
  on a SDK-vendored branch — < 10 new modules, all stdlib-adjacent).
- **Two transports, not seven.** Stdio + Streamable HTTP cover the
  overwhelming majority of real-world MCP deployments. SSE-only is
  deprecated (the spec replaced it with Streamable HTTP in 2025-03-26
  — Streamable HTTP can still upgrade to SSE for streaming on top of
  POST). WebSocket is not in the current spec. SDK / SSE-IDE / WS-IDE /
  claudeai-proxy are vendor-specific. Adding them is each a contained
  follow-up — the `transport.go` switch grows linearly.
- **Dynamic factories on the global registry, not a parallel namespace.**
  We considered a separate `mcpRegistry` parallel to
  `pubtoolset.DefaultRegistry`. Rejected because every consumer of tool
  factories (toolset.Build, agent.ResolveTool, MarkDiscovered,
  toolset.Describe) would need a dual-lookup path. Using the existing
  registry means: zero changes in those four sites, MCP names compose
  naturally with `tool_search`, the deferred allowlist guards the wire
  surface, and the registry's existing "duplicate registration is an
  error" check protects us from accidental double-registration.
- **Names are runtime-discovered.** `tools.ToolName` is
  `type ToolName string` — no enum check, no compile-time constraint.
  Runtime registration of `mcp__filesystem__read_file` is type-safe.
  The only risk: a typo in a settings.json server name produces a
  ghost prefix; documented in user-guide.
- **`pkg/mcp` exposes its own `OAuthPromptFn`; it does NOT import
  `internal/question`.** evva's package layering forbids `pkg/*` →
  `internal/*` imports. Two options to solve the OAuth-prompt seam:
  (a) promote `Question`/`Option`/`Broker` to a new public
  `pkg/question` package, or (b) define an MCP-specific narrow
  callback in `pkg/mcp` and let the host adapt. We chose (b) for v1.6
  because no downstream consumer has yet asked for a public
  `ask_user_question` primitive — promoting one preemptively risks
  freezing the wrong shape (the bundled types still have UI flex). The
  adapter `mcpPromptViaQuestion` lives in `internal/agent` (one ~30-line
  file) and trivially converts between the two. If a future phase
  needs `pkg/question`, (b) is forward-compatible: the adapter shrinks
  to a one-line type alias.
- **The two-phase profile/sysprompt build is the only invasive change.**
  Today `agent.Main(...)` builds tool lists AND the system prompt in
  one call (`profiles.go:163`). To inject MCP-discovered names into the
  `<available-deferred-tools>` block we need the deferred set to be
  known when the prompt builds. Two options were considered:
  (a) thread `*Manager` into `Main()`'s signature, or
  (b) split `Main()` into a tool-list-only phase + a prompt-build phase
  the agent runs second.
  We chose (b) — `Main()`'s signature stays stable, and SwitchProfile
  (which rebuilds the prompt on persona change) takes the same path.
- **OAuth via the question broker, not a hard-coded UI.** Routing the
  auth URL through `question.Broker.Ask` means it works against every
  evva front-end (bundled TUI, custom UIs, headless test harness).
  Token persistence is deferred (§6) — the SDK handles refresh
  in-memory; users re-auth on session restart in this phase. Add disk
  persistence when usage data shows it's needed.
- **Result truncation at 100k chars.** Mirrors ref's
  `MCPTool.maxResultSizeChars`. Some MCP servers can return multi-MB
  results (filesystem reads on large files); truncating saves tokens
  and prevents agent context blow-ups. Above the cap we append a
  `[truncated N chars]` marker. The model can ask the server for a
  smaller window (most servers expose pagination via params).
- **Binary blobs land on disk, not in the prompt.** `read_mcp_resource`
  and tool calls returning embedded binary content write the bytes to
  `<APP_HOME>/mcp-blobs/<uuid>` (mode 0600) and return a `[binary
  content saved at <path>]` line. The model can `read` the file via
  evva's fs tools if it needs to. Simpler than ref's mime-derived
  filenames; defer mime-sniffing if it matters.
- **Failure modes are non-fatal.** Bad command, unreachable URL,
  invalid env var — all become Warnings logged at boot. The agent
  starts; configured servers that DID connect work; the failed server
  is visible in `Manager.Status()` for a future `/mcp` panel.
  Half-broken `settings.json` (one good server, one malformed entry):
  the good one connects, the malformed one warns and is skipped — same
  policy as hooks loader.
- **Manager lifecycle bound to RootContext.** When the agent's
  `Shutdown()` cancels rootCtx, each `Manager.Shutdown` closes every
  active session, which terminates stdio subprocesses (SDK sends
  Cancel notification, then closes stdin → server exits) and HTTP
  sessions (sends MCP DELETE if session-id was assigned, otherwise
  closes the connection).
- **Subagents share live sessions, do NOT re-connect.** Saves time
  (stdio subprocess startup is the slow path) and avoids multiplying
  per-server resource usage. The `*Manager` is the shared state. Risk:
  if a subagent calls a stateful MCP tool that mutates server state,
  every sibling sees the new state. This is intentional — same risk as
  shared filesystem / shared HTTP backend. Document in the user-guide.
- **Race on registry registration.** `Manager.Open` runs server
  connects in parallel, and `RegisterFactories` runs after all complete
  (the `wg.Wait()` boundary in `Open` enforces this). So when the agent
  calls `RegisterFactories` synchronously after `Open` returns, the
  full tool set is known. No race window. (If we later make `Open`
  non-blocking, we'd add a `Manager.OnReady` callback — defer.)
- **SDK behavior may change in v1.7+.** The SDK is itself at
  Experimental→Stable transition. The 401/403 error detection in
  `isAuthError` is a string-match because the SDK has an open issue
  (#579) to expose structured HTTP error types. When that lands, swap
  to the structured check.
- **JSON-schema passthrough.** SDK `Tool.InputSchema` is `*JSONSchema`
  (or `map[string]any` depending on SDK version) — we marshal it
  directly into the tool's `Schema()` return. Anthropic's tool-use API
  accepts the JSON Schema dialect the SDK emits. No translation layer
  needed.

---

## 6. Out of scope for this phase (revisit in later phases)

Listed so contributors don't propose them as Phase MCP additions. Each
defers to a specific follow-up signal.

- **Sampling** (`CreateMessageHandler` — server requests an LLM
  completion from the client). High-complexity feature; v1.6 leaves
  the handler unwired (SDK returns "not supported" to the server).
  Revisit when a real server requires it.
- **Prompts** (`ListPrompts`, `GetPrompt` — server-provided prompt
  templates). Same posture as Sampling; defer.
- **Roots** (`RootsListChanged` — server queries client for fs roots).
  Server-side feature; few servers use it.
- **Elicitation** (`ElicitationHandler` — server prompts client for user
  input mid-call). Would need broker integration parallel to
  `ask_user_question`. Defer.
- **Progress notifications surfaced to the UI.** This phase logs them only.
  A real UI surface (progress bars in the TUI) is a v1.7+ TUI feature.
- **OAuth token disk persistence.** This phase keeps tokens in SDK memory;
  re-auth on session restart. Disk persistence (under
  `<APP_HOME>/mcp-tokens.json`, mode 0600) when users hit this.
- **Reconnect-on-server-restart.** This phase reconnects only on
  session-expired. A long-lived session that dies mid-conversation
  surfaces as a tool error; the user re-invokes. A future "watchdog
  reconnect" (poll connection health, auto-reconnect on transport
  close) is deferred.
- **WebSocket transport.** Not in the current MCP spec (the SDK
  exposes it as `mcpsdk.WebSocketClientTransport` but it's
  Experimental in the SDK too). Defer.
- **SSE-only legacy transport (2024-11-05 spec).** Deprecated by the
  newer Streamable HTTP. Defer until a real server requires it
  (none we know of as of May 2026).
- **SDK transport (in-process Claude Code SDK servers).** Claude Code
  internal; not relevant to evva.
- **claudeai-proxy and XAA (Cross-App Access).** Anthropic-specific
  product features. Out forever.
- **SSE-IDE / WS-IDE.** Anthropic IDE extension internals. Out forever.
- **Plugin-provided MCP servers.** evva doesn't have a plugin system
  yet. Out until plugins land.
- **`/mcp` TUI panel.** Live server status visualization, per-server
  enable/disable toggles, OAuth re-auth flow. Defer to v1.7+ TUI work.
- **`/setup-mcp` bundled skill.** v1.4's `setup-hooks` skill is the
  precedent — a model-driven authoring helper. We deliberately do NOT
  ship one in this phase to keep scope tight; the docs (Task 8) cover all
  authoring needs. v1.7+ can add it as a bundled skill content
  addition (no framework changes).
- **Hot reload.** Editing settings.json mid-session does not re-load
  MCP config. User restarts. Same posture as hooks, permissions, and
  skills — consistent with the rest of evva.
- **`prefetchAllMcpResources` cache warming.** Ref pre-fetches resource
  lists at boot for low latency on first `list_mcp_resources` call.
  Defer — `list_mcp_resources` is on the cold path (model invokes it
  occasionally, not on every turn).
- **`_meta`-driven hints** (`anthropic/searchHint`, `anthropic/alwaysLoad`).
  Ref reads tool `_meta` to surface search hints and force always-load.
  Defer — would require new fields on `tools.Descriptor` and
  always-load coordination with the deferred allowlist policy.
- **Result image down-sampling.** Ref runs `maybeResizeAndDownsampleImageBuffer`
  on image content before passing to the LLM. This phase passes through
  unchanged. Add when image-heavy MCP servers (screenshot tools)
  become common.
- **MCP server instructions injected into the system prompt.**
  Ref's `mcpInstructionsDelta.ts` appends server-supplied
  `instructions` text to the system prompt. Defer — most existing
  servers don't ship useful `instructions`; revisit when they do.
- **mTLS / proxy support.** The SDK has hooks; this phase doesn't expose
  config knobs. Add when needed.
- **Multi-tenant / sandboxed MCP execution.** Each agent shares one
  Manager. Multi-tenant evva (multiple isolated agent trees per
  process) would need per-tenant managers. Out of scope.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 0** — `go.mod` adds `github.com/modelcontextprotocol/go-sdk`
      at a pinned v1.6.x. `go mod tidy` produces a clean diff. PR
      commit message records the SDK SHA + any known-issue links being
      worked around.
- [ ] **Task 1** — `pkg/mcp` compiles in isolation
      (`go build ./pkg/mcp/...`). `doc.go` sets the package contract
      verbatim per §4.1.
- [ ] **Task 2** — `Config`, `ServerConfig`, `ServerStatus`,
      `NormalizeName`, `BuildToolName`, `ParseToolName`, `ExpandEnv`,
      `Load` all match the signatures in §4.2 / §4.3 / §4.4 / §4.5.
      Unit tests for each green.
- [ ] **Task 3** — `Manager`, `Client`, `Open`, `RegisterFactories`,
      `DiscoveredToolNames`, `Status`, `Shutdown` all match §4. The
      in-process echo-server integration test green.
- [ ] **Task 4** — `pkg/tools/name.go` carries `LIST_MCP_RESOURCES` +
      `READ_MCP_RESOURCE`; `internal/toolset/toolset.go` carries
      `mcpManager` + accessors; `internal/agent/options.go` exports
      `WithMcpManager`; `pkg/agent/options.go` re-exports it. The
      two-phase profile/sysprompt build lands cleanly with no public
      `agent.Main` signature change.
- [ ] **Task 5** — `internal/agent/spawn.go` passes the parent's
      Manager pointer. Permission and hook composition smoke tests
      green (one of each, against the echo server).
- [ ] **Task 6** — `oauth.go` plumbs the question broker; an HTTP test
      fixture that returns 401 round-trips through the OAuth flow.
- [ ] **Task 7** — `go test ./...` green. `pkg/mcp` has tests for
      every file listed in §4.7. `internal/agent/mcp_integration_test.go`
      runs the full loop end-to-end with the stub LLM.
- [ ] **Task 8** — `pkg/version.Version = "1.6.0"`. `CHANGELOG.md` has
      a `## [v1.6.0]` entry per §4.8. `docs/sdk-stability.md` lists
      `pkg/mcp` under Experimental. `docs/extending.md` carries the
      `## MCP servers` section. `docs/user-guide/en/user-guide.md`
      and zh-tw mirror both have `## MCP servers` / `## MCP 伺服器`.
- [ ] **Acceptance A1–A15** — every criterion from §3 demonstrably
      passes. Use the in-process echo server for A1–A4, A11; a
      tiny test fixture HTTP server for A5, A9; manual smoke against a
      real reference server (e.g.
      `@modelcontextprotocol/server-filesystem`) for the PR description.
- [ ] **Manual (TTY / network needed — flag for a human):**
      Configure `@modelcontextprotocol/server-filesystem` in
      `~/.evva/settings.json` pointing at `/tmp/evva-test`; start
      `evva`; ask the agent to list and read a file in `/tmp/evva-test`;
      confirm `mcp__filesystem__read_file` appears via `tool_search`
      and executes. Verify in `evva` logs: one `mcp: connect` info
      line per server; permission gate fires on first write; PreToolUse
      hook with matcher `mcp__filesystem__*` fires when configured.
