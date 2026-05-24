package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
	"time"
)

func TestWrite_NewFileSkipsReadGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	tool := NewWrite(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"hello"}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content: got %q, want %q", string(got), "hello")
	}
	if !strings.Contains(res.Content, "created") {
		t.Errorf("expected 'created' summary, got %q", res.Content)
	}
}

func TestWrite_OverwriteBlockedWithoutPriorRead(t *testing.T) {
	path := writeTempFile(t, "old")
	tool := NewWrite(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"new"}`))

	if !res.IsError {
		t.Fatal("expected guard to block overwrite without prior read")
	}
	if !strings.Contains(res.Content, "has not been read") {
		t.Errorf("error should mention 'has not been read'; got %q", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Errorf("file should NOT have been modified; got %q", string(got))
	}
}

// TestWrite_OverwriteBlockedOnMtimeDrift — the file's mtime advanced
// since the read, so the model's mental model of the content is stale.
// Force a re-read before overwriting.
func TestWrite_OverwriteBlockedOnMtimeDrift(t *testing.T) {
	path := writeTempFile(t, "old")
	tr := NewReadTracker()
	earlier := time.Now().Add(-time.Second)
	tr.Record(path, earlier, false, HashContent("old"))
	// External rewrite (different bytes) + mtime bump = real drift the
	// content-hash fallback must not mask.
	if err := os.WriteFile(path, []byte("changed underfoot"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	tool := NewWrite(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"new"}`))
	if !res.IsError {
		t.Fatal("expected mtime-drift rejection")
	}
	if !strings.Contains(res.Content, "modified since") {
		t.Errorf("error should mention 'modified since'; got %q", res.Content)
	}
}

// TestWrite_OverwriteAllowedAfterPartialView — Part 0: a prior partial
// read no longer blocks an overwrite. Write is a full replacement; the
// model supplies the complete new content, so seeing only a slice of the
// old file is irrelevant.
func TestWrite_OverwriteAllowedAfterPartialView(t *testing.T) {
	path := writeTempFile(t, "old")
	tr := NewReadTracker()
	info, _ := os.Stat(path)
	tr.Record(path, info.ModTime(), true, HashContent("old"))

	tool := NewWrite(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"new"}`))
	if res.IsError {
		t.Fatalf("overwrite after partial-view read should succeed; got %q", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("overwrite not applied; file = %q", string(got))
	}
}

func TestWrite_OverwriteAllowedAfterRead(t *testing.T) {
	path := writeTempFile(t, "old")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewWrite(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"new"}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("file content: got %q, want %q", string(got), "new")
	}
	if !strings.Contains(res.Content, "overwrote") {
		t.Errorf("expected 'overwrote' summary; got %q", res.Content)
	}
	if _, ok := res.Metadata.(*FileDiff); !ok {
		t.Error("expected Metadata to carry *FileDiff for overwrite")
	}
}

func TestWrite_AutoMkdirsMissingParents(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "f.txt")
	tool := NewWrite(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+deep+`","content":"x"}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if _, err := os.Stat(deep); err != nil {
		t.Errorf("deep path not created: %v", err)
	}
}

func TestWrite_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	tool := NewWrite(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":""}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("empty content size: got %d, want 0", info.Size())
	}
}

func TestWrite_DecodeError(t *testing.T) {
	tool := NewWrite(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{not json`))
	if !res.IsError || !strings.Contains(res.Content, "missing required") {
		t.Errorf("expected error about missing required params; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestWrite_EmptyInput(t *testing.T) {
	tool := NewWrite(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(``))
	if !res.IsError {
		t.Fatalf("expected error for empty input")
	}
	if !strings.Contains(res.Content, "file_path") && !strings.Contains(res.Content, "content") {
		t.Fatalf("error should mention missing required params; got: %s", res.Content)
	}
}

func TestWrite_MissingFilePath(t *testing.T) {
	tool := NewWrite(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"content":"hello"}`))
	if !res.IsError {
		t.Fatalf("expected error for missing file_path")
	}
	if !strings.Contains(res.Content, "file_path") {
		t.Fatalf("error should mention file_path; got: %s", res.Content)
	}
}

