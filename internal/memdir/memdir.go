// Package memdir loads the two on-disk memory files that seed the agent's
// system prompt at session start:
//
//   - <workdir>/EVVA.md       project memory — repo conventions, hot facts
//   - <evvaHome>/USER_PROFILE.md   user memory — preferences, working style
//
// Both files are optional. Missing files yield a zero-value Snapshot field
// and no warning; the prompt builder skips empty sections cleanly. Any
// non-missing read failure (permission, oversize) is recorded in
// Snapshot.Warnings — Load itself never returns an error so the agent can
// always boot.
//
// This package depends only on stdlib. It is not imported by the sysprompt
// package; the caller threads Snapshot.ProjectMemory / .UserProfile into
// the prompt context, keeping the dependency arrow one-way.
package memdir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// File names. Exposed so other packages (Phase 9 user-profile background
// agent, future /memory slash commands) can write to the same paths without
// re-spelling them.
const (
	ProjectMemoryFile = "EVVA.md"
	UserProfileFile   = "USER_PROFILE.md"
)

// MaxFileBytes caps each memory file at 64 KiB. Past that the user is
// almost certainly using EVVA.md for the wrong thing (knowledge base, not
// conventions doc); we truncate and warn rather than refuse outright so a
// bloated file doesn't break the session.
const MaxFileBytes = 64 * 1024

// Snapshot is one session's view of the two memory files. Either body field
// may be empty when the file is missing, empty, or unreadable; callers
// treat empty as "skip the section."
type Snapshot struct {
	ProjectMemory string   // raw contents of <workdir>/EVVA.md
	UserProfile   string   // raw contents of <evvaHome>/USER_PROFILE.md
	Warnings      []string // non-fatal: oversize-truncation, permission errors
}

// Load reads both memory files. Empty workdir or evvaHome silently skips
// that file. Files larger than MaxFileBytes are truncated with a warning.
// The function never returns an error.
func Load(workdir, evvaHome string) Snapshot {
	var snap Snapshot
	if workdir != "" {
		body, warn := readMemFile(filepath.Join(workdir, ProjectMemoryFile))
		snap.ProjectMemory = body
		if warn != "" {
			snap.Warnings = append(snap.Warnings, warn)
		}
	}
	if evvaHome != "" {
		body, warn := readMemFile(filepath.Join(evvaHome, UserProfileFile))
		snap.UserProfile = body
		if warn != "" {
			snap.Warnings = append(snap.Warnings, warn)
		}
	}
	return snap
}

// readMemFile reads at most MaxFileBytes from path. Returns (body, warning).
// Missing files return ("", "") so the caller can skip cleanly. Read errors
// other than os.IsNotExist return ("", "<reason>"); oversize files return
// (truncated, "<reason>"). LimitReader bounds the read so a runaway 1 GB
// EVVA.md doesn't pull the world into memory before we truncate.
func readMemFile(path string) (string, string) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ""
		}
		return "", fmt.Sprintf("memdir: cannot read %s: %v", path, err)
	}
	defer f.Close()

	buf, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return "", fmt.Sprintf("memdir: read %s: %v", path, err)
	}
	if len(buf) > MaxFileBytes {
		return string(buf[:MaxFileBytes]), fmt.Sprintf("memdir: %s truncated to %d bytes (cap %d)", path, MaxFileBytes, MaxFileBytes)
	}
	return string(buf), ""
}
