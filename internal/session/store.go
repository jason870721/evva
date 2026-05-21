// Session storage on disk. One JSON file per session at
//
//	<APP_HOME>/sessions/<workdir-slug>/<session-id>.json
//
// The store is intentionally small: Save / Load / List / Delete on
// straightforward filesystem primitives. List sorts by file mtime
// descending so the most recently active session lands at the top of
// the resume picker.
//
// Corrupt files (truncated writes from a crashed evva, JSON drift after a
// schema bump) are skipped with a warning during List — one broken file
// must never disable the picker for the whole directory.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// SessionsSubdir is the directory under APP_HOME that holds every
// persisted session, organized one level deeper by workdir slug.
const SessionsSubdir = "sessions"

// sessionFileSuffix is the on-disk extension for a single snapshot.
const sessionFileSuffix = ".json"

// SessionsDir returns the absolute path of the per-workdir directory
// holding this workdir's session files. Empty inputs yield "".
func SessionsDir(appHome, workdirSlug string) string {
	if appHome == "" || workdirSlug == "" {
		return ""
	}
	return filepath.Join(appHome, SessionsSubdir, workdirSlug)
}

// SessionFilePath resolves the snapshot file for one session-id.
func SessionFilePath(appHome, workdirSlug, sessionID string) string {
	dir := SessionsDir(appHome, workdirSlug)
	if dir == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(dir, sessionID+sessionFileSuffix)
}

// Save serializes snap to <SessionsDir>/<SessionID>.json atomically
// (temp + rename in the same directory). Creates the parent directory
// chain if missing. Returns an error only on real I/O failure.
func Save(appHome string, snap *Snapshot) error {
	if snap == nil {
		return errors.New("session: cannot save nil snapshot")
	}
	if snap.WorkdirSlug == "" || snap.SessionID == "" {
		return fmt.Errorf("session: snapshot missing workdir_slug or session_id (slug=%q id=%q)",
			snap.WorkdirSlug, snap.SessionID)
	}
	path := SessionFilePath(appHome, snap.WorkdirSlug, snap.SessionID)
	if path == "" {
		return fmt.Errorf("session: cannot resolve path (appHome=%q)", appHome)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}
	return writeAtomic(path, data)
}

// Load reads a single snapshot off disk. Returns os.ErrNotExist (wrapped)
// when the file is missing so callers can distinguish "no such session"
// from real I/O / parse errors.
func Load(appHome, workdirSlug, sessionID string) (*Snapshot, error) {
	path := SessionFilePath(appHome, workdirSlug, sessionID)
	if path == "" {
		return nil, fmt.Errorf("session: cannot resolve path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session: read %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("session: parse %s: %w", path, err)
	}
	if snap.Version > SnapshotVersion {
		return nil, fmt.Errorf("session: %s has version %d (this evva supports up to %d)",
			path, snap.Version, SnapshotVersion)
	}
	return &snap, nil
}

// ListEntry is one row in the resume picker: the snapshot plus the file
// mtime List uses to sort. The picker only needs a small subset of the
// snapshot fields — exposed separately so the picker can stay shallow.
type ListEntry struct {
	Snapshot *Snapshot
	MTime    int64 // unix nano of file mtime; List sorts by this desc
}

// List enumerates every session under <SessionsDir>/<workdir-slug>/,
// sorted by mtime descending (most recently saved first). Files that
// fail to parse are skipped — the corresponding error appears in the
// returned warnings slice so the caller can surface them.
//
// Returns an empty slice (not an error) when the directory does not
// exist yet — that's the normal "no prior sessions" state.
func List(appHome, workdirSlug string) ([]ListEntry, []string, error) {
	dir := SessionsDir(appHome, workdirSlug)
	if dir == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("session: read dir %s: %w", dir, err)
	}
	var warnings []string
	out := make([]ListEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != sessionFileSuffix {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("session: stat %s: %v", path, err))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("session: read %s: %v", path, err))
			continue
		}
		var snap Snapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			warnings = append(warnings, fmt.Sprintf("session: parse %s: %v", path, err))
			continue
		}
		if snap.Version > SnapshotVersion {
			warnings = append(warnings, fmt.Sprintf("session: %s has version %d (skipping; this evva supports up to %d)",
				path, snap.Version, SnapshotVersion))
			continue
		}
		out = append(out, ListEntry{Snapshot: &snap, MTime: info.ModTime().UnixNano()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out, warnings, nil
}

// Delete removes a single snapshot file. Missing files are not an error
// (idempotent — second delete is a no-op).
func Delete(appHome, workdirSlug, sessionID string) error {
	path := SessionFilePath(appHome, workdirSlug, sessionID)
	if path == "" {
		return fmt.Errorf("session: cannot resolve path")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("session: delete %s: %w", path, err)
	}
	return nil
}

// writeAtomic writes `data` to `path` by creating a sibling temp file
// and renaming it into place. Mirrors memdir.writeAtomic — duplicated
// here so the session package stays free of internal/memdir imports
// (memdir already depends on session-free utilities; cyclic risk if we
// ever flip the arrow).
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("session: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("session: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session: write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}
