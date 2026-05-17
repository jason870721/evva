package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 1 analysis — EditTool.Execute code paths:
//   - decode input
//   - resolvePath errors
//   - stat → not-found / directory rejection
//   - read-before-edit guard (tracker)
//   - old_string == new_string rejected
//   - old_string not found
//   - old_string ambiguous (>1 match without replace_all)
//   - single replacement happy path
//   - replace_all happy path
//   - Metadata carries *FileDiff

func TestEdit_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.txt")
	tr := NewReadTracker()
	tr.MarkRead(missing) // even with prior read, missing file rejected
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+missing+`","old_string":"a","new_string":"b"}`))

	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("expected 'not found' error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestEdit_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	tr := NewReadTracker()
	tr.MarkRead(dir)
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+dir+`","old_string":"a","new_string":"b"}`))

	if !res.IsError || !strings.Contains(res.Content, "not a regular file") {
		t.Errorf("expected dir rejection; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestEdit_BlockedWithoutPriorRead(t *testing.T) {
	path := writeTempFile(t, "hello world")
	tool := NewEdit(NewReadTracker())

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"hello","new_string":"bye"}`))

	if !res.IsError || !strings.Contains(res.Content, "read_file") {
		t.Errorf("expected read-guard error; got isErr=%v content=%q", res.IsError, res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello world" {
		t.Errorf("file mutated despite guard: %q", string(got))
	}
}

func TestEdit_RejectsIdenticalStrings(t *testing.T) {
	path := writeTempFile(t, "x")
	tr := NewReadTracker()
	tr.MarkRead(path)
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"x","new_string":"x"}`))

	if !res.IsError || !strings.Contains(res.Content, "identical") {
		t.Errorf("expected 'identical' rejection; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestEdit_OldStringNotFound(t *testing.T) {
	path := writeTempFile(t, "hello world")
	tr := NewReadTracker()
	tr.MarkRead(path)
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"nope","new_string":"yes"}`))

	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("expected 'not found'; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestEdit_AmbiguousWithoutReplaceAll(t *testing.T) {
	path := writeTempFile(t, "foo\nfoo\nfoo\n")
	tr := NewReadTracker()
	tr.MarkRead(path)
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"foo","new_string":"bar"}`))

	if !res.IsError {
		t.Fatal("expected ambiguity rejection")
	}
	if !strings.Contains(res.Content, "matches 3 locations") {
		t.Errorf("expected '3 locations' in error; got %q", res.Content)
	}
	// File must remain untouched.
	got, _ := os.ReadFile(path)
	if string(got) != "foo\nfoo\nfoo\n" {
		t.Errorf("file mutated on ambiguity: %q", string(got))
	}
}

func TestEdit_SingleReplacement_HappyPath(t *testing.T) {
	path := writeTempFile(t, "alpha beta gamma")
	tr := NewReadTracker()
	tr.MarkRead(path)
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"beta","new_string":"BETA"}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "alpha BETA gamma" {
		t.Errorf("file content: got %q", string(got))
	}
	if !strings.Contains(res.Content, "1 replacement") {
		t.Errorf("expected 1-replacement summary; got %q", res.Content)
	}
	if _, ok := res.Metadata.(*FileDiff); !ok {
		t.Error("expected *FileDiff in Metadata")
	}
}

func TestEdit_ReplaceAll(t *testing.T) {
	path := writeTempFile(t, "foo bar foo baz foo")
	tr := NewReadTracker()
	tr.MarkRead(path)
	tool := NewEdit(tr)

	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"foo","new_string":"FOO","replace_all":true}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "FOO bar FOO baz FOO" {
		t.Errorf("file content: got %q", string(got))
	}
	if !strings.Contains(res.Content, "3 replacement") {
		t.Errorf("expected 3-replacement summary; got %q", res.Content)
	}
}

func TestEdit_DecodeError(t *testing.T) {
	tool := NewEdit(NewReadTracker())
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{nope`))
	if !res.IsError || !strings.Contains(res.Content, "decode") {
		t.Errorf("expected decode error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}
