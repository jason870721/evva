package fs

import (
	"strings"
	"testing"
	"time"
)

func TestReadTracker_NotReadRejected(t *testing.T) {
	tr := NewReadTracker()
	ok, reason := tr.CanEdit("/x/y/z.go", time.Now(), HashContent("x"))
	if ok {
		t.Fatal("CanEdit must reject unread path")
	}
	if !strings.Contains(reason, "has not been read") {
		t.Errorf("reason = %q, want 'has not been read' phrase", reason)
	}
}

// Part 0: a truncated / offset (partial-view) read no longer blocks
// edits. ref stores offset/limit but never blocks on them, and evva's
// edit path re-reads the full file and requires a unique old_string
// match, so editing after seeing only a slice is safe.
func TestReadTracker_PartialViewAllowed(t *testing.T) {
	tr := NewReadTracker()
	now := time.Now()
	h := HashContent("hello world")
	tr.RecordRead("/x/file", now, true /* partial */, h)
	ok, reason := tr.CanEdit("/x/file", now, h)
	if !ok {
		t.Fatalf("CanEdit must allow editing after a partial-view read; reason=%q", reason)
	}
}

func TestReadTracker_MtimeDriftChangedContentRejected(t *testing.T) {
	tr := NewReadTracker()
	earlier := time.Now().Add(-time.Hour)
	tr.Record("/x/file", earlier, false, HashContent("old"))
	// Current content differs from the recorded read → real modification.
	ok, reason := tr.CanEdit("/x/file", time.Now(), HashContent("new"))
	if ok {
		t.Fatal("CanEdit must reject when mtime advanced and content changed")
	}
	if !strings.Contains(reason, "modified since") {
		t.Errorf("reason = %q, want 'modified since' phrase", reason)
	}
}

// Part 3: mtime advanced but the content is byte-identical (touch /
// formatter / cloud-sync). The content-hash fallback allows it.
func TestReadTracker_MtimeDriftSameContentAllowed(t *testing.T) {
	tr := NewReadTracker()
	earlier := time.Now().Add(-time.Hour)
	h := HashContent("unchanged content")
	tr.Record("/x/file", earlier, false, h)
	ok, reason := tr.CanEdit("/x/file", time.Now(), h)
	if !ok {
		t.Fatalf("CanEdit must allow when mtime advanced but content hash matches; reason=%q", reason)
	}
}

// Part 3: a zero stored hash (e.g. a PDF / notebook read) disables the
// fallback, so an mtime advance still forces a re-read.
func TestReadTracker_MtimeDriftZeroHashRejected(t *testing.T) {
	tr := NewReadTracker()
	earlier := time.Now().Add(-time.Hour)
	tr.Record("/x/file", earlier, false, [32]byte{})
	ok, _ := tr.CanEdit("/x/file", time.Now(), HashContent("whatever"))
	if ok {
		t.Fatal("CanEdit must reject mtime drift when the stored hash is zero (no fallback)")
	}
}

func TestReadTracker_HappyPath(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	h := HashContent("content")
	tr.Record("/x/file", mtime, false, h)
	ok, reason := tr.CanEdit("/x/file", mtime, h)
	if !ok {
		t.Fatalf("CanEdit must accept fresh full read; reason=%q", reason)
	}
}

func TestReadTracker_CanWriteMirrorsCanEdit(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	h := HashContent("content")
	tr.Record("/x/file", mtime, false, h)
	if ok, _ := tr.CanWrite("/x/file", mtime, h); !ok {
		t.Fatal("CanWrite must accept what CanEdit accepts")
	}
	// mtime drift + changed content → both must reject.
	if ok, _ := tr.CanWrite("/x/file", mtime.Add(time.Hour), HashContent("changed")); ok {
		t.Fatal("CanWrite must reject mtime drift with changed content, same as CanEdit")
	}
}

func TestReadTracker_PathsCleanedConsistently(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	h := HashContent("x")
	tr.Record("/x/./y/../file", mtime, false, h)
	if ok, _ := tr.CanEdit("/x/file", mtime, h); !ok {
		t.Fatal("recorded path /x/./y/../file should match lookup /x/file after Clean")
	}
}

func TestReadTracker_DedupRequiresReadOffset(t *testing.T) {
	// Regression: Edit/Write call Record() after mutation, which
	// updates the mtime. The Read tool's dedup check must NOT fire
	// for those entries — the model has never seen the post-edit
	// content. Only entries from RecordRead (HasReadOffset=true)
	// should qualify.
	tr := NewReadTracker()
	mtime := time.Now()
	h := HashContent("x")

	// Simulate Edit/Write post-mutation Record.
	tr.Record("/x/file", mtime, false, h)
	entry, _ := tr.Lookup("/x/file")
	if entry.HasReadOffset {
		t.Fatal("Record (used by Edit/Write) must leave HasReadOffset=false")
	}

	// Simulate a real Read.
	tr.RecordRead("/x/file", mtime, false, h)
	entry, _ = tr.Lookup("/x/file")
	if !entry.HasReadOffset {
		t.Fatal("RecordRead must set HasReadOffset=true")
	}
	if !(entry.Timestamp.Equal(mtime) && !entry.IsPartialView && entry.HasReadOffset) {
		t.Fatal("RecordRead entry must satisfy dedup condition")
	}

	// CanEdit must accept both — the guard only affects the read dedup
	// stub, not the edit/write safety check.
	if ok, _ := tr.CanEdit("/x/file", mtime, h); !ok {
		t.Fatal("CanEdit must accept file recorded by RecordRead")
	}
}

func TestReadTracker_Forget(t *testing.T) {
	tr := NewReadTracker()
	now := time.Now()
	h := HashContent("x")
	tr.Record("/x/file", now, false, h)
	tr.Forget("/x/file")
	if ok, _ := tr.CanEdit("/x/file", now, h); ok {
		t.Fatal("after Forget, CanEdit must report not-read")
	}
}

func TestReadTracker_ConcurrentAccess(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	h := HashContent("x")
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(i int) {
			path := "/x/" + string(rune('a'+(i%26)))
			tr.Record(path, mtime, false, h)
			tr.CanEdit(path, mtime, h)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}
