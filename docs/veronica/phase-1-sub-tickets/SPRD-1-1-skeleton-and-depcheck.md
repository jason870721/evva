# SPRD-1-1 — Module skeleton, package layout, dep-check, CI

> Milestone: M0 ｜ Status: IN REVIEW ｜ Owner: (unassigned) ｜ Depends on: —
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 0) ｜ Index: [`README.md`](README.md)

## 1. Goal

Stand up the empty-but-compiling scaffolding for the whole swarm subsystem so
every later ticket has a home and the **pkg-only discipline is enforced from
commit #1**. No behavior beyond a health endpoint and CLI arg parsing.

## 2. Scope

**In:**
- `internal/swarm/` package tree (empty/stub packages): `service`, `supervisor`,
  `scheduler`, `roster`, `bus`, `store`, `agentdef`, `tools`, `webapi`.
- `cmd/evva` subcommand dispatch: `evva service start|stop|status`,
  `evva swarm . | ls | stop <name> | add <name>` (parse + route only; real impl
  in later tickets). Bare `evva` (TUI) path unchanged.
- `web/` vite + vue3 project scaffold (empty SPA, `npm run build` produces
  `web/dist`), wired for `embed.FS` (a placeholder embed that compiles).
- **dep-check** CI target enforcing global invariant #1.
- `evva service start` brings up a bare HTTP server on `127.0.0.1:8888` with
  `GET /healthz → 200`.

**Out:** any swarm logic (store/bus/agents/web UI) — those are later tickets.

## 3. Dependencies & what this unblocks

- Depends on: nothing.
- Unblocks: **all** other tickets (they fill in the stub packages).

## 4. Technical design

- Package layout per parent PRD §8. Each stub package has a doc comment stating
  its responsibility + a `// TODO(SPRD-1-N)` marker.
- Subcommand dispatch: extend `cmd/evva/main.go` to branch on `os.Args[1]`
  (`service`/`swarm`) → `cmd/evva/service.go` / `swarm.go`; default → existing TUI.
- HTTP bootstrap lives in `internal/swarm/service` with a `New(addr) *Service`
  + `Start/Stop`; M0 only registers `/healthz`.
- **dep-check** (`scripts/depcheck.sh` or a Makefile target + CI step):
  ```sh
  go list -deps ./internal/swarm/... | grep -E '/internal/agent($|/)' && {
    echo "FAIL: internal/swarm must not import internal/agent"; exit 1; }
  ```
  (Also assert no other `evva/internal/` besides `internal/swarm` itself.)

## 5. Acceptance criteria

1. `go build ./...` and `go vet ./...` pass with the new tree.
2. `evva service start` serves `127.0.0.1:8888/healthz` → `200`; `evva` (no
   subcommand) still launches the existing TUI unchanged.
3. `evva swarm ls` (and the other swarm/service subcommands) parse and route
   without panicking (may print "not implemented").
4. `web/` builds (`npm ci && npm run build` → `web/dist`); the Go embed of
   `web/dist` compiles.
5. **dep-check passes** and is wired into CI; deliberately adding an
   `internal/agent` import to a swarm package makes it FAIL (demonstrate once).

## 6. Verification

- Unit: a trivial `service` test that boots the server on an ephemeral port and
  asserts `/healthz` returns 200.
- CI: `go build ./...`, `go test ./...`, `npm run build`, and `depcheck` all run.
- Manual: run the 5 acceptance commands; capture output in the PR.

## 7. Definition of Done

- [x] Package tree + subcommand dispatch + web scaffold; `go build ./...` + `go vet ./...` green.
- [x] `/healthz` → 200 "ok" (unit test `service_test.go` + manual curl); TUI path untouched (`-version` + bare path unchanged; dispatch only fires on `service`/`swarm`).
- [x] dep-check (`scripts/depcheck.sh` + `make depcheck`), wired into CI (`.github/workflows/ci.yml`), and proven to FAIL on a deliberate `internal/agent` import (exit 1) and pass when reverted (exit 0).
- [x] `web` builds (`npm ci && npm run build` → `web/dist`) and embeds (`web/embed.go` `//go:embed all:dist`); `dist/` is vendored (stable asset names) so `go build`/`go install`/release embed a working UI without a node step.
- [x] Global invariant #1 holds: `go list -deps ./internal/swarm/...` contains no `internal/agent` (dep-check green).

### Implementation notes / decisions

- **Layout reconciliation.** §2 lists `service/supervisor/scheduler/roster` as stub *packages*, but SPRD-1-4 §4 and the parent PRD §8 put `SwarmSpace`/`Roster` (and supervisor/scheduler) as *files in `package swarm`*. They are a tightly-coupled per-space coordination core, so: **`package swarm` = {space, roster, supervisor, scheduler}** (files); **subpackages = {service, webapi, bus, store, agentdef, tools}**. `service` stays a subpackage per §4's explicit `New(addr) *Service`. Layering: `service` → `swarm` → {`bus`,`store`,`agentdef`}, no cycles.
- **CI is new.** The repo had only a tag-triggered `release.yml`; added `ci.yml` (push/PR) running build · vet · `go test ./internal/swarm/...` · dep-check, plus a `web` job that rebuilds the SPA and fails if the vendored `dist/` is stale.
- **`evva service start`** runs in the foreground (blocking) for M0; daemonization/pidfile/`stop`/`status` are SPRD-1-9 stubs that print "not implemented".
