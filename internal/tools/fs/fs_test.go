package fs

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolvePathExpandsTilde locks down that `~/x` expands using the
// invoking user's home directory. Reported bug: under sudo (or any env
// where $HOME=/root but the user is non-root), `~/tmp` resolved to
// `/root/tmp` instead of the user's actual home. With SUDO_USER honored
// in resolveUserHome the expansion now follows the user's intent.
func TestResolvePathExpandsTilde(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("HOME", "/home/agent")

	got, err := resolvePath("~/tmp/notes.md", "")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := "/home/agent/tmp/notes.md"
	if got != want {
		t.Fatalf("resolvePath(~/tmp/notes.md) = %q, want %q", got, want)
	}
}

// TestResolvePathHonorsSudoUser proves that when $HOME points at /root
// (running under sudo) but $SUDO_USER names the real user, `~/x`
// resolves against the real user's home — the exact bug the user filed.
//
// The test needs a SUDO_USER whose homedir differs from /root (otherwise
// the assertion can't distinguish HOME shadowing from correct behavior).
// We look up the current user first; when running as root (CI containers,
// rootful docker) we fall back to scanning /etc/passwd for any local user
// whose homedir is not /root. Skipping when no such user exists keeps the
// test meaningful on minimal images.
func TestResolvePathHonorsSudoUser(t *testing.T) {
	username, homeDir := sudoUserCandidate(t)

	t.Setenv("HOME", "/root")
	t.Setenv("SUDO_USER", username)

	got, err := resolvePath("~/tmp/notes.md", "")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if strings.HasPrefix(got, "/root/") {
		t.Fatalf("path leaked /root — got %q (HOME shadowed SUDO_USER %q -> %q)", got, username, homeDir)
	}
	if !strings.HasSuffix(got, "/tmp/notes.md") {
		t.Fatalf("unexpected expansion: %q", got)
	}
}

// TestResolvePathAutoAbs locks down the second half of the fix:
// relative paths get auto-promoted to absolute via cfg.WorkDir
// instead of being rejected. Lets the model write "notes/todo.md"
// without plumbing the workdir itself.
func TestResolvePathAutoAbs(t *testing.T) {
	wd := t.TempDir()

	got, err := resolvePath("notes/todo.md", wd)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(wd, "notes", "todo.md")
	if got != want {
		t.Fatalf("resolvePath(notes/todo.md) = %q, want %q", got, want)
	}
}

// TestResolvePathPreservesAbsolute confirms an already-absolute path
// passes through untouched (modulo filepath.Clean).
func TestResolvePathPreservesAbsolute(t *testing.T) {
	got, err := resolvePath("/var/log/agent.log", "")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if got != "/var/log/agent.log" {
		t.Fatalf("resolvePath(/var/log/agent.log) = %q", got)
	}
}

// TestResolvePathRejectsEmpty keeps the empty-input contract — every
// fs tool relies on this guard to surface "file_path is required"
// rather than silently operating on the workdir root.
func TestResolvePathRejectsEmpty(t *testing.T) {
	if _, err := resolvePath("", ""); err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func lookupCurrentUsername(t *testing.T) string {
	t.Helper()
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	if u := strings.TrimSpace(os.Getenv("LOGNAME")); u != "" {
		return u
	}
	t.Skip("cannot determine current username from env")
	return ""
}

// sudoUserCandidate returns a (username, homeDir) pair suitable for
// driving the SUDO_USER-shadows-HOME test. Prefers the current user (the
// common path on dev laptops); when the current user is root or has
// homedir /root, falls back to /etc/passwd scanning for any user whose
// homedir is real and not /root. Skips the test when no candidate exists
// (minimal containers without local accounts beyond root).
func sudoUserCandidate(t *testing.T) (string, string) {
	t.Helper()
	if u, err := user.Current(); err == nil && u.HomeDir != "" && u.HomeDir != "/root" {
		return u.Username, u.HomeDir
	}
	for _, name := range []string{"nobody", "daemon", "bin", "mail", "games", "www-data", "ubuntu"} {
		if u, err := user.Lookup(name); err == nil && u.HomeDir != "" && u.HomeDir != "/root" {
			return u.Username, u.HomeDir
		}
	}
	t.Skip("no local user with a non-/root homedir to use as SUDO_USER candidate")
	return "", ""
}
