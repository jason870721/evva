# SPRD-1-8 — Service (multi-space host) + webapi (HTTP/WS, REST, event fan-out)

> Milestone: M0–M3 ｜ Status: IN REVIEW ｜ Owner: veronica ｜ Depends on: 1-4, 1-6, 1-7
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 6) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §3.1, §8, §8.1, §8.3

## 1. Goal

The **process-singleton `:8888` host** and its HTTP/WS API. `Service` is the multi-space
container — a registry of isolated `SwarmSpace`s — that fans each space's tagged event
stream out to the right WebSocket and routes inbound Web commands to the right
`Controller`/`Supervisor`. **Multi-space is native from M0** (§3.1, invariant #2): no
single-space hardcode anywhere.

## 2. Scope

**In:**
- `Service`: `:8888` server + `map[spaceID]*SwarmSpace` registry + session token +
  lifecycle (start/stop). Binds **`127.0.0.1` by default** (§8.3, invariant #6).
- **Register / Unregister a space**: given a workdir, build agents (1-3/1-4), start its
  supervisor (1-6), add to the registry; `stop <id>` tears one down without touching others.
- **Event fan-out**: select across every space's `out` channel; route each event to WS
  subscribers keyed by `(spaceID, AgentID)`. Use `event.Multi` to also tee to log.
- **REST snapshots**: `GET /api/swarms`, `/api/swarm/:id` (roster), `/api/tasks`,
  `/api/agents/:name/transcript`, `/api/messages`.
- **Inbound commands** (WS/REST): Leader chat → `Controller.Run`; approvals →
  `RespondPermission`/`RespondQuestion`; suspend/add/freeze/halt → `Supervisor`.
- **Auth**: a session token (generated on first start, printed to the terminal) required
  on every WS/REST request.
- Serve the embedded SPA (`web/dist` via `embed.FS`) — the placeholder from 1-1 until
  1-10 fills it.

**Out:** the CLI that calls this (1-9); the SPA content (1-10); restart reload (1-11).

## 3. Dependencies & what this unblocks

- Depends on: 1-4 (`SwarmSpace`/`out`/`Roster`), 1-6 (`Supervisor` commands), 1-7 (the
  tools whose task/message output these endpoints expose).
- Unblocks: 1-9 (subcommands POST here), 1-10 (SPA consumes this API), 1-11 (boot reconcile).

## 4. Technical design

Package `internal/swarm` (`service.go`) + `internal/swarm/webapi`.

```go
type Service struct {
    addr, token string
    mu     sync.RWMutex
    spaces map[string]*SwarmSpace
    hub    *webapi.Hub
}

func NewService(addr string) *Service
func (s *Service) Start(ctx context.Context) error
func (s *Service) Stop(ctx context.Context) error
func (s *Service) Register(workdir string) (spaceID string, err error) // build space + supervisor.Start
func (s *Service) StopSpace(id string) error
func (s *Service) ListSpaces() []SpaceInfo
```

- `webapi.Hub`: WS upgrade + per-connection subscription filtered by `(spaceID,
  AgentID)`; a pump goroutine drains each space's `out` channel into matching sockets.
- Router: stdlib `net/http` + `http.ServeMux` (or a light router); a token-check
  middleware wraps all `/api` + `/ws`.
- Inbound handlers translate JSON → the narrow `Controller`/`Supervisor` calls and expose
  **nothing beyond those public seams** (invariant #1).

## 5. Acceptance criteria

1. `Start` serves `:8888` on `127.0.0.1`; a request without the token is `401`.
2. `Register(workdirA)` then `Register(workdirB)` yields two isolated spaces in
   `/api/swarms`; stopping A leaves B serving (isolation).
3. A `Controller.Run` on a space's leader streams `event.Event`s to a WS client
   subscribed to that `(spaceID, leaderAgentID)` — and **not** to a client subscribed to
   the other space.
4. `/api/swarm/:id` returns the roster snapshot; `/api/tasks` reflects the ledger.
5. An approval event raised by an agent is resolvable via the WS `RespondPermission` path.

## 6. Verification

- `httptest` integration: token gate, two-space registration + isolation, WS event
  routing by `(spaceID, AgentID)`, REST snapshots, a permission round-trip — all with a
  fake LLM provider so no real API calls.
- `go test -race ./internal/swarm/... ./internal/swarm/webapi/...` clean.

## 7. Definition of Done

- [x] `Service` multi-space registry + `:8888` (127.0.0.1) + token; start/stop/register/stop-space.
- [x] WS fan-out keyed by `(spaceID, AgentID)`; REST snapshots; inbound command routing.
- [x] Two-space isolation + token gate proven by test (invariants #2, #6).
- [x] Embeds `web/dist`; `-race` clean; no `internal/agent` import (invariant #1).

### Implementation notes

- `Service` (`internal/swarm/service/service.go`) holds the `map[id]*spaceEntry`
  registry, a `crypto/rand` session token, a root context whose children are
  each space's supervisor + event pump. `Register(workdir)` is the from-disk
  production path (manifest → `BuildAll` → `register`); the unexported
  `register(manifest, loaded, cfg)` core is shared so tests bring spaces up with
  a stub LLM and no disk/env. A `loadConfig` seam keeps `config.Load` overridable.
- `webapi` (`api.go` + `hub.go`) owns its own wire DTOs and talks to the host
  only through the narrow `Backend` interface — zero agent/store/llm imports.
  WebSocket transport is `golang.org/x/net/websocket` (already an x/net
  subpackage, so **no new module dependency** — keeps the pure-Go,
  dep-conscious posture). `Hub.Publish` fans out by `(spaceID, AgentID)`;
  a conn subscribes to one space (+ optional agent filter).
- Endpoints follow the PRD names with a `?space=<id>` selector for the flat
  ones: `GET /api/swarms`, `/api/swarm/{id}`, `/api/tasks`, `/api/messages`,
  `/api/agents/{name}/transcript`; command POSTs (`run`, `suspend`/`resume`/
  `freeze`/`unfreeze`, `members`, `halt`) mirror the WS inbound channel
  (`run`, `respond_permission`, `respond_question`). `/healthz` is the only
  unauthenticated route besides the SPA.
- Added `store.ListMessages` (read-only DAO) for `/api/messages`.
- Tests: `webapi/api_test.go` (token gate, REST, WS isolation, WS respond-
  permission routing — fake Backend) + `service/service_integration_test.go`
  (two real stub-LLM spaces: registration/isolation, REST reflects ledger, WS
  routing across spaces, RespondPermission routing). `-race` clean.
- **Out of scope (later tickets):** the `evva swarm .` CLI that POSTs a workdir
  here (1-9); the real SPA content (1-10); restart reload (1-11). AC#5's
  end-to-end "agent raises an approval" needs a permission-gated tool mid-run;
  here the **routing path** is proven (unknown-reqID surfaces the controller's
  own error, proving the call reached the right controller).
