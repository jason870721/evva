// Package web2 embeds the built FE v2 SPA (web2/dist) so `evva service` can
// serve the swarm workstation UI from a single binary.
//
// Like web/embed.go, the embed lives at this module subdir root because
// go:embed paths cannot climb out of the Go file's directory (no ".."). The
// service switches its import from web to web2 at the FE-8 cutover; until then
// this package builds and embeds dist/ but is not yet wired into the service.
//
// dist/ is vendored (committed) with stable, un-hashed asset names so
// `go build` / `go install` embed a working UI without a node build step. After
// any frontend change, run `npm run build` in web2/ and commit dist/.
package web2

import "embed"

//go:embed all:dist
var Dist embed.FS
