// Package checkpoint implements evva's per-turn "checkpoint & rewind" store:
// before each user turn the runtime records a checkpoint, and the first time a
// turn's fs tools (edit/write) mutate a file, that file's pre-mutation bytes
// are captured. A later /rewind restores the captured files (code), truncates
// the conversation to the turn boundary (chat), or both.
//
// On-disk layout, joining the .evva/* family (plans, worktrees):
//
//	<workdir>/.evva/checkpoints/<session-id>/
//	    cp-000001.json        # one Record per checkpoint (per user turn)
//	    cp-000002.json
//	    blobs/<sha256-hex>     # content-addressed before-images (raw file bytes)
//
// Before-images are stored as RAW file bytes (not the edit tool's
// LF-normalized in-memory copy) so a code-restore reproduces the original file
// exactly — encoding, BOM, and line endings included — with a plain
// os.WriteFile. Content-addressing dedupes a file re-touched across turns.
//
// The store mirrors internal/session's durability shape: atomic temp+rename
// writes, a versioned schema, and List that skips corrupt/too-new files rather
// than aborting the picker.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Version is the on-disk schema version for a Record. Bump on a breaking JSON
// change; List skips files with a higher version (forward-compat) the same way
// session.List does.
const Version = 1

// PreviewMaxBytes caps the persisted first-user-prompt preview shown in the
// /rewind picker. Matches session.PreviewMaxBytes intent.
const PreviewMaxBytes = 200

const (
	recordPrefix = "cp-"
	recordSuffix = ".json"
	blobsSubdir  = "blobs"
)

// maxSessionDirs bounds how many per-session checkpoint namespaces accumulate
// under the checkpoints root across sessions. Within-session retention caps
// each namespace; this caps how many namespaces persist as sessions come and
// go (the current session is always retained on top of this count).
const maxSessionDirs = 30

// FileRef records one file captured in a checkpoint.
//
// Existed=false marks a file the turn CREATED (it had no prior bytes): a
// code-restore deletes it. Existed=true carries the before-image's content
// hash; the blob lives at <session>/blobs/<Hash>.
type FileRef struct {
	Path    string `json:"path"`           // absolute path captured
	Existed bool   `json:"existed"`        // false → created this turn → delete on restore
	Hash    string `json:"hash,omitempty"` // sha256 hex of before-bytes; empty when !Existed
	Size    int    `json:"size"`           // before-image byte length (0 when !Existed)
}

// Record is one checkpoint: the conversation cut-point plus the before-images
// captured during the turn.
//
// CutLen is len(session.Messages) at turn start — truncating the live history
// to it rewinds the conversation to just before the turn. FullCompactCount is
// the session's full-compaction counter at capture time: a chat-restore is
// only coherent while it still matches the live session (a full compaction
// rewrites Messages into a single brief, invalidating every prior index — a
// micro compaction preserves indices and is fine). See the rewind PRD §5.2.
type Record struct {
	Version          int       `json:"version"`
	SessionID        string    `json:"session_id"`
	Seq              int       `json:"seq"` // monotonic per session, 1-based
	CreatedAt        time.Time `json:"created_at"`
	PromptPreview    string    `json:"prompt_preview"`
	CutLen           int       `json:"cut_len"`
	FullCompactCount int       `json:"full_compact_count"`
	Files            []FileRef `json:"files"`
}

// FileCount returns how many files the checkpoint captured.
func (r *Record) FileCount() int { return len(r.Files) }

// Retention bounds how many / how old checkpoints a session keeps. A zero
// field means "no limit on this axis"; both zero means unbounded (the store
// never prunes).
type Retention struct {
	MaxCount int           // keep at most this many newest checkpoints (0 = unlimited)
	MaxAge   time.Duration // drop checkpoints older than this (0 = no age limit)
}

// sessionDir returns <root>/<sessionID>, the per-session checkpoint directory.
func sessionDir(root, sessionID string) string {
	if root == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(root, sessionID)
}

// recordName is the on-disk filename for a checkpoint's Record. Zero-padded so
// a lexical directory sort matches numeric seq order.
func recordName(seq int) string {
	return fmt.Sprintf("%s%06d%s", recordPrefix, seq, recordSuffix)
}

// blobPath resolves the content-addressed before-image path for hash within
// dir (the per-session directory).
func blobPath(dir, hash string) string {
	return filepath.Join(dir, blobsSubdir, hash)
}

// hashBytes returns the lowercase hex sha256 of data — the blob's name.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeRecord persists r atomically to dir/cp-<seq>.json, creating dir if
// needed.
func writeRecord(dir string, r *Record) error {
	if dir == "" {
		return errors.New("checkpoint: empty session dir")
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal record: %w", err)
	}
	return writeAtomic(filepath.Join(dir, recordName(r.Seq)), data)
}

