// Package fs exposes filesystem tools (Read, Write, Edit) as stateless
// singletons. Construction policy (eager vs lazy) is decided by the agent;
// this package only knows how to produce tool instances.
package fs

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

// fileChangedSince reports whether the file at path now has an mtime
// strictly after baseline. It is the pre-write TOCTOU guard: edit and
// write capture the file's mtime when they read it, then call this right
// before writing — if the file changed on disk in between (user, linter,
// another process), the write is aborted rather than clobbering the
// concurrent modification with content computed from stale bytes. A stat
// error returns false; the subsequent write attempt surfaces the real
// error with better context.
func fileChangedSince(path string, baseline time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().After(baseline)
}

// Names lists every tool name this package contributes, in canonical order.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.READ_FILE, tools.WRITE_FILE, tools.EDIT_FILE, tools.GLOB}
}

// resolvePath normalizes a model-supplied file path into a cleaned
// absolute path on disk. Three transforms in order:
//
//  1. A leading `~` / `~/` expands to the invoking user's home dir
//     (see resolveUserHome — robust against sudo / container envs
//     where `$HOME=/root`).
//  2. Relative paths are joined onto workdir so the model never has
//     to plumb the workdir itself; "make a file at notes/todo.md"
//     Just Works. Pass "" to fall back to os.Getwd() (test convenience).
//  3. The result is filepath.Cleaned to collapse `..`, double
//     slashes, and `.` segments.
//
// Returns an error only when the input is empty or `~` expansion
// fails outright — both signal a misconfigured agent or environment
// rather than user intent.
func resolvePath(pathStr, workdir string) (string, error) {
	if pathStr == "" {
		return "", fmt.Errorf("file_path is required")
	}
	// Reject UNC / network paths (\\server\share, //server/share) before
	// any normalization. Filesystem operations on these can leak NTLM
	// credentials to a remote host; filepath.Clean would also collapse the
	// leading // and hide the intent. Mirrors ref's UNC short-circuit.
	if strings.HasPrefix(pathStr, `\\`) || strings.HasPrefix(pathStr, "//") {
		return "", fmt.Errorf("UNC / network paths are not allowed: %s", pathStr)
	}
	expanded, err := expandHome(pathStr)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		if workdir == "" {
			workdir, _ = os.Getwd()
		}
		expanded = filepath.Join(workdir, expanded)
	}
	return filepath.Clean(expanded), nil
}

// expandHome resolves a leading `~` or `~/` against the invoking
// user's home directory. Any other tilde (e.g. `~bob/foo`) is left
// untouched — per-user lookup is out of scope for now.
func expandHome(p string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if len(p) > 1 && p[1] != '/' && p[1] != filepath.Separator {
		return p, nil
	}
	home, err := resolveUserHome()
	if err != nil {
		return "", fmt.Errorf("expand ~: %w", err)
	}
	if len(p) == 1 {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// resolveUserHome returns the directory the invoking user expects `~`
// to mean. Resolution order matters — the first source that yields a
// non-empty value wins:
//
//  1. $SUDO_USER's HomeDir — when running under sudo, the user almost
//     always wants `~` to mean their *original* home, not `/root`.
//     This was the reported bug: `~/tmp` silently became `/root/tmp`
//     because $HOME inherited from the sudo session points at root.
//  2. $HOME — the conventional source. Reliable in normal shells.
//  3. user.Current().HomeDir — last-ditch lookup against /etc/passwd
//     for environments where $HOME got unset entirely.
//
// The chain returns an error only when every source fails, which
// shouldn't happen on any well-formed system.
func resolveUserHome() (string, error) {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil && u.HomeDir != "" {
			return u.HomeDir, nil
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir, nil
	}
	return "", errors.New("could not determine user home directory")
}

