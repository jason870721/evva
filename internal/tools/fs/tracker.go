package fs

import "path/filepath"

// ReadTracker records file paths the agent has called read_file on.
// Zero value is ready to use.
type ReadTracker struct {
	seen map[string]struct{}
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		seen: make(map[string]struct{}),
	}
}

// MarkRead records that path was read.
func (t *ReadTracker) MarkRead(absPath string) {
	if t.seen == nil {
		t.seen = make(map[string]struct{})
	}
	t.seen[filepath.Clean(absPath)] = struct{}{}
}

// WasRead reports whether path has been marked via MarkRead.
func (t *ReadTracker) WasRead(absPath string) bool {
	if t.seen == nil {
		return false
	}
	_, ok := t.seen[filepath.Clean(absPath)]
	return ok
}
