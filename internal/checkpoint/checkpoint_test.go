package checkpoint

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A turn that touches no file still leaves a conversation-only checkpoint that
// carries the cut-point and compaction epoch.
func TestBeginConversationOnly(t *testing.T) {
	wd := t.TempDir()
	m := NewManager(wd, "s1", Retention{}, nil)
	m.Begin(5, 2, "  do the\nthing  ")

	recs := m.List()
	if len(recs) != 1 {
		t.Fatalf("want 1 checkpoint, got %d", len(recs))
	}
	r := recs[0]
	if r.Seq != 1 || r.CutLen != 5 || r.FullCompactCount != 2 || r.FileCount() != 0 {
		t.Fatalf("unexpected record: %+v", r)
	}
	if r.PromptPreview != "do the thing" {
		t.Fatalf("preview = %q, want flattened+trimmed", r.PromptPreview)
	}
}

// First-touch wins and the call is idempotent within a turn; restore rewrites
// the captured before-image over a since-changed file.
func TestCaptureAndRestoreExisting(t *testing.T) {
	wd := t.TempDir()
	f := filepath.Join(wd, "a.txt")
	if err := os.WriteFile(f, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(wd, "s1", Retention{}, nil)
	m.Begin(0, 0, "edit a")
	m.CaptureBefore(f)
	// A later edit in the same turn changes the file then captures again —
	// the earliest before-image must survive.
	_ = os.WriteFile(f, []byte("interim\n"), 0o644)
	m.CaptureBefore(f)

	rec := m.List()[0]
	if rec.FileCount() != 1 {
		t.Fatalf("want 1 captured file (idempotent), got %d", rec.FileCount())
	}
	if !rec.Files[0].Existed {
		t.Fatalf("file existed before; Existed should be true")
	}

	// Simulate the turn's final mutation, then restore.
	_ = os.WriteFile(f, []byte("totally changed\n"), 0o644)
	res := m.RestoreCode(rec)
	if res.Restored != 1 || res.Deleted != 0 || len(res.Errors) != 0 {
		t.Fatalf("restore result = %+v", res)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "original\n" {
		t.Fatalf("restored content = %q, want original", string(got))
	}
}

// A file created during the turn (no prior bytes) is deleted on restore.
func TestCaptureMissingThenDeleteOnRestore(t *testing.T) {
	wd := t.TempDir()
	f := filepath.Join(wd, "sub", "new.txt")
	m := NewManager(wd, "s1", Retention{}, nil)
	m.Begin(0, 0, "create new")
	m.CaptureBefore(f) // does not exist yet

	rec := m.List()[0]
	if rec.FileCount() != 1 || rec.Files[0].Existed {
		t.Fatalf("want one Existed=false ref, got %+v", rec.Files)
	}

	// Simulate the create, then restore → file gone.
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	_ = os.WriteFile(f, []byte("hi\n"), 0o644)
	res := m.RestoreCode(rec)
	if res.Deleted != 1 || res.Restored != 0 {
		t.Fatalf("restore result = %+v", res)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatalf("created file should be deleted on restore")
	}
}

// Before-images are raw bytes: a CRLF file normalized to LF by an edit comes
// back byte-identical (encoding/line-endings preserved, the whole point of
// capturing raw rather than the tool's LF-normalized copy).
func TestRestorePreservesRawBytes(t *testing.T) {
	wd := t.TempDir()
	f := filepath.Join(wd, "crlf.txt")
	raw := []byte("alpha\r\nbeta\r\n")
	if err := os.WriteFile(f, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(wd, "s1", Retention{}, nil)
	m.Begin(0, 0, "x")
	m.CaptureBefore(f)

	_ = os.WriteFile(f, []byte("alpha\nbeta\n"), 0o644) // LF-normalized
	m.RestoreCode(m.List()[0])

	got, _ := os.ReadFile(f)
	if !bytes.Equal(got, raw) {
		t.Fatalf("restored = %q, want raw CRLF %q", got, raw)
	}
}

// A code restore refuses any path that escapes the workdir and leaves it
// untouched.
func TestRestoreRefusesOutsideWorkdir(t *testing.T) {
	wd := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "evil.txt")
	if err := os.WriteFile(outside, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(wd, "s1", Retention{}, nil)
	m.Begin(0, 0, "x")

	r := &Record{Files: []FileRef{{Path: outside, Existed: true, Hash: "deadbeef"}}}
	res := m.RestoreCode(r)
	if res.Restored != 0 || res.Deleted != 0 || len(res.Errors) != 1 {
		t.Fatalf("want exactly one refusal, got %+v", res)
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "keep me\n" {
		t.Fatalf("outside-workdir file must be untouched, got %q", string(got))
	}
}

// Retention keeps at most MaxCount newest checkpoints and garbage-collects the
// blobs the dropped checkpoints uniquely owned.
func TestPruneMaxCountAndBlobGC(t *testing.T) {
	wd := t.TempDir()
	m := NewManager(wd, "s1", Retention{MaxCount: 2}, nil)
	for i := range 3 {
		f := filepath.Join(wd, fmt.Sprintf("f%d.txt", i))
		_ = os.WriteFile(f, fmt.Appendf(nil, "content-%d\n", i), 0o644)
		m.Begin(0, 0, fmt.Sprintf("turn %d", i))
		m.CaptureBefore(f)
	}

	recs := m.List()
	if len(recs) != 2 {
		t.Fatalf("want 2 checkpoints after prune, got %d", len(recs))
	}
	if recs[0].Seq != 3 || recs[1].Seq != 2 {
		t.Fatalf("want seqs [3 2] newest-first, got [%d %d]", recs[0].Seq, recs[1].Seq)
	}

	// Seq 1's blob (content-0) must be gone; seqs 2 & 3's blobs remain.
	blobDir := filepath.Join(wd, ".evva", "checkpoints", "s1", "blobs")
	entries, _ := os.ReadDir(blobDir)
	if len(entries) != 2 {
		t.Fatalf("want 2 surviving blobs, got %d", len(entries))
	}
	if string(mustReadByHash(t, m, recs[1])) != "content-1\n" {
		t.Fatalf("seq 2 before-image content wrong")
	}
}

// Age-based retention drops checkpoints older than MaxAge on the next Begin.
func TestPruneMaxAge(t *testing.T) {
	wd := t.TempDir()
	m := NewManager(wd, "s1", Retention{MaxAge: time.Hour}, nil)
	m.Begin(0, 0, "old")

	// Backdate the first checkpoint well past the age limit.
	old := filepath.Join(wd, ".evva", "checkpoints", "s1", recordName(1))
	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	// Rewrite its CreatedAt too (prune reads the field, not mtime).
	r, err := readRecord(old)
	if err != nil {
		t.Fatal(err)
	}
	r.CreatedAt = stale
	if err := writeRecord(filepath.Dir(old), r); err != nil {
		t.Fatal(err)
	}

	m.Begin(0, 0, "new") // prunes the stale one
	recs := m.List()
	if len(recs) != 1 || recs[0].PromptPreview != "new" {
		t.Fatalf("stale checkpoint should be pruned, got %+v", recs)
	}
}

// SetSession re-scopes to a fresh namespace and drops the in-progress turn.
func TestSetSessionRescopes(t *testing.T) {
	wd := t.TempDir()
	m := NewManager(wd, "s1", Retention{}, nil)
	m.Begin(0, 0, "s1 turn")
	if len(m.List()) != 1 {
		t.Fatal("expected one checkpoint in s1")
	}
	m.SetSession("s2")
	if len(m.List()) != 0 {
		t.Fatal("s2 should start empty")
	}
	// A capture with no active turn (cur dropped by SetSession) is a no-op.
	f := filepath.Join(wd, "x.txt")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	m.CaptureBefore(f)
	if len(m.List()) != 0 {
		t.Fatal("capture without Begin must not create a checkpoint")
	}
}

func TestNewManagerNilWhenUnscoped(t *testing.T) {
	if NewManager("", "s1", Retention{}, nil) != nil {
		t.Fatal("empty workdir should yield nil manager")
	}
	if NewManager(t.TempDir(), "", Retention{}, nil) != nil {
		t.Fatal("empty session should yield nil manager")
	}
	// All methods are nil-safe.
	var m *Manager
	m.Begin(0, 0, "x")
	m.CaptureBefore("/tmp/x")
	m.SetSession("s")
	if m.List() != nil {
		t.Fatal("nil manager List should be nil")
	}
	if res := m.RestoreCode(&Record{}); res.Restored != 0 {
		t.Fatal("nil manager RestoreCode should be empty")
	}
}

// The cross-session cap keeps the most-recent namespaces plus the live one.
func TestPruneSessionDirs(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, age time.Duration) {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		_ = os.Chtimes(d, ts, ts)
	}
	mk("cur", 10*time.Hour) // live session — old mtime, but must survive
	mk("a", 1*time.Hour)
	mk("b", 2*time.Hour)
	mk("c", 3*time.Hour)

	pruneSessionDirs(root, "cur", 2) // keep cur + 1 newest other ("a")

	for _, name := range []string{"cur", "a"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s should survive: %v", name, err)
		}
	}
	for _, name := range []string{"b", "c"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s should be pruned", name)
		}
	}
}

func mustReadByHash(t *testing.T, m *Manager, r *Record) []byte {
	t.Helper()
	data, err := readBlob(m.dir(), r.Files[0].Hash)
	if err != nil {
		t.Fatalf("readBlob: %v", err)
	}
	return data
}