func TestWrite_MissingContent(t *testing.T) {
	dir := t.TempDir()
	tool := NewWrite(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+filepath.Join(dir, "f.txt")+`"}`))
	if !res.IsError {
		t.Fatalf("expected error for missing content")
	}
	if !strings.Contains(res.Content, "content") {
		t.Fatalf("error should mention content; got: %s", res.Content)
	}
}

func TestWrite_EmptyFilePath(t *testing.T) {
	tool := NewWrite(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"","content":"hello"}`))
	if !res.IsError {
		t.Fatalf("expected error for empty file_path")
	}
	if !strings.Contains(res.Content, "file_path") {
		t.Fatalf("error should mention file_path; got: %s", res.Content)
	}
}

// =============================================================================
// 1:1 ref port: encoding preservation on overwrite.
// =============================================================================

// TestWrite_PreservesUTF16LEOnOverwrite — overwriting a UTF-16 LE file
// (Notepad/Windows default with BOM) re-encodes the new content as
// UTF-16 LE so the file's downstream consumer still recognizes it.
func TestWrite_PreservesUTF16LEOnOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	// Seed file as UTF-16 LE "old".
	original := []byte{0xff, 0xfe, 'o', 0, 'l', 0, 'd', 0}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tr := NewReadTracker()
	recordFullRead(t, tr, path)

	tool := NewWrite(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"new"}`))
	if res.IsError {
		t.Fatalf("UTF-16 overwrite should succeed: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if len(got) < 2 || got[0] != 0xff || got[1] != 0xfe {
		t.Errorf("UTF-16 BOM not preserved on write; first bytes: %v", got[:min(2, len(got))])
	}
	// "new" UTF-16 LE = n(00) e(00) w(00).
	wantBody := []byte("n\x00e\x00w\x00")
	if !strings.Contains(string(got), string(wantBody)) {
		t.Errorf("expected 'new' as UTF-16 LE; got: %v", got)
	}
}

// TestWrite_DoesNotRestoreCRLF — ref's Write deliberately writes
// content verbatim (no CRLF re-introduction) even if the prior file
// used CRLF. Distinguishes Write (full replacement) from Edit
// (in-place mutation that preserves shape).
func TestWrite_DoesNotRestoreCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tr := NewReadTracker()
	recordFullRead(t, tr, path)

	tool := NewWrite(tr, "")
	// Model sends LF content — Write must respect that.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"x\ny\n"}`))
	if res.IsError {
		t.Fatalf("overwrite should succeed: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "\r\n") {
		t.Errorf("Write should not restore CRLF; got: %q", string(got))
	}
	if string(got) != "x\ny\n" {
		t.Errorf("content should be verbatim LF: got %q", string(got))
	}
}

// TestWrite_DiffUsesNormalizedPriorContent — overwriting a CRLF file
// produces a diff against the normalized (LF) prior content, so the
// diff doesn't show every line as changed just because of \r.
func TestWrite_DiffUsesNormalizedPriorContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(path, []byte("line1\r\nline2\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tr := NewReadTracker()
	recordFullRead(t, tr, path)

	tool := NewWrite(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","content":"line1\nLINE2\n"}`))
	if res.IsError {
		t.Fatalf("overwrite should succeed: %s", res.Content)
	}
	diff, ok := res.Metadata.(*FileDiff)
	if !ok {
		t.Fatalf("metadata should be *FileDiff")
	}
	// The diff should reflect a single replaced line, not a full
	// every-line replacement caused by \r being treated as content
	// difference.
	totalChanges := 0
	for _, h := range diff.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineAdd || l.Kind == LineRemove {
				totalChanges++
			}
		}
	}
	if totalChanges > 2 { // one remove + one add = 2 expected
		t.Errorf("diff should show only line2 changing; got %d add/remove lines (likely \\r treated as content)", totalChanges)
	}
}
