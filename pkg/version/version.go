// Package version exposes the evva SDK's release identity. Downstream
// apps can log Version on startup or assert against it when their
// integration depends on a specific evva surface.
//
// The Version constant is the source of truth for the release.
// BuildStamp is empty by default; release builds can set it via
// `-ldflags "-X github.com/johnny1110/evva/pkg/version.BuildStamp=..."`
// to capture a commit hash, build timestamp, or CI run id without
// touching tracked source.
package version

import "fmt"

// Version is the SDK release identifier. Bumped on every tagged
// release. As of v1.0.0 the Stable-tier surface promise in
// docs/contributing/sdk-stability.md is in force: breaking changes to Stable
// packages require a major bump.
const Version = "v1.7.4"

// BuildStamp is an optional build-identifying string populated at link
// time via -ldflags. Empty for `go build` / `go run` invocations off
// the source tree; tagged release binaries carry the commit short hash
// + build date.
var BuildStamp = ""

// String returns "vX.Y.Z" or "vX.Y.Z+<stamp>" when a build stamp is
// present. The leading "v" matches git tag conventions; use Bare for
// the unprefixed semver.
func String() string {
	if BuildStamp == "" {
		return "v" + Version
	}
	return fmt.Sprintf("v%s+%s", Version, BuildStamp)
}

// Bare returns the bare semver without the leading "v" prefix
// ("0.3.0-alpha.1"). Useful for callers that compose their own tag
// format (e.g. "release/0.3.0-alpha.1") or fold the version into a
// JSON field where the `v` prefix would be non-idiomatic.
//
// The build stamp, if set, is appended with "+" — matching the
// SemVer 2.0 build-metadata syntax: `0.3.0-alpha.1+abc1234`.
func Bare() string {
	if BuildStamp == "" {
		return Version
	}
	return Version + "+" + BuildStamp
}
