# SPRD-1-1 — Module skeleton, package layout, dep-check, CI

> Milestone: M0 ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: —
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

- [ ] Package tree + subcommand dispatch + web scaffold committed; `go build ./...` green.
- [ ] `/healthz` works; TUI path untouched.
- [ ] dep-check committed, wired to CI, and proven to fail on a bad import.
- [ ] `web` builds and embeds.
- [ ] Global invariant #1 holds.
