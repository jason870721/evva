package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 1 analysis — WriteTool.Execute code paths:
//   - decode input
//   - resolvePath errors
//   - new-file path (no read-guard required)
//   - overwrite path requires prior read via tracker
//   - auto-mkdir for missing parents
//   - empty content writes empty file

func TestWrite_NewFileSkipsReadGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	tool := NewWrite(NewReadTracker())

	res, _ := tool.Execute(context.Background(),
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
	tool := NewWrite(NewReadTracker())

	res, _ := tool.Execute(context.Background(),
		json.RawMessage(`{"file_path":"`+path+`","content":"new"}`))

	if !res.IsError {
		t.Fatal("expected guard to block overwrite without prior read")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Errorf("file should NOT have been modified; got %q", string(got))
	}
}

func TestWrite_OverwriteAllowedAfterRead(t *testing.T) {
	path := writeTempFile(t, "old")
	tr := NewReadTracker()
	tr.MarkRead(path)
	tool := NewWrite(tr)

	res, _ := tool.Execute(context.Background(),
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
	tool := NewWrite(NewReadTracker())

	res, _ := tool.Execute(context.Background(),
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
	tool := NewWrite(NewReadTracker())

	res, _ := tool.Execute(context.Background(),
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
	tool := NewWrite(NewReadTracker())
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{not json`))
	if !res.IsError || !strings.Contains(res.Content, "decode") {
		t.Errorf("expected decode error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}
