# evva swarm — web SPA (FE v2)

Vue 3 + TypeScript + Pinia + vue-router SPA for the `evva service` swarm
workstation (Veronica). This is the **FE v2** app (PRDs:
[`docs/veronica/refine-plan/fe-v2/`](../docs/veronica/refine-plan/fe-v2/README.md)).
The built output (`dist/`) is embedded into the `evva` binary via
[`embed.go`](embed.go) (package `web2`) and served by `internal/swarm/service`
(`service.go` embeds `web2.Dist`).

> v1 lives in [`../web`](../web) and is no longer embedded. It is kept for
> reference until explicitly retired.

## Build / dev / test

```sh
npm ci
npm run build      # vue-tsc --noEmit + vite build → web2/dist (embedded by the Go build)
npm run dev        # vite dev server
npm test           # node --test (pure lib) + vitest run (stores)
```

## Notes

- **`dist/` is vendored (committed).** `go build` / `go install` run no node step,
  so the built SPA must live in the repo. **After any frontend change, run
  `npm run build` and commit `dist/`.** CI (the `web` job) rebuilds + runs tests +
  guards against a stale `dist/`.
- Asset names are **stable / un-hashed** (`assets/index.js`, `assets/index.css`,
  see `vite.config.ts`); `base: './'` keeps URLs relative for the embedded FS.
- **Theming**: three-layer CSS tokens (`src/styles/`) — `primitive` (per-theme
  raw palette: `neon-tokyo`, `midnight`) → `semantic` (the only layer components
  read) → `component`. A new theme = one `tokens.primitive.<name>.css` + a name in
  `THEMES` (`src/stores/ui.ts`). NEON TOKYO is ported 1:1 from the TUI
  (`pkg/ui/bubbletea/theme/palette.go`).
- **Tests**: pure, framework-free logic (`src/lib/*.test.ts`) runs under
  `node --test`; Pinia store glue (`src/**/*.spec.ts`) runs under Vitest.

## Layout

```
src/
  lib/        pure logic (events, colors, api, ws, cron, timeline, segments, tools) + tests
  types/      wire/event contracts mirroring the Go backend
  stores/     Pinia (connection, stream, space, ledger, mail, gate, spaces, session, ui)
  composables/ useSwarm (active-space IO lifecycle)
  router/     vue-router (hash history; URL = state)
  shell/      TopBar, AppLayout, Inspector, SpaceMenu, SpaceSwitcher, ThemeToggle
  views/      Landing, Workspace, Board, Timeline, Stream, Completed, ThemeProbe (/probe)
  components/ base/ (atoms) · stream/ · board/ · attention/ · gates/ · roster/ · compose/ · inspector/ · safety/
  styles/     token system + base scale
```
