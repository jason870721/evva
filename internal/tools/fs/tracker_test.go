package fs

import (
	"strings"
	"testing"
	"time"
)

func TestReadTracker_NotReadRejected(t *testing.T) {
	tr := NewReadTracker()
	ok, reason := tr.CanEdit("/x/y/z.go", time.Now())
	if ok {
		t.Fatal("CanEdit must reject unread path")
	}
	if !strings.Contains(reason, "has not been read") {
		t.Errorf("reason = %q, want 'has not been read' phrase", reason)
	}
}

func TestReadTracker_PartialViewRejected(t *testing.T) {
	tr := NewReadTracker()
	now := time.Now()
	tr.Record("/x/file", now, true)
	ok, reason := tr.CanEdit("/x/file", now)
	if ok {
		t.Fatal("CanEdit must reject partial-view read")
	}
	if !strings.Contains(reason, "partially read") {
		t.Errorf("reason = %q, want 'partially read' phrase", reason)
	}
}

func TestReadTracker_MtimeDriftRejected(t *testing.T) {
	tr := NewReadTracker()
	earlier := time.Now().Add(-time.Hour)
	tr.Record("/x/file", earlier, false)
	ok, reason := tr.CanEdit("/x/file", time.Now())
	if ok {
		t.Fatal("CanEdit must reject when file mtime advanced")
	}
	if !strings.Contains(reason, "modified since") {
		t.Errorf("reason = %q, want 'modified since' phrase", reason)
	}
}

func TestReadTracker_HappyPath(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	tr.Record("/x/file", mtime, false)
	ok, reason := tr.CanEdit("/x/file", mtime)
	if !ok {
		t.Fatalf("CanEdit must accept fresh full read; reason=%q", reason)
	}
}

func TestReadTracker_CanWriteMirrorsCanEdit(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	tr.Record("/x/file", mtime, false)
	if ok, _ := tr.CanWrite("/x/file", mtime); !ok {
		t.Fatal("CanWrite must accept what CanEdit accepts")
	}
	tr.Record("/x/file", mtime, true)
	if ok, _ := tr.CanWrite("/x/file", mtime); ok {
		t.Fatal("CanWrite must reject partial-view, same as CanEdit")
	}
}

func TestReadTracker_PathsCleanedConsistently(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	tr.Record("/x/./y/../file", mtime, false)
	if ok, _ := tr.CanEdit("/x/file", mtime); !ok {
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

	// Simulate Edit/Write post-mutation Record.
	tr.Record("/x/file", mtime, false)
	entry, _ := tr.Lookup("/x/file")
	if entry.HasReadOffset {
		t.Fatal("Record (used by Edit/Write) must leave HasReadOffset=false")
	}
	if entry.Timestamp.Equal(mtime) && !entry.IsPartialView && entry.HasReadOffset {
		t.Fatal("Record-only entry must not satisfy dedup condition")
	}

	// Simulate a real Read.
	tr.RecordRead("/x/file", mtime, false)
	entry, _ = tr.Lookup("/x/file")
	if !entry.HasReadOffset {
		t.Fatal("RecordRead must set HasReadOffset=true")
	}
	if !(entry.Timestamp.Equal(mtime) && !entry.IsPartialView && entry.HasReadOffset) {
		t.Fatal("RecordRead entry must satisfy dedup condition")
	}

	// CanEdit must still accept both — the guard only affects the
	// read dedup stub, not the edit/write safety check.
	if ok, _ := tr.CanEdit("/x/file", mtime); !ok {
		t.Fatal("CanEdit must accept file recorded by RecordRead")
	}
}

func TestReadTracker_Forget(t *testing.T) {
	tr := NewReadTracker()
	now := time.Now()
	tr.Record("/x/file", now, false)
	tr.Forget("/x/file")
	if ok, _ := tr.CanEdit("/x/file", now); ok {
		t.Fatal("after Forget, CanEdit must report not-read")
	}
}

func TestReadTracker_ConcurrentAccess(t *testing.T) {
	tr := NewReadTracker()
	mtime := time.Now()
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(i int) {
			path := "/x/" + string(rune('a'+(i%26)))
			tr.Record(path, mtime, false)
			tr.CanEdit(path, mtime)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}
