// Package web embeds the built vue.js SPA (web/dist) so `evva service` can
// serve the swarm workstation UI from a single binary.
//
// The embed lives here, at the module root, because go:embed paths cannot
// climb out of the Go file's directory (no ".."); consumers such as
// internal/swarm/service and internal/swarm/webapi import this package to get
// the FS. It holds nothing but the embed — no agent concerns — so the
// multi-agent dep-check is unaffected.
package web

import "embed"

// Dist is the built SPA tree rooted at "dist". The built output is vendored
// into the repo (web/dist is committed, with stable un-hashed asset names) so
// `go build` / `go install` / release.yml embed a working UI without a node
// build step. Run `npm run build` (see README.md) and commit dist/ after
// changing the frontend. all: keeps any underscore/dot-prefixed files in the
// embed too.
//
//go:embed all:dist
var Dist embed.FS
