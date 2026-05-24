// Package update implements self-update for evva.
//
// It fetches the latest GitHub Release, compares versions, downloads the
// correct binary asset for the current OS/arch, and replaces the running
// binary atomically — no Go toolchain required.
package update

import (
	"context"
	"fmt"
	"os"
	"runtime"

	config "github.com/johnny1110/evva/pkg/config"
)

// defaults for the bundled evva binary.
const (
	DefaultOwner = "johnny1110"
	DefaultRepo  = "evva"
)

// CurrentVersion returns the ldflags-injected version string. Falls back to
// the compile-time default when ldflags weren't set (dev builds, go run).
func CurrentVersion() string {
	v := config.Version
	if v == "" {
		v = config.DefaultAppVersion
	}
	return v
}

// Check fetches the latest release from GitHub for owner/repo and returns it.
// If the latest tag matches currentVersion, the release is still returned so
// the caller can decide what "up-to-date" means (exact match vs newer).
func Check(ctx context.Context, owner, repo string) (*Release, error) {
	return fetchLatestRelease(ctx, owner, repo)
}

// Apply downloads the appropriate asset for the current OS/arch from the
// release, decompresses it, and replaces the currently running binary.
// Returns the path to the replaced binary.
//
// On Unix the replacement is atomic (os.Rename on the same filesystem).
// On Windows the replacement is deferred to next launch (the running exe
// cannot be overwritten).
func Apply(ctx context.Context, release *Release) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("can't locate current executable: %w", err)
	}

	assetName := assetNameFor(runtime.GOOS, runtime.GOARCH)
	asset := release.assetByName(assetName)
	if asset == nil {
		// Try without the compression suffix as well.
		for i := range release.Assets {
			if release.Assets[i].Name == assetName {
				asset = &release.Assets[i]
				break
			}
		}
	}
	if asset == nil {
		return "", fmt.Errorf("no asset found for %s/%s (looked for %q)", runtime.GOOS, runtime.GOARCH, assetName)
	}

	data, err := downloadAsset(ctx, asset)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}

	newExe, err := decompressAndWrite(asset.Name, data)
	if err != nil {
		return "", fmt.Errorf("decompress %s: %w", asset.Name, err)
	}

	if err := replaceBinary(exe, newExe); err != nil {
		os.Remove(newExe)
		return "", fmt.Errorf("replace binary: %w", err)
	}

	return exe, nil
}

// assetNameFor returns the expected asset filename for the given OS and arch,
// e.g. "evva-darwin-arm64.tar.gz".
func assetNameFor(goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("evva-%s-%s.%s", goos, goarch, ext)
}
