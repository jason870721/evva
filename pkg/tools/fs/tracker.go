package fs

import (
	"crypto/sha256"
	"path/filepath"
	"sync"
	"time"
)

// ReadTracker records every read_file call the agent makes so that
// edit_file / write_file can refuse to mutate a file whose on-disk
// state has drifted from what the model has in context.
//
// Two failure modes the tracker protects against, mirroring Claude
// Code's FileReadTool ↔ FileEditTool contract:
//
//  1. Never read — the model is editing blind. Reject.
//  2. Read but the file's bytes changed on disk afterwards — another
//     process, or the user, edited it. Force a re-read so the model's
//     old_string still reflects reality. Detected by an mtime advance,
//     with a content-hash fallback so a touch / formatter / cloud-sync
//     that bumps mtime without changing bytes does NOT force a re-read.
//
// A truncated or offset read is NOT treated as a blocking "partial
// view": ref stores offset/limit but never blocks edits on them, and
// evva's edit path re-reads the full file and requires old_string to
// match uniquely, so editing after seeing only a slice is safe.
// IsPartialView is retained only to gate the read-dedup "file unchanged"
// stub.
//
// Zero value is NOT usable; call NewReadTracker.
type ReadTracker struct {
	mu    sync.RWMutex
	state map[string]readEntry
}

type readEntry struct {
	// Timestamp is the file's mtime captured at the moment of the
	// read. We compare against the current on-disk mtime to detect
	// drift between reads and mutations.
	Timestamp time.Time
	// ContentHash is the SHA-256 of the full file content (LF-normalized,
	// as readFileWithEncoding returns it) at record time. Used as the
	// staleness fallback: if mtime advanced but the current content
	// hashes identically, the file wasn't really modified. Zero when the
	// recorder had no full-content view (e.g. PDF / notebook reads),
	// which disables the fallback for that entry.
	ContentHash [32]byte
	// IsPartialView is true when the read covered only a slice of the
	// file (offset>0 or truncated by the default limit). It no longer
	// gates edits — it is kept solely so the Read tool's dedup "file
	// unchanged" stub doesn't fire for a read that didn't see the whole
	// file.
	IsPartialView bool
	// HasReadOffset is true when this entry was recorded by the Read
	// tool (which always passes a concrete offset, even if 0). Edit
	// and Write record entries with HasReadOffset=false so the Read
	// tool's dedup check can distinguish "the model actually read
	// this file" from "a mutation updated the mtime." Mirrors the ref
	// TS check: existingState.offset !== undefined.
	HasReadOffset bool
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{state: make(map[string]readEntry)}
}

// HashContent returns the SHA-256 of s. Callers pass the LF-normalized
// full file content (what readFileWithEncoding produces) so the read
// site and the edit/write site hash the same representation and the
// staleness fallback can compare them. A zero [32]byte means "no
// full-content view" and disables the fallback.
func HashContent(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}

// Record stores that absPath was read at the given mtime, with the
// partial flag indicating whether the read covered only a slice of
// the file and contentHash the SHA-256 of the full file content (zero
// to disable the staleness fallback). HasReadOffset is left false —
// callers that represent an actual Read tool invocation should use
// RecordRead instead so the dedup check can tell them apart from
// Edit/Write post-mutation updates.
func (t *ReadTracker) Record(absPath string, mtime time.Time, partial bool, contentHash [32]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == nil {
		t.state = make(map[string]readEntry)
	}
	t.state[filepath.Clean(absPath)] = readEntry{
		Timestamp:     mtime,
		ContentHash:   contentHash,
		IsPartialView: partial,
	}
}

// RecordRead is like Record but marks the entry as coming from an
// actual Read tool call. The Read tool's dedup check only fires for
// entries with HasReadOffset=true, so Edit/Write post-mutation
// updates (which call Record) don't cause spurious "File unchanged
// since last read" stubs.
func (t *ReadTracker) RecordRead(absPath string, mtime time.Time, partial bool, contentHash [32]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == nil {
		t.state = make(map[string]readEntry)
	}
	t.state[filepath.Clean(absPath)] = readEntry{
		Timestamp:     mtime,
		ContentHash:   contentHash,
		IsPartialView: partial,
		HasReadOffset: true,
	}
}

// Lookup returns the recorded entry, or zero+false if absPath has
// never been recorded.
func (t *ReadTracker) Lookup(absPath string) (readEntry, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.state == nil {
		return readEntry{}, false
	}
	e, ok := t.state[filepath.Clean(absPath)]
	return e, ok
}

// CanEdit reports whether an edit_file call against absPath is
// permitted given the file's current mtime and content hash. When
// false, reason is the model-facing explanation (mirrors ref TS
// FileEditTool wording).
//
// currentHash is the SHA-256 of the file's current full content
// (LF-normalized). It is only consulted when mtime indicates drift:
// if the recorded read carried a full-content hash and it still equals
// currentHash, the mtime bump is spurious (touch / formatter / cloud
// sync) and the edit proceeds — matching ref's full-read content
// comparison.
func (t *ReadTracker) CanEdit(absPath string, currentMtime time.Time, currentHash [32]byte) (bool, string) {
	entry, found := t.Lookup(absPath)
	if !found {
		return false, "File has not been read yet. Read it first before writing to it."
	}
	if currentMtime.After(entry.Timestamp) {
		var zero [32]byte
		if entry.ContentHash != zero && entry.ContentHash == currentHash {
			// mtime advanced but bytes are identical — not a real edit.
			return true, ""
		}
		return false, "File has been modified since it was last read. Re-read it before editing."
	}
	return true, ""
}

// CanWrite reports whether write_file may overwrite absPath. Same gate
// as edit — overwriting a stale read is the same hazard as editing one.
func (t *ReadTracker) CanWrite(absPath string, currentMtime time.Time, currentHash [32]byte) (bool, string) {
	return t.CanEdit(absPath, currentMtime, currentHash)
}

// Forget drops the entry for absPath. Used by tests; never called in
// production code.
func (t *ReadTracker) Forget(absPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != nil {
		delete(t.state, filepath.Clean(absPath))
	}
}
