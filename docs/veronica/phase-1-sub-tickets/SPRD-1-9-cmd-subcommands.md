# SPRD-1-9 — `cmd/evva` subcommands: `service` (daemon+pidfile), `swarm ./ls/stop/add`

> Milestone: M0 (service / swarm .) / M3 (add) ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-8
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 7) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §4.1, §4.2, §4.3

## 1. Goal

The **control-plane CLI**: turn the dispatch stubs from 1-1 into real commands.
`evva service start/stop/status` runs the `:8888` host as a background daemon with a
pidfile; `evva swarm .` POSTs the current workdir to the running service to register a
space (process model **A** — the service builds the agents, §4.2); `ls`/`stop`/`add`
manage spaces. Bare `evva` (TUI) is untouched.

## 2. Scope

**In:**
- `evva service start` — daemonize (detached process), write pidfile + logs under
  `~/.evva/service/` (§4.3); idempotent (refuse if already running). `stop` (signal +
  pidfile cleanup); `status` (running? pid? `:8888` reachable? token location).
- `evva swarm .` — read `./evva-swarm.yml` (validate via 1-3), POST `{workdir}` to the
  running service's register endpoint; print the returned space URL. Error clearly if the
  service isn't running.
- `evva swarm ls` — GET `/api/swarms`, print the space table.
- `evva swarm stop <id|name>` — POST stop-space.
- `evva swarm add <name>` (M3) — POST hot-load a member into the current space.
- Token handling: read the service token from `~/.evva/service/` for authenticated calls.

**Out:** the HTTP server itself (1-8); the SPA (1-10).

## 3. Dependencies & what this unblocks

- Depends on: 1-8 (the service endpoints these call).
- Unblocks: the end-user M0 gate (a human can start the service + register a space);
  1-13 (the e2e drives these commands).

## 4. Technical design

`cmd/evva/service.go` + `cmd/evva/swarm.go` (dispatch from 1-1's `main.go`).

- **Daemonize**: re-exec self with a sentinel flag/env in the background (no third-party
  daemon lib); the parent writes the pidfile and returns; the child runs `Service.Start`.
- **pidfile**: `~/.evva/service/evva-service.pid`; `status`/`stop` read it; stale-pid
  detection (pid not alive → treat as stopped).
- `swarm .` is a **thin HTTP client** — it does **not** build agents (model A: the service
  does). Resolve the absolute workdir before POST.
- Reuse the existing `config`/AppHome conventions for the `~/.evva/service/` paths.

## 5. Acceptance criteria

1. `evva service start` backgrounds, writes a pidfile, and `:8888/healthz` answers; a
   second `start` refuses ("already running").
2. `evva service status` reports running/stopped accurately (incl. the stale-pid case);
   `evva service stop` terminates the daemon and removes the pidfile.
3. `evva swarm .` in a dir with a valid `evva-swarm.yml` registers a space and prints its
   URL; in a dir without one (or with the service down) it errors clearly.
4. `evva swarm ls` lists registered spaces; `evva swarm stop <id>` stops one.
5. Bare `evva` still launches the TUI unchanged.

## 6. Verification

- Integration: start the daemon on an ephemeral port (override `:8888`), exercise
  start/status/stop + swarm ./ls/stop against it; assert the pidfile lifecycle.
- A stale-pidfile unit test (write a dead pid → `status` says stopped).
- Manual: capture the 5 acceptance commands' output in the PR.

## 7. Definition of Done

- [ ] `service start/stop/status` with daemonize + pidfile + `~/.evva/service/` logs.
- [ ] `swarm . / ls / stop` (+ `add` at M3) as thin authenticated HTTP clients.
- [ ] TUI path untouched; stale-pid handled.
- [ ] Integration test green; no `internal/agent` import (invariant #1).