// readRecord parses a single Record file. A version newer than this build
// understands is an error (callers in List downgrade it to a skip+warn).
func readRecord(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("checkpoint: parse %s: %w", path, err)
	}
	if r.Version > Version {
		return nil, fmt.Errorf("checkpoint: %s has version %d (this evva supports up to %d)", path, r.Version, Version)
	}
	return &r, nil
}

// listRecords returns every checkpoint Record in dir, newest (highest seq)
// first. A missing dir yields an empty slice (the normal "no checkpoints yet"
// state). Corrupt / too-new files are skipped, never fatal.
func listRecords(dir string) ([]*Record, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: read dir %s: %w", dir, err)
	}
	out := make([]*Record, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, recordPrefix) || !strings.HasSuffix(name, recordSuffix) {
			continue
		}
		r, rerr := readRecord(filepath.Join(dir, name))
		if rerr != nil {
			continue // skip corrupt/too-new — one bad file must not break the picker
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	return out, nil
}

// maxSeq returns the highest checkpoint seq present in dir, or 0 when none.
func maxSeq(dir string) int {
	recs, _ := listRecords(dir)
	if len(recs) == 0 {
		return 0
	}
	return recs[0].Seq // listRecords sorts seq desc
}

// writeBlob stores data under dir/blobs/<hash>, skipping the write when the
// blob already exists (content-addressed: identical bytes share one file).
func writeBlob(dir, hash string, data []byte) error {
	p := blobPath(dir, hash)
	if _, err := os.Stat(p); err == nil {
		return nil // already present
	}
	return writeAtomic(p, data)
}

// readBlob loads a before-image by hash.
func readBlob(dir, hash string) ([]byte, error) {
	return os.ReadFile(blobPath(dir, hash))
}

// prune enforces ret over dir: it deletes the Record files that fall outside
// the count/age budget, then garbage-collects any blob no surviving Record
// references. Best-effort — individual removal errors are ignored so a locked
// file can't wedge the sweep.
func prune(dir string, ret Retention) {
	recs, err := listRecords(dir) // newest first
	if err != nil || len(recs) == 0 {
		return
	}
	cutoff := time.Time{}
	if ret.MaxAge > 0 {
		cutoff = time.Now().Add(-ret.MaxAge)
	}
	survivors := make([]*Record, 0, len(recs))
	for i, r := range recs {
		over := ret.MaxCount > 0 && i >= ret.MaxCount
		old := !cutoff.IsZero() && r.CreatedAt.Before(cutoff)
		if over || old {
			_ = os.Remove(filepath.Join(dir, recordName(r.Seq)))
			continue
		}
		survivors = append(survivors, r)
	}
	gcBlobs(dir, survivors)
}

// gcBlobs deletes every blob in dir/blobs not referenced by a surviving
// Record. Cheap set membership over the survivors' hashes.
func gcBlobs(dir string, survivors []*Record) {
	live := make(map[string]struct{})
	for _, r := range survivors {
		for _, f := range r.Files {
			if f.Hash != "" {
				live[f.Hash] = struct{}{}
			}
		}
	}
	blobDir := filepath.Join(dir, blobsSubdir)
	entries, err := os.ReadDir(blobDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := live[e.Name()]; !ok {
			_ = os.Remove(filepath.Join(blobDir, e.Name()))
		}
	}
}

// pruneSessionDirs bounds the number of per-session checkpoint namespaces under
// root, keeping the `keep` most-recently-modified directories. keepID (the live
// session) is always retained, even if it is not among the most recent. This is
// the cross-session footprint cap — within-session prune bounds each namespace;
// this bounds how many namespaces survive as sessions come and go. Best-effort:
// removal errors are ignored.
func pruneSessionDirs(root, keepID string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type dirAge struct {
		name  string
		mtime int64
	}
	var dirs []dirAge
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keepID {
			continue // keepID occupies a slot of its own; never a deletion candidate
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		dirs = append(dirs, dirAge{e.Name(), info.ModTime().UnixNano()})
	}
	budget := max(keep-1, 0) // the live session takes one of the `keep` slots
	if len(dirs) <= budget {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime > dirs[j].mtime })
	for _, d := range dirs[budget:] {
		_ = os.RemoveAll(filepath.Join(root, d.name))
	}
}

// writeAtomic writes data to path via a sibling temp file + rename, creating
// the parent directory chain. Mirrors session.writeAtomic — duplicated so the
// checkpoint package stays dependency-free.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("checkpoint: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("checkpoint: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("checkpoint: write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("checkpoint: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("checkpoint: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// withinDir reports whether abs sits strictly inside root (not root itself,
// not a sibling, not reachable only via ".."). The restore-time guard that
// keeps a code-restore from ever writing outside the workdir.
func withinDir(root, abs string) bool {
	if root == "" || abs == "" {
		return false
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(abs)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}
