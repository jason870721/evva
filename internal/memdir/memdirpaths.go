package memdir

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// MemoryDirName is the single global auto-memory directory under AppHome.
	// Port of paths.ts:AUTO_MEM_DIRNAME. evva diverges from ref's per-git-root
	// keying — one global store, no project key (PRD §5.2).
	MemoryDirName = "memory"

	// MemoryIndexFile is the always-loaded index inside the memory dir. The
	// model maintains it (one `- [Title](file.md) — hook` line per memory); Go
	// only reads it. Port of paths.ts:AUTO_MEM_ENTRYPOINT_NAME.
	MemoryIndexFile = "MEMORY.md"
)

// MemoryDir returns <appHome>/memory — the one global memory store. Empty
// appHome yields "".
func MemoryDir(appHome string) string {
	if appHome == "" {
		return ""
	}
	return filepath.Join(appHome, MemoryDirName)
}

// MemoryIndexPath returns <appHome>/memory/MEMORY.md. Empty appHome yields "".
func MemoryIndexPath(appHome string) string {
	dir := MemoryDir(appHome)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, MemoryIndexFile)
}

// EnsureMemoryDir creates the memory dir (mkdir -p). Idempotent; called once at
// session start so the model can write a memory file without first checking the
// dir exists — the prompt's "this directory already exists" claim depends on it
// (ensureMemoryDirExists parity, memdir.ts:129). Never fatal in spirit: a real
// failure (EACCES/EROFS) is returned for the caller to log, and the model's own
// Write would surface the underlying error anyway. Empty appHome is a no-op.
func EnsureMemoryDir(appHome string) error {
	dir := MemoryDir(appHome)
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// IsInMemoryDir reports whether absPath is confined within MemoryDir(appHome).
// Same filepath.Rel containment shape as permission.IsPlanFilePath: rejects the
// dir root itself, "..", siblings, and absolute-elsewhere. Empty appHome or a
// path that can't be proven contained → false.
//
// Used by the per-turn recall read-file guard (the permission write carve-out
// uses permission.IsAutoMemPath, which keeps pkg/permission free of an
// internal/memdir import — same logic, two homes by design).
func IsInMemoryDir(appHome, absPath string) bool {
	dir := MemoryDir(appHome)
	if dir == "" || absPath == "" {
		return false
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(absPath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}
