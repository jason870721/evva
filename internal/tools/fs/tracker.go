package fs

import (
	"path/filepath"
	"sync"
	"time"
)

// ReadTracker records every read_file call the agent makes so that
// edit_file / write_file can refuse to mutate a file whose on-disk
// state has drifted from what the model has in context.
//
// Three failure modes the tracker protects against, mirroring Claude
// Code's FileReadTool ↔ FileEditTool contract:
//
//  1. Never read — the model is editing blind. Reject.
//  2. Read but mtime advanced on disk afterwards — another process,
//     or the user, changed the file. Force a re-read so the model's
//     old_string still matches.
//  3. Partial-view read (offset / explicit limit) — the model only saw
//     a slice and has no idea what surrounds the edit. Force a full
//     re-read before mutating.
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
	// IsPartialView is true when the read used offset>0 or a
	// non-default limit — only part of the file made it into context.
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

// Record stores that absPath was read at the given mtime, with the
// partial flag indicating whether the read covered only a slice of
// the file. HasReadOffset is left false — callers that represent an
// actual Read tool invocation should use RecordRead instead so the
// dedup check can tell them apart from Edit/Write post-mutation
// updates.
func (t *ReadTracker) Record(absPath string, mtime time.Time, partial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == nil {
		t.state = make(map[string]readEntry)
	}
	t.state[filepath.Clean(absPath)] = readEntry{
		Timestamp:     mtime,
		IsPartialView: partial,
	}
}

// RecordRead is like Record but marks the entry as coming from an
// actual Read tool call. The Read tool's dedup check only fires for
// entries with HasReadOffset=true, so Edit/Write post-mutation
// updates (which call Record) don't cause spurious "File unchanged
// since last read" stubs.
func (t *ReadTracker) RecordRead(absPath string, mtime time.Time, partial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == nil {
		t.state = make(map[string]readEntry)
	}
	t.state[filepath.Clean(absPath)] = readEntry{
		Timestamp:     mtime,
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
// permitted given the file's current mtime. When false, reason is the
// model-facing explanation (mirrors ref TS FileEditTool wording).
func (t *ReadTracker) CanEdit(absPath string, currentMtime time.Time) (bool, string) {
	entry, found := t.Lookup(absPath)
	if !found {
		return false, "File has not been read yet. Read it first before writing to it."
	}
	if entry.IsPartialView {
		return false, "File was only partially read (offset/limit). Re-read the full file before editing."
	}
	if currentMtime.After(entry.Timestamp) {
		return false, "File has been modified since it was last read. Re-read it before editing."
	}
	return true, ""
}

// CanWrite reports whether write_file may overwrite absPath. Same gate
// as edit — overwriting a stale or partial-view read is the same
// hazard as editing one.
func (t *ReadTracker) CanWrite(absPath string, currentMtime time.Time) (bool, string) {
	return t.CanEdit(absPath, currentMtime)
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
