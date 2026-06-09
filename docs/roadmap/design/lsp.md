# LSP Module Integration — Feasibility Analysis & Development Plan

> **Status:** Revised (v1.1) — incorporates feedback from 3-agent review  
> **Date:** 2026-05-24  
> **Author:** evva (coding agent)  
> **Target:** evva v0.4.x or later

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Reference Architecture Analysis](#2-reference-architecture-analysis)
3. [evva Architecture Gap Analysis](#3-evva-architecture-gap-analysis)
4. [Feasibility Assessment](#4-feasibility-assessment)
5. [Technical Design](#5-technical-design)
   - 5.1 [Package Layout](#51-new-package-layout)
   - 5.2 [Daemon Integration](#52-daemon-integration)
   - 5.3 [JSON-RPC Transport](#53-json-rpc-transport-design)
   - 5.4 [Server Lifecycle](#54-server-lifecycle-design)
   - 5.4.1 [Concurrency Design (singleflight)](#541-concurrency-design-for-lazy-server-start)
   - 5.5 [Manager](#55-manager-design)
   - 5.6 [Tool Design](#56-tool-design)
   - 5.7 [Diagnostics](#57-diagnostics-design)
   - 5.8 [Document Synchronization Model](#58-document-synchronization-model)
   - 5.9 [Cancellation Model](#59-cancellation-model)
   - 5.10 [Response Truncation & Context Budget](#510-response-truncation--context-budget)
   - 5.11 [Observability & Structured Logging](#511-observability--structured-logging)
   - 5.12 [Configuration Format](#512-configuration-format)
   - 5.13 [Agent Profile Integration](#513-integration-with-agent-profiles)
   - 5.14 [Initialization Handshake](#514-initialization-handshake)
6. [Implementation Plan](#6-implementation-plan)
7. [Risk Analysis & Mitigation](#7-risk-analysis--mitigation)
8. [Open Questions](#8-open-questions)
9. [Appendix: Key Reference Files](#9-appendix-key-reference-files)

---

## 1. Executive Summary

### 1.1 What is an LSP Module?

An LSP (Language Server Protocol) module would give evva deep, semantic understanding of code. Instead of grepping for strings or guessing symbol locations, the agent could ask a language server running as a subprocess: "Where is `UserService` defined?", "Show me all callers of `authenticate`", "What type is this variable?" — and get precise, compiler-grade answers.

This is a significant capability upgrade. Currently evva's code exploration relies on `grep`, `glob`, `read`, and `tree` — lexical tools that can't reason about scope, types, or symbol relationships. An LSP module bridges that gap.

### 1.2 Bottom Line

**Feasible, with low-to-medium complexity for a core MVP (Phase 1).** evva's daemon infrastructure, tool system, and event bus already provide the plumbing needed — no architectural rewrites required. The primary new work is a Go-native JSON-RPC 2.0 over stdio transport and LSP protocol type definitions.

Estimated scope (two tiers):

| Tier | Scope | Lines (Go) | Calendar |
|---|---|---|---|
| **Functional MVP** | 4 operations, single server, happy path | ~2,100–3,000 | ~3 weeks |
| **Production-grade** | + diagnostics, cancellation, sync model, truncation, mock tests, multi-server edge cases | ~4,000–5,500 | ~6–8 weeks |

> **Disclaimer:** These estimates are based on codebase analysis and reference implementation study, not on running real LSP servers against the planned Go transport layer. Integration testing with gopls, rust-analyzer, and typescript-language-server typically surfaces 1–2 weeks of unanticipated work (server-specific quirks, cold-start indexing behavior, edge-case response shapes). Treat the upper bound as more realistic for a deliverable that handles real-world projects.

### 1.3 Recommended Approach

A phased rollout:

| Phase | Scope | Effort (MVP) | Effort (Prod) | Delivers |
|---|---|---|---|---|---|
| **Phase 1 — Core LSP Client** | JSON-RPC transport, server lifecycle, concurrency-safe lazy start, 4 operations, mock test server | ~3 weeks | ~4–5 weeks | Agent can query LSP servers |
| **Phase 2 — Diagnostics** | Passive `textDocument/publishDiagnostics`, dedup, volume limiting, sync model, cancellation | ~1.5 weeks | ~2–3 weeks | Real-time error/warning feedback; safe concurrent edits |
| **Phase 3 — Advanced Operations** | Workspace symbols, call hierarchy, go-to-implementation, response truncation | ~1.5 weeks | ~2 weeks | Full feature parity with Claude Code |
| **Phase 4 — Server Discovery** | Auto-detection, graceful error messages, observability, docs | ~1–2 weeks | ~2–3 weeks | Zero-config LSP; production telemetry |

---

## 2. Reference Architecture Analysis

The reference implementation (Claude Code, TypeScript) uses a five-layer architecture spanning ~4,500 lines across 16 files. Below is a detailed breakdown.

### 2.1 Layer Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  LSPTool.ts              AI-Facing Tool Layer                │
│  - 9 operations (definition, references, hover, symbols...)  │
│  - Input validation, filesystem permissions, gitignore filter│
│  - Waits for server init, opens files via didOpen            │
│  - Formats results for LLM display                           │
└──────────────────────────┬───────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────┐
│  manager.ts              Global Singleton Orchestrator       │
│  - initializeLspServerManager() / shutdown / reinitialize    │
│  - isLspConnected() for tool enablement                      │
│  - waitForInitialization() API                               │
└──────────────────────────┬───────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────┐
│  LSPServerManager.ts     File-to-Server Router               │
│  - extension → server name → LSPServerInstance map           │
│  - ensureServerStarted(filePath) — lazy start                │
│  - didOpen / didChange / didSave / didClose sync             │
│  - workspace/configuration handler                           │
└──────────────────────────┬───────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────┐
│  LSPServerInstance.ts    Single Server Lifecycle             │
│  - State machine: stopped→starting→running→stopping→error    │
│  - Crash recovery (maxRestarts cap)                          │
│  - Request retry with exponential backoff (ContentModified)  │
│  - Startup timeout racing                                    │
└──────────────────────────┬───────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────┐
│  LSPClient.ts            JSON-RPC Transport                  │
│  - spawn() child process (stdio pipes)                       │
│  - vscode-jsonrpc (StreamMessageReader/Writer)               │
│  - initialize handshake (capabilities exchange)              │
│  - sendRequest / sendNotification / onNotification           │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 LSP Operations Implemented

| Operation | LSP Method | Two-Step? |
|---|---|---|
| `goToDefinition` | `textDocument/definition` | No |
| `findReferences` | `textDocument/references` | No |
| `hover` | `textDocument/hover` | No |
| `documentSymbol` | `textDocument/documentSymbol` | No |
| `workspaceSymbol` | `workspace/symbol` | No |
| `goToImplementation` | `textDocument/implementation` | No |
| `prepareCallHierarchy` | `textDocument/prepareCallHierarchy` | Yes (→ incoming/outgoing) |
| `incomingCalls` | `callHierarchy/incomingCalls` | Yes |
| `outgoingCalls` | `callHierarchy/outgoingCalls` | Yes |

**Notably absent:** Completion, code actions, signature help, code lens, formatting — these were deemed lower priority for a coding agent that primarily reads and navigates code.

### 2.3 Diagnostics Pathway (Passive, Async)

```
LSP Server ──publishDiagnostics──► passiveFeedback.ts
  └─ formatDiagnosticsForAttachment()
  └─ registerPendingLSPDiagnostic()  → LSPDiagnosticRegistry
  └─ checkForLSPDiagnostics()        → query pipeline
  └─ deduplication + volume limiting → delivered as attachments
```

Key limits in the reference:
- **Per-file cap:** 10 diagnostics
- **Total cap:** 30 diagnostics
- **Dedup:** Hashing `{message, severity, range, source, code}`; cross-turn LRU of 500 delivered keys
- **Delivery:** Attachments injected into the conversation context

### 2.4 Server Configuration

LSP servers are discovered from plugins, not hardcoded:

```typescript
// Config shape
interface LspServerConfig {
  command: string                          // e.g. "typescript-language-server"
  args?: string[]                          // e.g. ["--stdio"]
  extensionToLanguage: Record<string, string>  // e.g. { ".ts": "typescript" }
  transport?: 'stdio' | 'socket'          // only stdio implemented
  env?: Record<string, string>
  initializationOptions?: unknown
  startupTimeout?: number
  maxRestarts?: number                    // default 3
}
```

Server names are scoped: `plugin:{pluginName}:{serverName}` to avoid collisions.

### 2.5 Key Design Decisions Worth Porting

1. **Lazy server start** — servers launch only when `ensureServerStarted()` is called for a concrete file, not at agent boot. This avoids starting 10+ servers unnecessarily.
2. **State machine per server** — explicit `stopped | starting | running | stopping | error` states prevent race conditions during concurrent tool calls.
3. **Crash recovery with cap** — servers that crash repeatedly hit `maxRestarts` and stop retrying. Prevents infinite restart loops.
4. **`ContentModified` retry** — rust-analyzer sends this during indexing; the reference retries with exponential backoff (500ms → 1000ms → 2000ms).
5. **Diagnostics dedup** — same diagnostic from same file/server isn't re-delivered across turns.
6. **Volume limiting** — diagnostics are capped per-file and globally so they don't flood the LLM context.

### 2.6 What NOT to Port

1. **Plugin-based server discovery (Phase 1)** — evva doesn't have a plugin system yet. Start with a simple config file.
2. **`vscode-jsonrpc` dependency** — evva is Go, not Node.js. A ~200-line JSON-RPC 2.0 implementation over stdio is sufficient.
3. **`workspace/configuration` handler** — the reference returns `null` for every config item. Skip entirely; evva can omit the capability declaration.
4. **React Ink UI components** — evva's TUI is Bubble Tea; LSP results render as standard tool result text. No special UI needed initially.

---

## 3. evva Architecture Gap Analysis

### 3.1 What Already Exists (Ready to Use)

| Capability | Where | Ready? |
|---|---|---|
| Long-running subprocess management | `pkg/tools/daemon/` — `Daemon` interface, `DaemonState`, `Kind*` constants, signal draining | Yes |
| Parallel tool execution | `internal/agent/loop.go` — goroutine-per-tool dispatch | Yes |
| Event publication to UI | `pkg/event/` — `Event` envelope, `Sink` contract | Yes |
| Tool registration & discovery | `pkg/toolset/` — `Registry`, `ToolName`, active/deferred/resolved phases | Yes |
| Permission gating | `internal/permission/` — gate, broker, matcher | Yes |
| Provider-agnostic LLM interface | `pkg/llm/` — `Client`, message types | Yes |
| Config loading | `pkg/config/` — YAML + env | Yes |
| User-facing tool descriptions | `pkg/tools/tool.go` — `Description()` method | Yes |

### 3.2 What Must Be Built

| Capability | Complexity | Notes |
|---|---|---|
| **JSON-RPC 2.0 transport over stdio** | Medium | ~200–400 lines. Go has no dominant library; write a focused one. Must handle header-based message framing (`Content-Length: ...\r\n\r\n`), request/response correlation, notification dispatch. |
| **LSP protocol types** | Low-Medium | ~300–500 lines of Go structs. Can be generated from the LSP metamodel or hand-written for the subset we need. `go.lsp.dev/protocol` exists but may be overkill. |
| **LSP server lifecycle manager** | Medium | ~400–600 lines. State machine, lazy start, crash recovery, file-to-server routing. Analogous to `LSPServerInstance.ts` + `LSPServerManager.ts` merged. |
| **LSP tool (`lsp_request` or similar)** | Medium | ~400–600 lines. Implements `tools.Tool`, dispatches to correct LSP method, formats results. |
| **Diagnostic delivery pathway** | Medium | ~300–500 lines. Daemon signal handler, dedup registry, volume limiting, context injection. |
| **LSP server config format** | Low | ~100–200 lines. YAML-based config file, extension-to-server mapping, command/args/env. |

### 3.3 What Does NOT Need to Change

- **Agent loop** (`internal/agent/loop.go`) — daemon signals already flow through `drainDaemonSignals()`. LSP diagnostics arrive as signals, no loop changes needed.
- **Event system** (`pkg/event/`) — existing `KindStoreUpdate` and `BgResult`/`DrainBackgroundTask` events carry daemon lifecycle updates. Add `LSPMeta` to daemon snapshot types.
- **UI contract** (`pkg/ui/`) — LSP results are text; UI renders them like any tool result.
- **LLM providers** — zero changes. The LLM sees LSP as just another tool.
- **Permission system** — existing `PreToolUse` hooks and gate can gate LSP server launches if needed.
- **Config system** — LSP config is a new YAML file, loaded alongside existing config.

---

## 4. Feasibility Assessment

### 4.1 Technical Feasibility: HIGH

Go's standard library provides everything needed:
- `os/exec` — subprocess spawning with stdin/stdout pipes (already used by `bash.go`)
- `bufio` — line-based reading for JSON-RPC header parsing
- `encoding/json` — message serialization
- `sync` — mutexes, WaitGroups for concurrent request tracking
- `context` — cancellation propagation

The JSON-RPC framing is straightforward: each message is prefixed with `Content-Length: N\r\n\r\n` followed by N bytes of JSON. A reader goroutine per server is sufficient.

### 4.2 Architectural Compatibility: HIGH

The LSP module fits naturally into evva's design:

```
LSP Server Process ──stdio──► lspDaemon (implements daemon.Daemon)
  ├─ JSON-RPC messages → responses routed to pending requests
  └─ notifications (diagnostics) → daemon signals → agent loop → LLM context

LSP Tool (implements tools.Tool)
  ├─ Agent calls lsp_request with {operation, filePath, line, character}
  ├─ Tool resolves server via extension map
  ├─ Tool ensures server started (lazy)
  └─ Tool sends LSP request → formats result → returns Result{Content, Metadata}
```

This mirrors the existing `bash` tool's relationship with `bashDaemon` — a proven pattern in the codebase.

### 4.3 Effort Estimate

**Functional MVP** (happy path, single server, 4 operations):

| Component | Lines (Go) | Days |
|---|---|---|
| JSON-RPC transport (in-house, ~200 lines) | 200–300 | 2–3 |
| LSP protocol types | 300–500 | 2–3 |
| Server lifecycle + manager (with singleflight lazy start) | 500–700 | 4–5 |
| LSP tool + formatters + truncation | 400–600 | 3–4 |
| Config format + loader | 100–200 | 1–2 |
| Mock LSP server for unit tests | 200–300 | 2 |
| Unit + integration tests | 400–600 | 2–3 |
| **Total (Phase 1 MVP)** | **~2,100–3,200** | **16–22** |

**Production-grade delta** (adds diagnostics, sync model, cancellation, multi-server, observability, real-server edge cases):

| Additional Component | Lines (Go) | Days |
|---|---|---|
| Diagnostics pathway (registry, dedup, volume limiting) | 300–500 | 2–3 |
| Document synchronization model | 200–400 | 2–3 |
| Cancellation model ($/cancelRequest, context propagation) | 100–200 | 1–2 |
| Response truncation & context budget enforcement | 100–200 | 1 |
| Observability (structured logging, telemetry hooks) | 100–200 | 1–2 |
| Real-server integration hardening (gopls, rust-analyzer, tsc) | 300–500 | 5–10 |
| **Total additional** | **~1,100–2,000** | **12–21** |
| **Grand total (production-grade)** | **~3,200–5,200** | **28–43** |

> **Note:** The largest uncertainty is "real-server integration hardening" (5–10 days). This is the work that emerges only after running against gopls on a large codebase, rust-analyzer during indexing, and typescript-language-server on a monorepo. Server-specific quirks cannot be fully anticipated from spec reading alone.

### 4.4 Key Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| JSON-RPC framing edge cases (chunked messages, malformed headers) | Medium | Medium | Use `io.ReadFull` for body after header; fuzz test with random chunk boundaries |
| Server-specific quirks (rust-analyzer ContentModified, gopls cold-start latency) | High | Medium | Port `ContentModified` retry with exponential backoff; test with top 3 servers |
| LSP server binary not installed | High | Low | Graceful error messages with install commands; Phase 4 auto-detection |
| Document state drift (agent edits, bash writes, git operations desync LSP server's view of files) | High | Medium | Phase 1: explicit didOpen/didClose per lsp_request call. Phase 2: full sync model (see §5.8) |
| Large response payloads flooding LLM context (workspace/symbol, references in monorepo) | Medium | High | Truncation policy: per-operation max results + total byte cap (see §5.10) |
| Thundering herd on lazy server start (multiple concurrent lsp_request calls trigger duplicate Start) | Medium | Medium | singleflight pattern: one in-flight start per server name, all callers share result (see §5.4.1) |
| Zombie processes (server ignores shutdown/exit, becomes orphaned) | Medium | Medium | Time-boxed shutdown (5s) → SIGKILL; `exec.Cmd` with `WaitDelay = 2s` (same pattern as bash tool) |
| UTF-16 position encoding for non-BMP characters | Low | Low | UTF-8 → UTF-16 offset converter (~30 lines); edge case for emoji in string literals |

### 4.5 Go Ecosystem Assessment

**JSON-RPC libraries evaluated:**

| Library | Pros | Cons |
|---|---|---|
| `net/rpc/jsonrpc` (stdlib) | No dependency; reliable | Not JSON-RPC 2.0; no notification support |
| `github.com/sourcegraph/jsonrpc2` | Full 2.0; async handler; used in production (Sourcegraph) | Extra dependency; might be heavier than needed |
| Write our own | Zero deps; precise fit; ~200 lines | Maintenance burden; bug surface |

**Recommendation: Write our own (~200 lines).** Reasoning:
- The framing layer (LSP's `Content-Length` header parsing) is not part of JSON-RPC 2.0 — any library still requires us to write the header reader/writer ourselves.
- `sourcegraph/jsonrpc2` solves async request correlation, but Go's `chan` + `map[id]chan response` does the same in ~50 lines.
- A self-contained implementation means zero external dependencies in the critical I/O path, easier fuzz testing, and complete control over error semantics (especially for LSP-specific error codes like `ContentModified`).
- If future needs (e.g., WebSocket transport) outgrow the in-house implementation, we can swap in a library behind the same `Client` interface — the interface surface is small enough that this is a low-risk migration path.

**LSP type libraries:**

| Library | Pros | Cons |
|---|---|---|
| `go.lsp.dev/protocol` | Complete LSP 3.17 types; generated from metamodel | Heavy dependency chain; may lag spec |
| Hand-written subset | Zero deps; only the types we use; lightweight | Manual maintenance; risk of omission |

**Recommendation:** Hand-write a minimal type set (~20 structs) covering the 9 operations we implement plus diagnostics. LSP types are stable and well-documented; the maintenance burden is low. This avoids a dependency that pulls in the entire LSP metamodel.

**LRU cache for diagnostic dedup:**

| Library | Pros | Cons |
|---|---|---|
| `github.com/hashicorp/golang-lru/v2` | Generics (`Cache[K, V]`); 5.1k stars, 1,261 importers; thread-safe | External dep; last release Sep 2023 (v2.0.7); overkill for bounded set use case |
| `container/list` + `map` (stdlib) | Zero deps; ~30 lines; same pattern as Go's groupcache/lru | Not a reusable generic type; must be written per use case |

**Recommendation: `container/list` + `map` internally.** The `DiagnosticRegistry` only needs `Contains` + `Add` with bounded eviction — a ~30-line `diagnosticKeySet` type (see §5.7). This keeps the LSP module's only external dependencies to `golang.org/x/sync` (singleflight). If the codebase later needs LRU in multiple places, switching to `hashicorp/golang-lru/v2` is a drop-in replacement behind the same two-method interface.

---

## 5. Technical Design

### 5.1 New Package Layout

```
pkg/tools/lsp/                      # Public LSP tool package
├── lsp.go                          # ToolName constants, Names(), family registration
├── client.go                       # JSON-RPC 2.0 transport over stdio
├── server.go                       # LSP server lifecycle (state machine, lazy start, crash recovery)
├── manager.go                      # Extension-to-server routing, file sync (didOpen/Change/Save/Close)
├── tool.go                         # tools.Tool implementation (lsp_request)
├── formatters.go                   # Result formatters for each operation
├── diagnostics.go                  # publishDiagnostics handler, dedup, volume limiting
├── config.go                       # LSP server config struct + YAML loader
├── protocol/                       # LSP protocol types
│   ├── types.go                    # Core types: Position, Range, Location, TextDocumentIdentifier...
│   ├── methods.go                  # Method constants + param/result types
│   └── capabilities.go             # ServerCapabilities, ClientCapabilities
├── client_test.go
├── server_test.go
├── manager_test.go
├── tool_test.go
└── diagnostics_test.go
```

### 5.2 Daemon Integration

Add `KindLSP` to the daemon system:

```go
// pkg/tools/daemon/kind.go
const (
    KindBash   DaemonKind = "local_bash"
    KindAgent  DaemonKind = "local_agent"
    KindMonitor DaemonKind = "monitor"
    KindLSP    DaemonKind = "lsp"        // NEW
)
// ID prefix: "l" (e.g., "l1", "l2")
```

The `lspDaemon` struct:

```go
type lspDaemon struct {
    id       string
    server   *LSPServer               // wraps client + lifecycle
    exitCode int
    err      error
    mu       sync.RWMutex
}
```

Implements `daemon.Daemon`:
- `Snapshot()` → `DaemonSnapshot{Kind: KindLSP, Status, Extras: LSPMeta{...}}`
- `Kill(ctx)` → sends `shutdown` + `exit` to LSP, kills process
- `Output()` → recent log output

### 5.3 JSON-RPC Transport Design

```go
// client.go — core transport

type Client struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout io.ReadCloser

    mu       sync.Mutex
    pending  map[json.RawMessage]chan *Response  // request ID → response channel
    handlers map[string]NotificationHandler      // method → handler
    nextID   int64

    connCtx    context.Context
    connCancel context.CancelFunc
}

func Start(ctx context.Context, command string, args []string) (*Client, error)
func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error)
func (c *Client) Notify(ctx context.Context, method string, params any) error
func (c *Client) OnNotify(method string, handler func(params json.RawMessage))
func (c *Client) Close() error
```

Key design points:
- **Header-based framing:** Read `Content-Length: N\r\n\r\n`, then read N bytes of JSON. This is the standard LSP framing.
- **Request correlation:** `id` field (int64, monotonically increasing). Response carries the same `id`. A `map[id]chan` routes responses to callers.
- **Concurrent-safe:** Mutex protects the pending map and nextID. A single reader goroutine dispatches incoming messages.
- **Graceful shutdown:** Send `shutdown` request → wait for response → send `exit` notification → close pipes → kill process.

### 5.4 Server Lifecycle Design

```go
// server.go

type State int
const (
    StateStopped  State = iota
    StateStarting
    StateRunning
    StateStopping
    StateError
)

type Server struct {
    Name        string
    Config      LspServerConfig
    Client      *Client
    State       State
    Capabilities ServerCapabilities

    restartCount   int
    maxRestarts    int           // default 3
    startupTimeout time.Duration // default 30s

    mu sync.RWMutex
}

func (s *Server) Start(ctx context.Context) error
func (s *Server) Stop(ctx context.Context) error
func (s *Server) Restart(ctx context.Context) error
func (s *Server) IsHealthy() bool
func (s *Server) Request(ctx context.Context, method string, params any) (json.RawMessage, error)
func (s *Server) Notify(ctx context.Context, method string, params any) error

// State transitions:
//   Stopped → (Start) → Starting → (initialize response) → Running
//   Running → (Stop) → Stopping → (process dead) → Stopped
//   Running → (crash) → Error → (restart) → Starting → Running
//   Error → (maxRestarts exceeded) → Stopped (permanent)
```

### 5.4.1 Concurrency Design for Lazy Server Start

`EnsureServerStarted(filePath)` is the critical concurrency point. Multiple goroutines (parallel tool dispatches) may request the same server simultaneously. The design must prevent:

1. **Duplicate starts** — two callers both see `Stopped` and both call `Start()`.
2. **Start failure propagation** — if the first start fails, waiting callers must receive the same error rather than retrying independently.
3. **Crash recovery races** — if a server is in `Error` state with remaining restart budget, only one goroutine should trigger the restart.

**Solution: `golang.org/x/sync/singleflight` per server name.**

```go
// manager.go — EnsureServerStarted

type Manager struct {
    servers      map[string]*Server
    extMap       map[string]string
    startGroup   singleflight.Group    // keyed by server name
    mu           sync.RWMutex
}

func (m *Manager) EnsureServerStarted(ctx context.Context, filePath string) (*Server, error) {
    serverName, ok := m.extMap[filepath.Ext(filePath)]
    if !ok {
        return nil, fmt.Errorf("no LSP server for extension %q", filepath.Ext(filePath))
    }

    m.mu.RLock()
    srv, exists := m.servers[serverName]
    m.mu.RUnlock()

    if exists && srv.IsHealthy() {
        return srv, nil
    }

    // singleflight: only one goroutine per server name enters the start function.
    // All concurrent callers block on the same call and receive the same result.
    result, err, _ := m.startGroup.Do(serverName, func() (any, error) {
        m.mu.Lock()
        srv, exists := m.servers[serverName]
        if !exists {
            return nil, fmt.Errorf("server %q not configured", serverName)
        }
        m.mu.Unlock()

        // Double-check state under singleflight — another caller may have
        // already completed the start before this flight was entered.
        if srv.IsHealthy() {
            return srv, nil
        }
        if srv.State == StateError && srv.restartCount >= srv.maxRestarts {
            return nil, fmt.Errorf("server %q exceeded max restarts", serverName)
        }

        if err := srv.Start(ctx); err != nil {
            return nil, err
        }
        return srv, nil
    })

    if err != nil {
        return nil, err
    }
    return result.(*Server), nil
}
```

**Sequence diagram for concurrent callers:**

```
Goroutine A                    Manager.startGroup              Server
     │                               │                           │
     │──EnsureServerStarted("x.go")──►│                           │
     │                               │──Do("gopls", fn)─────────►│
     │                               │                           │──Start()
     │                               │                           │  ...initializing...
     │                               │                           │
     │                               │                           │
Goroutine B                          │                           │
     │──EnsureServerStarted("y.go")──►│                           │
     │                               │──Do("gopls", fn)          │
     │                               │  (blocks — same key)      │
     │                               │                           │──initialize response
     │                               │◄───────return srv─────────│  State → Running
     │                               │                           │
     │◄──────return srv──────────────│                           │
     │                               │                           │
     │◄──────return srv──────────────│  (B gets same result)     │
```

**Why singleflight over double-check locking:**

| Approach | Pros | Cons |
|---|---|---|
| `sync.Mutex` + double-check | Simple; no extra dependency | Callers block on mutex even when server is healthy; second caller may start a duplicate if first fails between check and lock |
| Channel-based serialization (actor) | No locks; clear ownership | More code; harder to integrate with context cancellation |
| `singleflight` | Built for exactly this pattern; deduplicates in-flight work; callers share result (success or failure) | Adds `golang.org/x/sync` dependency |

`singleflight` is the standard Go solution for "deduplicate concurrent calls to the same expensive operation." It is used in production by the Go project itself (e.g., `net/lookup.go`).

### 5.5 Manager Design

```go
// manager.go

type Manager struct {
    servers map[string]*Server          // server name → server
    extMap  map[string]string            // file extension → server name
    openFiles map[string]string          // file URI → language ID

    daemonState *daemon.DaemonState      // for registering LSP daemons

    mu sync.RWMutex
}

func NewManager(configs []LspServerConfig, ds *daemon.DaemonState) *Manager

// Core operations
func (m *Manager) ServerForFile(filePath string) (*Server, bool)
func (m *Manager) EnsureServerStarted(ctx context.Context, filePath string) (*Server, error)
func (m *Manager) Request(ctx context.Context, filePath, method string, params any) (json.RawMessage, error)

// File synchronization (textDocument/didOpen, didChange, didSave, didClose)
func (m *Manager) OpenFile(ctx context.Context, filePath, content string) error
func (m *Manager) ChangeFile(ctx context.Context, filePath, content string) error
func (m *Manager) CloseFile(ctx context.Context, filePath string) error

// Lifecycle
func (m *Manager) Initialize(ctx context.Context) error
func (m *Manager) Shutdown(ctx context.Context) error
```

### 5.6 Tool Design

```go
// tool.go

// Single tool with an "operation" discriminator
type lspTool struct {
    manager *Manager
    workDir string
}

func (t *lspTool) Name() string        { return "lsp_request" }
func (t *lspTool) Description() string { /* detailed prompt for the LLM */ }
func (t *lspTool) Schema() json.RawMessage { /* JSON Schema with oneOf on operation */ }

func (t *lspTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
    // 1. Parse operation from input
    // 2. Validate filePath exists (if applicable)
    // 3. res := t.manager.EnsureServerStarted(ctx, filePath)
    // 4. Open file via didOpen (if using textDocument/* methods)
    // 5. Dispatch to correct LSP method based on operation
    // 6. Format result via formatters.go
    // 7. Return tools.Result{Content, Metadata}
}
```

Tool schema (discriminated union, 4 operations for Phase 1):

```json
{
  "type": "object",
  "required": ["operation"],
  "oneOf": [
    {
      "properties": {
        "operation": {"const": "go_to_definition"},
        "filePath": {"type": "string"},
        "line": {"type": "integer"},
        "character": {"type": "integer"}
      }
    },
    {
      "properties": {
        "operation": {"const": "find_references"},
        "filePath": {"type": "string"},
        "line": {"type": "integer"},
        "character": {"type": "integer"}
      }
    },
    {
      "properties": {
        "operation": {"const": "hover"},
        "filePath": {"type": "string"},
        "line": {"type": "integer"},
        "character": {"type": "integer"}
      }
    },
    {
      "properties": {
        "operation": {"const": "document_symbols"},
        "filePath": {"type": "string"}
      }
    }
  ]
}
```

### 5.7 Diagnostics Design

Diagnostics arrive as `textDocument/publishDiagnostics` notifications from the LSP server. They follow a different path from request/response operations — they are passive, not solicited by the agent.

```
LSP Server
  └─ publishDiagnostics notification
     └─ Client.OnNotify("textDocument/publishDiagnostics", handler)
        └─ diagnostics.go: handlePublishDiagnostics(params)
           └─ registry.Register(serverName, fileURI, diagnostics)
              └─ daemon signal emitted → agent loop drains it
                 └─ next turn: diagnostics injected as system reminder
```

The `DiagnosticRegistry`:

```go
// diagnostics.go

type DiagnosticRegistry struct {
    pending       []PendingDiagnostic
    delivered     *diagnosticKeySet              // bounded LRU set of seen keys
    maxPerFile    int                            // 10
    maxTotal      int                            // 30
    mu            sync.Mutex
}

// diagnosticKeySet is a bounded, thread-safe set with LRU eviction.
// Built on container/list + map to avoid external dependencies — the
// same pattern used by Go's own groupcache/lru.
//
// Alternative considered: github.com/hashicorp/golang-lru/v2 (5.1k stars,
// 1,261 importers, generics-based, last release v2.0.7 Sep 2023). Rejected
// for Phase 1 because the feature set we need (Contains + Add with eviction)
// is ~30 lines with container/list, and evva's policy is to minimize
// dependencies in the critical path. If the codebase later needs LRU in
// multiple places, switching to hashicorp/golang-lru/v2 is a drop-in
// replacement behind the same interface.
type diagnosticKeySet struct {
    capacity int
    items    map[string]*list.Element // key → list node
    order    *list.List               // front = oldest, back = newest
}

func newDiagnosticKeySet(capacity int) *diagnosticKeySet {
    return &diagnosticKeySet{
        capacity: capacity,
        items:    make(map[string]*list.Element, capacity),
        order:    list.New(),
    }
}

// Contains checks if key is in the set without affecting recency.
func (s *diagnosticKeySet) Contains(key string) bool {
    _, ok := s.items[key]
    return ok
}

// Add inserts key. If at capacity, evicts the least recently used key.
// If key already exists, it is moved to the back (most recent).
func (s *diagnosticKeySet) Add(key string) {
    if elem, ok := s.items[key]; ok {
        s.order.MoveToBack(elem)
        return
    }
    if s.order.Len() >= s.capacity {
        oldest := s.order.Front()
        s.order.Remove(oldest)
        delete(s.items, oldest.Value.(string))
    }
    elem := s.order.PushBack(key)
    s.items[key] = elem
}

func (r *DiagnosticRegistry) Register(serverName, fileURI string, diags []Diagnostic)
func (r *DiagnosticRegistry) Drain() []PendingDiagnostic     // called by agent loop
func (r *DiagnosticRegistry) ClearFile(fileURI string)       // when file edited
```

The `delivered` set stores diagnostic identity keys of the form `"{fileURI}|{hash(message,severity,range,source,code)}"`. Size: 500 entries (matching the reference). At ~80 bytes per entry (key + list.Element overhead), total memory is ~40 KB — negligible.

**Delivery format** (injected as `<system-reminder>`):

```
LSP diagnostics from gopls for internal/agent/loop.go:
  [Error] Line 89: undefined: drainDaemonSignals
  [Warning] Line 142: unused variable 'result'
```

### 5.8 Document Synchronization Model

The hardest part of LSP integration is not the request/response protocol — it is keeping the LSP server's in-memory document state consistent with the filesystem, especially when evva can modify files through multiple channels.

**Mutation channels that cause state drift:**

| Channel | Example | LSP-aware? |
|---|---|---|
| `lsp_request` tool | Agent opens a file via didOpen before querying | Yes |
| `write` / `edit` tools | Agent modifies file content | No |
| `bash` tool | `sed -i`, `go fmt`, git operations | No |
| External edits | User edits file in another editor | No |
| `git checkout` / `git reset` | File content changes without tool involvement | No |

**Phase 1: Minimal model (caller-responsible sync).**

- `lsp_request` sends `textDocument/didOpen` with current file content before the first LSP request on that file in a given turn.
- `lsp_request` sends `textDocument/didClose` after the last request in that turn.
- Between turns, the LSP server's document state is considered **stale** — the next `lsp_request` re-syncs via a fresh didOpen.
- No didChange notifications are sent from write/edit/bash tools. This means diagnostics produced between turns may reflect stale content.
- Trade-off: simple implementation, no cross-tool coupling. Cost: possible ghost diagnostics until the next lsp_request re-opens the file.

**Phase 2: Full incremental sync.**

- Register a `PostToolUse` hook (evva's existing hook system) for `write`, `edit`, and `bash` tools.
- On file mutation, send `textDocument/didChange` with the full new content to any LSP server that has the file open.
- `DiagnosticRegistry.ClearFile(fileURI)` is called on mutation to invalidate diagnostics produced from stale content.
- On `git checkout` / `git reset` (detected via file mtime monitoring or explicit tool result parsing), send didClose + didOpen to force full re-sync.
- Server maintains `openFiles map[uri]version` for version tracking (LSP's `TextDocumentIdentifier.version`).

**Source-of-truth policy:**

The filesystem is always the source of truth. The LSP server's document state is a cache. When in doubt, re-sync from disk. The tool reads file content via `read` tool (or direct `os.ReadFile`) at the moment of `lsp_request`, not from an in-memory cache.

### 5.9 Cancellation Model

Long-running LSP requests (e.g., `workspace/symbol` on a large monorepo, or `references` during indexing) can block the agent. The cancellation model ensures that:

1. The agent can interrupt an in-flight LSP request.
2. The LSP server is notified so it can free resources.
3. Superseded requests don't accumulate.

**Design:**

```go
// client.go

func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
    id := atomic.AddInt64(&c.nextID, 1)
    respCh := make(chan *response, 1)

    c.mu.Lock()
    c.pending[id] = respCh
    c.mu.Unlock()

    // Send the request
    if err := c.send(ctx, id, method, params); err != nil {
        c.mu.Lock()
        delete(c.pending, id)
        c.mu.Unlock()
        return nil, err
    }

    // Wait for response or cancellation
    select {
    case <-ctx.Done():
        // Send $/cancelRequest to the server
        c.Notify(context.Background(), "$/cancelRequest", CancelParams{ID: id})
        // Still drain the response channel briefly in case it arrives
        select {
        case resp := <-respCh:
            return resp.result, resp.err
        case <-time.After(500 * time.Millisecond):
            c.mu.Lock()
            delete(c.pending, id)
            c.mu.Unlock()
            return nil, ctx.Err()
        }
    case resp := <-respCh:
        return resp.result, resp.err
    }
}
```

**Timeout policy:**

| Operation | Default Timeout | Rationale |
|---|---|---|
| `initialize` | 30s | Server startup — indexing may take time |
| `textDocument/definition` | 10s | Should be fast on indexed projects |
| `textDocument/references` | 30s | Large projects may have many references |
| `workspace/symbol` | 30s | Full workspace scan |
| `callHierarchy/*` | 15s | Moderate complexity |
| All others | 10s | Default |

Timeouts are enforced via `context.WithTimeout` in the tool's `Execute` method, which propagates naturally through `Client.Request`.

**Shutdown cancellation:**

When `Manager.Shutdown(ctx)` is called, all in-flight requests are cancelled via their contexts, `$/cancelRequest` is sent for each, and then the standard `shutdown` → `exit` sequence follows.

### 5.10 Response Truncation & Context Budget

Large LSP responses can flood the LLM's context window. For example, `workspace/symbol` on a Go monorepo might return 5,000+ symbols; `references` on a widely-used function could return 200+ locations.

**Truncation policy (enforced in formatters.go):**

| Operation | Max Results | Max Total Bytes | Strategy |
|---|---|---|---|
| `go_to_definition` | 1 (best) | 2 KB | Return top result only (LSP `LocationLink` target) |
| `find_references` | 50 | 20 KB | Truncate with count: "…and 147 more locations" |
| `hover` | 1 | 5 KB | Full hover content (usually small) |
| `document_symbols` | 100 | 30 KB | Top-level symbols first; nested symbols counted |
| `workspace_symbols` | 50 | 20 KB | Sort by kind priority (function > class > variable); truncate |
| `go_to_implementation` | 20 | 10 KB | Same as references |
| `call_hierarchy` | 30 | 15 KB | Depth-first, max 3 levels |

**Formatting on truncation:**

```
textDocument/references for UserService.Authenticate (showing 50 of 247 results):
  internal/auth/handler.go:45    service.Authenticate(ctx, token)
  internal/auth/handler.go:89    if err := service.Authenticate(ctx, token); err != nil {
  internal/middleware/auth.go:23  svc.Authenticate(ctx, token)
  ...and 197 more locations across 42 files
```

**Global budget:** No single `lsp_request` result may exceed 40 KB of formatted text. If truncation at per-operation caps still exceeds this, the result is cut at the 40 KB boundary with a clear marker. This prevents a single tool call from consuming the entire context window.

### 5.11 Observability & Structured Logging

LSP server issues are notoriously hard to debug without structured telemetry. evva's existing `slog` infrastructure provides the foundation.

**Log points (all at `slog.LevelDebug` by default, upgradeable to `Info` via config):**

| Event | Level | Key Fields |
|---|---|---|
| `lsp.server.start` | Info | `server`, `command`, `args` |
| `lsp.server.started` | Info | `server`, `duration_ms`, `capabilities_summary` |
| `lsp.server.start_failed` | Error | `server`, `error`, `duration_ms`, `restart_count` |
| `lsp.server.crash` | Error | `server`, `exit_code`, `stderr_tail` |
| `lsp.server.restart` | Warn | `server`, `restart_count`, `max_restarts` |
| `lsp.server.stop` | Info | `server`, `duration_ms` |
| `lsp.request.start` | Debug | `server`, `method`, `file` |
| `lsp.request.end` | Debug | `server`, `method`, `duration_ms`, `result_bytes`, `error` |
| `lsp.request.slow` | Warn | `server`, `method`, `duration_ms` (> 5s threshold) |
| `lsp.diagnostic.received` | Debug | `server`, `file_count`, `total_diag_count`, `dedup_dropped` |
| `lsp.sync.didOpen` | Debug | `server`, `uri`, `bytes` |
| `lsp.sync.didChange` | Debug | `server`, `uri`, `bytes`, `version` |
| `lsp.sync.didClose` | Debug | `server`, `uri` |

**Observability hooks (for future metrics pipeline):**

```go
// Server struct has an optional observer
type ServerObserver interface {
    OnStart(serverName string, duration time.Duration, err error)
    OnCrash(serverName string, exitCode int, stderr string)
    OnRequest(serverName, method string, duration time.Duration, resultBytes int, err error)
    OnRestart(serverName string, count, max int)
}

// Set via Server.SetObserver(o ServerObserver)
// Default: no-op. Tests inject a recording observer.
```

This is a passive hook — no metrics pipeline dependency. Future integration with Prometheus/OpenTelemetry can implement the same interface.

### 5.12 Configuration Format

```yaml
# ~/.evva/lsp_servers.yml  (or <project>/.evva/lsp_servers.yml)

servers:
  gopls:
    command: gopls
    args: []
    extensions:
      ".go": "go"
    env:
      GOPATH: "${HOME}/go"
    startupTimeout: "30s"
    maxRestarts: 3

  typescript:
    command: typescript-language-server
    args: ["--stdio"]
    extensions:
      ".ts": "typescript"
      ".tsx": "typescriptreact"
      ".js": "javascript"
      ".jsx": "javascriptreact"
    startupTimeout: "60s"

  rust-analyzer:
    command: rust-analyzer
    args: []
    extensions:
      ".rs": "rust"
    env:
      RUST_SRC_PATH: "/path/to/rust/src"
```

- **Project-level** config (`.evva/lsp_servers.yml`) overrides **user-level** config (`~/.evva/lsp_servers.yml`).
- Server names with same key at project level replace the user-level definition.
- Environment variable expansion (`${VAR}` and `${HOME}`) in `command`, `args`, and `env` values.

### 5.13 Integration with Agent Profiles

`lsp_request` is a **deferred** tool in the Main profile:

```go
// internal/agent/profiles.go — Main()
DeferredTools: append(existing,
    tools.ToolName("lsp_request"),
)
```

This means:
- The tool name appears in the LLM's tool list but without its full schema.
- The LLM uses `tool_search` to discover the schema when it needs LSP capabilities.
- This saves context window space — LSP is powerful but not needed every turn.

### 5.14 Initialization Handshake

When a server starts, the `initialize` request declares evva's capabilities:

```go
params := InitializeParams{
    ProcessID: os.Getpid(),
    RootURI:   workDirURI,
    Capabilities: ClientCapabilities{
        Workspace: nil,       // Don't claim workspace support
        TextDocument: &TextDocumentClientCapabilities{
            Synchronization: &TextDocumentSyncClientCapabilities{
                DidSave: true,
            },
            PublishDiagnostics: &PublishDiagnosticsClientCapabilities{
                RelatedInformation: true,
            },
            Hover: &HoverClientCapabilities{
                ContentFormat: []MarkupKind{"markdown", "plaintext"},
            },
            Definition: &DefinitionClientCapabilities{
                LinkSupport: true,
            },
            References: &ReferencesClientCapabilities{},
            DocumentSymbol: &DocumentSymbolClientCapabilities{
                HierarchicalDocumentSymbolSupport: true,
            },
            CallHierarchy: &CallHierarchyClientCapabilities{},   // Phase 3
        },
        General: &GeneralClientCapabilities{
            PositionEncodings: []PositionEncodingKind{"utf-16"},
        },
    },
}
```

Position encoding is `utf-16` because that's what most LSP servers expect (matching JavaScript/TypeScript string indexing). The tool converts Go source positions (byte offsets or rune counts) to UTF-16 code units before sending.

---

## 6. Implementation Plan

### Phase 1 — Core LSP Client (MVP)

**Goal:** Agent can query an LSP server for definition, references, hover, and document symbols.

**Tasks:**

| # | Task | Files | Dependencies |
|---|---|---|---|
| 1.1 | Add `KindLSP` to daemon kind constants | `pkg/tools/daemon/kind.go` | None |
| 1.2 | Add `lsp_request` ToolName constant + deferred tags | `pkg/tools/name.go`, `pkg/toolset/tags.go` | 1.1 |
| 1.3 | Implement LSP protocol types | `pkg/tools/lsp/protocol/*.go` | None |
| 1.4 | Implement JSON-RPC 2.0 over stdio transport (in-house, header-based framing) | `pkg/tools/lsp/client.go` | 1.3 |
| 1.5 | Implement LSP server lifecycle (state machine, start/stop/restart, singleflight lazy start) | `pkg/tools/lsp/server.go` | 1.4 |
| 1.6 | Implement extension-to-server manager (routing, file sync) | `pkg/tools/lsp/manager.go` | 1.5 |
| 1.7 | Implement LSP config loader (YAML) | `pkg/tools/lsp/config.go` | None |
| 1.8 | Implement lspDaemon (daemon.Daemon adapter) | `pkg/tools/lsp/daemon.go` | 1.1, 1.5 |
| 1.9 | Implement lspTool (tools.Tool: definition, references, hover, documentSymbols) | `pkg/tools/lsp/tool.go` | 1.6 |
| 1.10 | Implement result formatters with truncation | `pkg/tools/lsp/formatters.go` | 1.9 |
| 1.11 | Implement mock LSP server for offline unit testing | `pkg/tools/lsp/mock_server_test.go` | 1.4 |
| 1.12 | Register tool factory in builtins | `internal/toolset/builtins.go` | 1.9 |
| 1.13 | Add to Main profile deferred tools | `internal/agent/profiles.go` | 1.2 |
| 1.14 | Add daemon signal handling for LSP lifecycle | `internal/agent/drain_daemons.go` | 1.8 |
| 1.15 | Write unit tests (client, server, manager, tool) using mock server | `pkg/tools/lsp/*_test.go` | 1.11 |
| 1.16 | Integration test with gopls on evva's own codebase | Manual | All above |

**Verification criteria:**
- `evva` can call `lsp_request` with `operation: "go_to_definition"` on a Go file and receive correct location data from gopls.
- Server starts lazily on first request; concurrent callers share a single start via singleflight (no thundering herd).
- Server stops cleanly on agent shutdown (no zombie processes — SIGKILL fallback after 5s timeout).
- Server crash is handled gracefully (logged, state set to error, restart on next request up to maxRestarts).
- All unit tests pass offline using the mock LSP server (no real server binary required).

### Phase 2 — Diagnostics

**Goal:** LSP diagnostics (errors, warnings) appear automatically in the conversation context.

| # | Task | Dependencies |
|---|---|---|
| 2.1 | Implement `textDocument/publishDiagnostics` notification handler | 1.5 |
| 2.2 | Implement DiagnosticRegistry (dedup, volume limiting) | 2.1 |
| 2.3 | Emit diagnostics as daemon signals | 1.8, 2.2 |
| 2.4 | Drain diagnostics in agent loop (inject as system reminder) | 2.3 |
| 2.5 | Clear delivered diagnostics when file is edited | 2.2 |
| 2.6 | Write tests | All above |

**Verification criteria:**
- After agent reads a file, diagnostics for that file appear in the next context window.
- Same diagnostic is not repeated across turns.
- Per-file cap (10) and total cap (30) are enforced.
- Editing a file resets delivered diagnostics for that file.

### Phase 3 — Advanced Operations

**Goal:** Full feature parity with Claude Code's LSP tool.

| # | Task | Dependencies |
|---|---|---|
| 3.1 | Add `workspaceSymbol` operation | 1.9 |
| 3.2 | Add `goToImplementation` operation | 1.9 |
| 3.3 | Add `prepareCallHierarchy` + `incomingCalls` + `outgoingCalls` (two-step) | 1.9 |
| 3.4 | Update tool schema with new operations | 1.9 |
| 3.5 | Update formatters | 3.1–3.3 |
| 3.6 | Write tests | All above |

**Verification criteria:**
- All 9 operations work against gopls, typescript-language-server, and rust-analyzer (if available).

### Phase 4 — Server Discovery & UX

**Goal:** Zero-config LSP for common languages; polished error messages.

| # | Task | Dependencies |
|---|---|---|
| 4.1 | Auto-detect common LSP servers from PATH + project files (`go.mod`, `package.json`, `Cargo.toml`) | 1.7 |
| 4.2 | Graceful "server not found" messages with install instructions | 1.6 |
| 4.3 | LSP server startup status in UI (using existing daemon monitor strip) | 1.8 |
| 4.4 | Documentation + user guide | All above |

### Phase 1 Detailed Timeline

```
Week 1:
  Day 1–2: Protocol types (1.3) + JSON-RPC transport (1.4)
  Day 3–4: Server lifecycle with singleflight lazy start (1.5) + config loader (1.7)
  Day 5: Manager with file routing (1.6)

Week 2:
  Day 1–2: lspDaemon (1.8) + lspTool (1.9) + formatters with truncation (1.10)
  Day 3–4: Mock LSP server (1.11) + unit tests using mock (1.15)
  Day 5: Registration & wiring into agent (1.12, 1.13, 1.14)

Week 3:
  Day 1–3: Integration testing with gopls on evva's own codebase (1.16)
            — cold-start behavior, indexing time, first-request latency
            — concurrent request stress testing (parallel goroutine dispatch)
            — crash recovery verification (kill -9 the server mid-request)
  Day 4–5: Bug fixes from integration findings; edge case hardening
```

### Phase 2 Timeline (estimated)

```
Week 4:
  Day 1–2: publishDiagnostics handler (2.1) + DiagnosticRegistry (2.2)
  Day 3–4: Daemon signal integration (2.3, 2.4) + stale diagnostic invalidation (2.5)
  Day 5: Tests (2.6)
```

### Phase 3 Timeline (estimated)

```
Week 5:
  Day 1–2: workspaceSymbol + goToImplementation (3.1, 3.2)
  Day 3: Call hierarchy three-operation chain (3.3)
  Day 4: Schema + formatter updates (3.4, 3.5)
  Day 5: Tests (3.6)
```

### Phase 4 Timeline (estimated)

```
Week 6–7:
  Day 1–2: Auto-detection from project files (4.1)
  Day 3: Graceful error messages (4.2)
  Day 4: UI status integration (4.3)
  Day 5: Documentation (4.4)
```

---

## 7. Risk Analysis & Mitigation

### Risk 1: JSON-RPC framing edge cases

**Severity:** Medium  
**Likelihood:** Medium  

Some LSP servers send messages in chunks (the `Content-Length` header arrives, then the body arrives in a subsequent `Read()`). The reader goroutine must handle partial reads correctly.

**Mitigation:** Use `io.ReadFull` for the body after reading the header. Write a fuzz test that sends random chunk boundaries.

### Risk 2: UTF-16 position encoding

**Severity:** Medium  
**Likelihood:** High  

LSP uses UTF-16 code units for positions. Go source files are UTF-8. Characters outside the BMP (e.g., emoji in string literals) have different lengths in UTF-8 vs UTF-16.

**Mitigation:** Implement a UTF-8 → UTF-16 offset converter. Most files contain only BMP characters, so this is a correctness edge case rather than a daily problem. The converter can be ~30 lines.

### Risk 3: Server hangs during shutdown (zombie processes)

**Severity:** Medium  
**Likelihood:** Medium  

Some LSP servers don't respond to the `shutdown` request promptly, or exit without cleaning up child processes. This can leave zombie processes consuming resources.

**Mitigation:** Shutdown is time-boxed (5s default). If the server doesn't respond to `shutdown`, the client sends `SIGKILL` to the process group. evva's existing `exec.Cmd` pattern (from the bash tool) is reused: `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` for process-group isolation, `cmd.WaitDelay = 2s` to drain pipes after kill, and `syscall.Kill(-pid, SIGKILL)` to kill the entire group. The daemon system's `Kill(ctx)` method already implements this for other daemon kinds.

### Risk 4: Document state drift

**Severity:** Medium  
**Likelihood:** High  

Agent edits, bash tool writes, git operations, and external edits can desynchronize the LSP server's in-memory document state from the filesystem. This produces ghost diagnostics (errors about code that no longer exists) and incorrect query results.

**Mitigation:** Phase 1 uses a minimal model — explicit didOpen/didClose per `lsp_request` call, with the understanding that diagnostics between turns may be stale. Phase 2 implements full incremental sync via `PostToolUse` hooks on write/edit/bash tools (see §5.8).

### Risk 5: Large response payloads flooding LLM context

**Severity:** High  
**Likelihood:** Medium  

Operations like `workspace/symbol` and `references` on large monorepos can return thousands of results, consuming the entire LLM context window in a single tool call.

**Mitigation:** Per-operation truncation policy with max result counts and byte limits, plus a global 40 KB cap per `lsp_request` result (see §5.10).

### Risk 6: Thundering herd on lazy server start

**Severity:** Medium  
**Likelihood:** Medium  

Multiple concurrent `lsp_request` calls for files of the same extension could each try to start the same LSP server simultaneously.

**Mitigation:** `singleflight` pattern — exactly one in-flight `Start()` per server name, all concurrent callers share the result (success or failure). See §5.4.1 for the detailed design.

### Risk 7: LSP server binary not installed

**Severity:** Low  
**Likelihood:** High (for new users)

**Mitigation:** Clear error messages with install commands: `gopls not found in PATH. Install with: go install golang.org/x/tools/gopls@latest`. Phase 4 adds auto-detection to make this less likely.

---

## 8. Open Questions

1. **Should `lsp_request` be active or deferred?**  
   Recommendation: deferred. LSP is powerful but not needed every turn. Deferred loading saves LLM context. The tool can be brought active via `tool_search` when the agent needs it.

2. **One tool vs. multiple tools?**  
   The reference uses a single `LSP` tool with an `operation` discriminator. This is better than separate tools (`lsp_definition`, `lsp_references`, etc.) because it keeps the tool list manageable and groups related functionality.

3. **Should diagnostics go through the daemon system or a separate channel?**  
   Recommendation: daemon system. It already handles async subprocess output, lifecycle tracking, and UI updates. Diagnostics are a natural fit — they're "output" from a daemon.

4. **Project-level vs. user-level LSP config?**  
   Both. Project-level `.evva/lsp_servers.yml` takes precedence. User-level `~/.evva/lsp_servers.yml` is the fallback. This lets projects pin specific server versions while users set defaults.

5. **Should we support socket transport?**  
   Not in Phase 1. The reference only implements stdio, and all major LSP servers support stdio. Socket transport adds complexity without clear benefit for a CLI agent.

6. **Should `lsp_request` count toward the tool-use limit?**  
   Yes. It's a standard tool call. Unlike the reference (which has special `isLsp` handling), evva can treat it uniformly.

7. **Multi-root workspace support?**  
   Not in Phase 1. The current design uses a single `RootURI` (the agent's `WorkDir`). Real-world projects — monorepos with nested `go.mod`, pnpm workspaces, Cargo workspaces — may need multiple workspace folders. The LSP protocol supports `workspaceFolders` in `InitializeParams`, and evva's design should use a `[]RootURI` slice rather than a scalar to avoid a breaking change later. This is a Phase 4 consideration; for now, the single-root model covers the common case (a Go project at the working directory root).

---

## 9. Appendix: Key Reference Files

For developers implementing this plan, these reference files are the most relevant:

| File | Lines | Relevance |
|---|---|---|
| `ref/src/services/lsp/LSPClient.ts` | 447 | JSON-RPC transport, spawn, shutdown |
| `ref/src/services/lsp/LSPServerInstance.ts` | 511 | State machine, crash recovery, retry |
| `ref/src/services/lsp/LSPServerManager.ts` | 420 | File-to-server routing, didOpen/Change/Close |
| `ref/src/services/lsp/manager.ts` | 289 | Global orchestrator singleton |
| `ref/src/services/lsp/LSPDiagnosticRegistry.ts` | 386 | Dedup, volume limiting, cross-turn tracking |
| `ref/src/services/lsp/passiveFeedback.ts` | 328 | publishDiagnostics → attachment conversion |
| `ref/src/tools/LSPTool/LSPTool.ts` | 860 | AI-facing tool, input validation, operation dispatch |
| `ref/src/tools/LSPTool/formatters.ts` | 592 | Human-readable output formatting |
| `ref/src/tools/LSPTool/schemas.ts` | 215 | Zod discriminated union schema |
| `ref/src/services/lsp/config.ts` | 79 | Config loading from plugins |
| `ref/src/utils/plugins/lspPluginIntegration.ts` | 387 | LSP server loading from plugin manifests |

Porting guidance: read these files from top to bottom in the order listed. The TypeScript patterns translate naturally to Go — classes become structs with methods, async/await becomes goroutines + channels, Zod schemas become `encoding/json` unmarshaling.

---

## Summary of Recommendations

1. **Start with Phase 1** — core LSP client with 4 operations. This is the minimum viable integration and proves the architecture.
2. **Use the daemon system** — don't build a separate lifecycle tracker. evva's daemon infrastructure is already designed for long-running subprocesses.
3. **Write our own JSON-RPC transport** — ~200 lines, no external dependency, full control over LSP header framing and error semantics.
4. **Hand-write protocol types** — the subset we need (~20 structs) is manageable and avoids a heavy dependency.
5. **Use `singleflight` for lazy server start** — prevents thundering herd; all concurrent callers share a single start result.
6. **Deferred tool** — `lsp_request` should be deferred to save context window space.
7. **Lazy server start** — servers launch only when a file of a matching extension is queried.
8. **Project + user config** — YAML-based, with project-level override.
9. **Mock LSP server for tests** — unit tests run offline; real-server integration tests are separate and manual.
10. **Response truncation from day one** — 40 KB global cap, per-operation limits to protect LLM context.
11. **Structured logging from day one** — `slog` log points for every lifecycle event, request, and diagnostic delivery.
12. **Test against gopls first** — it's the most relevant for a Go codebase, and it's the easiest to install (`go install golang.org/x/tools/gopls@latest`).
