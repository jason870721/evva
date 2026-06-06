# evva swarm — web SPA

vue 3 + vite single-page app for the `evva service` swarm workstation
(Veronica). The built output (`dist/`) is embedded into the `evva` binary via
[`embed.go`](embed.go) and served by `internal/swarm/service`.

## Build

```sh
npm ci
npm run build      # → web/dist (embedded by the Go build)
```

## Develop

```sh
npm run dev        # vite dev server (API proxy to `evva service` lands in SPRD-1-10)
```

## Notes

- **`dist/` is vendored (committed), not git-ignored.** The single `evva`
  binary embeds the built SPA via [`embed.go`](embed.go), and `go install` /
  `release.yml` run `go build` *only* (no node step) — so the built output must
  live in the repo for the binary to ship a working UI. **After any frontend
  change, run `npm run build` and commit `dist/`.** The CI `web` job rebuilds
  the SPA on every push to guard against a stale `dist/`.
- Asset names are **stable / un-hashed** (`assets/index.js`, `assets/index.css`
  — see `vite.config.js`) so rebuilds are content-only diffs, not churn of
  hash-named files. Cache-busting is a non-issue for a localhost workstation.
- `base: './'` keeps asset URLs relative so the bundle works from the embedded
  FS at any mount path.
