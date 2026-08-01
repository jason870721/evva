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
	"time"
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

// Header is one row in a session picker: the snapshot envelope, the
// message count, and the file mtime List sorts by.
//
// Listing decodes into this rather than into a full Snapshot. Messages
// dominate a snapshot's bytes — the envelope is a few hundred of them and
// the conversation is the rest — so a picker that materialized every
// message body to render a preview line would pay for the whole store to
// show a dozen rows. Measured on a real store (93 sessions, 14 MB across
// 14 workdirs): 66 ms header-only against 107 ms full. That margin is why
// v1.19 could add machine-wide listing without adding an index.
type Header struct {
	Meta
	MessageCount int   // len(session.messages), counted without decoding bodies
	MTime        int64 // unix nano of file mtime; List sorts by this desc
}

// headerRow is the decode target: the envelope, plus messages as raw
// json so len() is available without unmarshalling a single message.
type headerRow struct {
	Meta
	Session struct {
		Messages []json.RawMessage `json:"messages"`
	} `json:"session"`
}

// List enumerates every session under <SessionsDir>/<workdir-slug>/,
// sorted by mtime descending (most recently saved first). Files that
// fail to parse are skipped — the corresponding error appears in the
// returned warnings slice so the caller can surface them.
//
// Returns an empty slice (not an error) when the directory does not
// exist yet — that's the normal "no prior sessions" state.
func List(appHome, workdirSlug string) ([]Header, []string, error) {
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
	out, warnings := readHeaders(dir, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out, warnings, nil
}

// ListAll enumerates sessions across EVERY workdir slug, newest first.
//
// The machine-wide view behind `evva sessions list` and the picker's
// all-workdirs toggle: a session's home directory is where it was started,
// which is not always where the operator is standing when they go looking
// for it. A slug directory that cannot be read is skipped rather than
// failing the whole listing — one unreadable project must not hide the
// other fourteen.
func ListAll(appHome string) ([]Header, []string, error) {
	if appHome == "" {
		return nil, nil, nil
	}
	root := filepath.Join(appHome, SessionsSubdir)
	slugs, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("session: read sessions root %s: %w", root, err)
	}
	var (
		out      []Header
		warnings []string
	)
	for _, slug := range slugs {
		if !slug.IsDir() {
			continue
		}
		dir := filepath.Join(root, slug.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("session: read dir %s: %v", dir, err))
			continue
		}
		rows, warn := readHeaders(dir, entries)
		out = append(out, rows...)
		warnings = append(warnings, warn...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out, warnings, nil
}

// readHeaders decodes every *.json in dir into a Header. Unsorted — both
// callers sort, and ListAll must sort across directories anyway.
func readHeaders(dir string, entries []os.DirEntry) ([]Header, []string) {
	var warnings []string
	out := make([]Header, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != sessionFileSuffix {
			continue
		}
		path := filepath.Join(dir, e.Name())
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
		var row headerRow
		if err := json.Unmarshal(data, &row); err != nil {
			warnings = append(warnings, fmt.Sprintf("session: parse %s: %v", path, err))
			continue
		}
		if row.Version > SnapshotVersion {
			warnings = append(warnings, fmt.Sprintf("session: %s has version %d (skipping; this evva supports up to %d)",
				path, row.Version, SnapshotVersion))
			continue
		}
		out = append(out, Header{
			Meta:         row.Meta,
			MessageCount: len(row.Session.Messages),
			MTime:        info.ModTime().UnixNano(),
		})
	}
	return out, warnings
}

// CountTouchedSince counts persisted sessions across ALL workdir slugs whose
// file mtime is after `since`, excluding excludeID (the current session, whose
// mtime is always recent). evva's auto-memory is one global store, so the
// dream activity-gate counts activity in every project — not just the current
// workdir — as the signal that enough has accumulated to consolidate.
//
// Cheap and parse-free: one ReadDir per slug + a stat per file (mtime + name
// are all that matter here; the snapshot JSON is never decoded). A missing
// sessions root is 0, not an error — that's the normal "no prior sessions"
// state. An unreadable individual slug dir is skipped, not fatal.
func CountTouchedSince(appHome string, since time.Time, excludeID string) (int, error) {
	if appHome == "" {
		return 0, nil
	}
	root := filepath.Join(appHome, SessionsSubdir)
	slugs, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("session: read sessions root %s: %w", root, err)
	}
	sinceNano := since.UnixNano()
	var excludeFile string
	if excludeID != "" {
		excludeFile = excludeID + sessionFileSuffix
	}
	count := 0
	for _, slug := range slugs {
		if !slug.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, slug.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != sessionFileSuffix {
				continue
			}
			if excludeFile != "" && f.Name() == excludeFile {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if info.ModTime().UnixNano() > sinceNano {
				count++
			}
		}
	}
	return count, nil
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
