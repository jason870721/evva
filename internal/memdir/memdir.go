// Package memdir loads the on-disk memory that seeds the agent's system prompt
// at session start, and provides the read primitives for evva's typed-memory
// directory:
//
//   - <workdir>/EVVA.md          workdir memory — repo conventions (user-authored)
//   - <appHome>/memory/          one global auto-memory directory of typed *.md files
//   - <appHome>/memory/MEMORY.md the always-injected index (maintained by the model)
//
// Individual memory files carry frontmatter (name / description / type) and are
// pulled into a turn on demand by the relevance retriever in the recall
// sub-package; only the MEMORY.md index injects statically into the prompt.
// The model writes memory files itself with the standard write/edit tools — a
// permission write carve-out auto-allows writes confined to the memory dir.
//
// All files are optional. Missing files yield zero-value Snapshot fields and no
// warning; the prompt builder skips empty sections cleanly. Any non-missing read
// failure (permission, oversize) is recorded in Snapshot.Warnings — Load itself
// never returns an error so the agent can always boot.
//
// This package depends only on stdlib. It is not imported by the sysprompt
// package; the caller threads Snapshot fields into the prompt context, keeping
// the dependency arrow one-way. The relevance retriever needs llm.Client, so it
// lives in the internal/memdir/recall sub-package rather than here.
package memdir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ProjectMemoryFile is the basename of the user-authored, repo-scoped memory
// file read from the workdir. (EVVA.md is conventions, NOT auto-memory — it is
// out of scope for the typed-memory directory and left exactly as-is.)
const ProjectMemoryFile = "EVVA.md"

// MaxFileBytes caps each read memory file at 64 KiB. Past that the user is
// almost certainly using EVVA.md for the wrong thing (knowledge base, not
// conventions doc); we truncate and warn rather than refuse outright so a
// bloated file doesn't break the session.
const MaxFileBytes = 64 * 1024

// Snapshot is one session's view of the on-disk memory. Any field may be empty
// when the underlying file is missing, empty, or unreadable; callers treat
// empty as "skip the section."
type Snapshot struct {
	WorkdirMemory string   // raw contents of <workdir>/EVVA.md (user-authored, repo-scoped)
	MemoryIndex   string   // <appHome>/memory/MEMORY.md body, truncated + inject-ready; "" when absent or auto-memory off
	MemoryDir     string   // absolute <appHome>/memory when auto-memory is on; "" otherwise (the per-turn recall + carve-out gate on this)
	Warnings      []string // non-fatal: oversize-truncation, permission errors
}

// Load reads the memory that seeds a session. EVVA.md is always read (it is
// user-authored conventions, independent of the auto-memory toggle). When
// autoMemory is true and appHome is set, Load also ensures the global memory
// dir exists (so the model can write without checking), records its path, and
// reads the MEMORY.md index. The function never returns an error.
func Load(workdir, appHome string, autoMemory bool) Snapshot {
	var snap Snapshot
	if workdir != "" {
		body, warn := readMemFile(filepath.Join(workdir, ProjectMemoryFile))
		snap.WorkdirMemory = body
		if warn != "" {
			snap.Warnings = append(snap.Warnings, warn)
		}
	}
	if autoMemory && appHome != "" {
		if err := EnsureMemoryDir(appHome); err != nil {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("memdir: ensure %s: %v", MemoryDir(appHome), err))
		} else {
			// Only advertise the dir to the recall + carve-out paths once it
			// exists — an empty MemoryDir disables both (auto-memory-off parity).
			snap.MemoryDir = MemoryDir(appHome)
		}
		index, warn := ReadIndex(appHome)
		snap.MemoryIndex = index
		if warn != "" {
			snap.Warnings = append(snap.Warnings, "memdir: "+warn)
		}
	}
	return snap
}

// readMemFile reads at most MaxFileBytes from path. Returns (body, warning).
// Missing files return ("", "") so the caller can skip cleanly. Read errors
// other than os.IsNotExist return ("", "<reason>"); oversize files return
// (truncated, "<reason>"). LimitReader bounds the read so a runaway 1 GB file
// doesn't pull the world into memory before we truncate.
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
