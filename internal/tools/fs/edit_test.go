package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/tools"
	"time"
)

func TestEdit_FileNotFound_NonEmptyOldString(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.txt")
	tool := NewEdit(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+missing+`","old_string":"a","new_string":"b"}`))

	if !res.IsError || !strings.Contains(res.Content, "does not exist") {
		t.Errorf("expected 'does not exist' error; got isErr=%v content=%q", res.IsError, res.Content)
	}
	if !strings.Contains(res.Content, "old_string=\"\"") {
		t.Errorf("error should mention empty old_string for file creation; got %q", res.Content)
	}
}

func TestEdit_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	tool := NewEdit(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+dir+`","old_string":"a","new_string":"b"}`))

	if !res.IsError || !strings.Contains(res.Content, "not a regular file") {
		t.Errorf("expected dir rejection; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestEdit_BlockedWithoutPriorRead(t *testing.T) {
	path := writeTempFile(t, "hello world")
	tool := NewEdit(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"hello","new_string":"bye"}`))

	if !res.IsError || !strings.Contains(res.Content, "has not been read") {
		t.Errorf("expected 'has not been read' guard error; got isErr=%v content=%q", res.IsError, res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello world" {
		t.Errorf("file mutated despite guard: %q", string(got))
	}
}

// TestEdit_BlockedOnMtimeDrift — the file's mtime advanced on disk
// after the read, so the model's old_string may not match. Force a
// re-read.
func TestEdit_BlockedOnMtimeDrift(t *testing.T) {
	path := writeTempFile(t, "hello world")
	tr := NewReadTracker()
	// Record a read from one second in the past so any os.Chtimes
	// "now" will count as drift.
	earlier := time.Now().Add(-time.Second)
	tr.Record(path, earlier, false)
	// Move the file's mtime forward.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	tool := NewEdit(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"hello","new_string":"bye"}`))
	if !res.IsError {
		t.Fatal("expected mtime-drift error")
	}
	if !strings.Contains(res.Content, "modified since") {
		t.Errorf("error should mention 'modified since'; got %q", res.Content)
	}
}

// TestEdit_BlockedOnPartialView — a partial read (offset/limit) is
// not enough context to edit safely.
func TestEdit_BlockedOnPartialView(t *testing.T) {
	path := writeTempFile(t, "hello world")
	tr := NewReadTracker()
	info, _ := os.Stat(path)
	tr.Record(path, info.ModTime(), true) // partial

	tool := NewEdit(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"hello","new_string":"bye"}`))
	if !res.IsError {
		t.Fatal("expected partial-view rejection")
	}
	if !strings.Contains(res.Content, "partially read") {
		t.Errorf("error should mention 'partially read'; got %q", res.Content)
	}
}

func TestEdit_RejectsIPYNB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n.ipynb")
	os.WriteFile(path, []byte(`{"cells":[]}`), 0o644)
	tr := NewReadTracker()
	recordFullRead(t, tr, path)

	tool := NewEdit(tr, "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"a","new_string":"b"}`))
	if !res.IsError {
		t.Fatal("expected .ipynb rejection")
	}
	if !strings.Contains(res.Content, "Jupyter notebooks") {
		t.Errorf("error should mention Jupyter notebooks; got %q", res.Content)
	}
}

func TestEdit_RejectsIdenticalStrings(t *testing.T) {
	path := writeTempFile(t, "x")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"x","new_string":"x"}`))

	if !res.IsError || !strings.Contains(res.Content, "exactly the same") {
		t.Errorf("expected 'exactly the same' rejection; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestEdit_OldStringNotFound(t *testing.T) {
	path := writeTempFile(t, "hello world\nsecond line\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"nope","new_string":"yes"}`))

	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("expected 'not found'; got isErr=%v content=%q", res.IsError, res.Content)
	}
	if !strings.Contains(res.Content, "File starts with:") {
		t.Errorf("expected hint with file preview; got %q", res.Content)
	}
}

func TestEdit_OldStringNotFound_LineNumberPrefixHint(t *testing.T) {
	path := writeTempFile(t, "hello world\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	oldWithPrefix := "     1\thello world"
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":`+strconv.Quote(oldWithPrefix)+`,"new_string":"bye"}`))

	if !res.IsError {
		t.Fatalf("expected error; got content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "line-number prefix") {
		t.Errorf("expected line-number-prefix hint; got %q", res.Content)
	}
}

func TestEdit_AmbiguousWithoutReplaceAll(t *testing.T) {
	path := writeTempFile(t, "foo\nfoo\nfoo\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"foo","new_string":"bar"}`))

	if !res.IsError {
		t.Fatal("expected ambiguity rejection")
	}
	if !strings.Contains(res.Content, "matches 3 locations") {
		t.Errorf("expected '3 locations' in error; got %q", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "foo\nfoo\nfoo\n" {
		t.Errorf("file mutated on ambiguity: %q", string(got))
	}
}

func TestEdit_SingleReplacement_HappyPath(t *testing.T) {
	path := writeTempFile(t, "alpha beta gamma")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
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
	// After edit, tracker should re-record with the new mtime so a
	// follow-up edit doesn't fail the drift guard.
	if entry, ok := tr.Lookup(path); !ok || entry.IsPartialView {
		t.Errorf("post-edit tracker entry malformed: ok=%v entry=%+v", ok, entry)
	}
}

func TestEdit_ReplaceAll(t *testing.T) {
	path := writeTempFile(t, "foo bar foo baz foo")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
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
	tool := NewEdit(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{nope`))
	if !res.IsError || !strings.Contains(res.Content, "decode") {
		t.Errorf("expected decode error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

// =============================================================================
// 1:1 ref port: new behaviors covering quote normalization, CRLF, file
// creation via empty old_string, trailing-newline cleanup, and encoding
// roundtrip.
// =============================================================================

// TestEdit_CreatesNewFileWithEmptyOldString — ref's documented file-
// creation idiom: empty old_string targeting a path that doesn't yet
// exist writes new_string as the initial content.
func TestEdit_CreatesNewFileWithEmptyOldString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "new.go")
	tool := NewEdit(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"","new_string":"package main\n"}`))

	if res.IsError {
		t.Fatalf("file creation should succeed; got: %s", res.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(got) != "package main\n" {
		t.Errorf("file content: got %q, want %q", string(got), "package main\n")
	}
	if !strings.Contains(res.Content, "created") {
		t.Errorf("expected 'created' summary; got %q", res.Content)
	}
}

// TestEdit_EmptyOldStringOnExistingFileRejected — empty old_string
// against a non-empty existing file is ambiguous (model wants to
// create AND file exists). Reject with a write-instead hint.
func TestEdit_EmptyOldStringOnExistingFileRejected(t *testing.T) {
	path := writeTempFile(t, "existing content\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"","new_string":"new content"}`))
	if !res.IsError {
		t.Fatal("expected rejection for empty old_string on non-empty file")
	}
	if !strings.Contains(res.Content, "already exists") {
		t.Errorf("error should mention file exists; got %q", res.Content)
	}
}

// TestEdit_EmptyOldStringOnEmptyFile — populating a previously-empty
// file via Edit is valid; treats new_string as the initial content.
func TestEdit_EmptyOldStringOnEmptyFile(t *testing.T) {
	path := writeTempFile(t, "")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"","new_string":"populated"}`))
	if res.IsError {
		t.Fatalf("populating empty file should succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "populated" {
		t.Errorf("file content: got %q, want %q", string(got), "populated")
	}
}

// TestEdit_CRLFRoundtrip — file written with CRLF line endings is
// edited via a model that sends LF-only old_string; the file's CRLF
// shape is preserved on write.
func TestEdit_CRLFRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(path, []byte("first\r\nsecond\r\nthird\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"second","new_string":"SECOND"}`))
	if res.IsError {
		t.Fatalf("CRLF edit should succeed (LF old_string normalized to LF in mem); got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	want := "first\r\nSECOND\r\nthird\r\n"
	if string(got) != want {
		t.Errorf("CRLF not preserved on write: got %q want %q", string(got), want)
	}
}

// TestEdit_UTF16LERoundtrip — Windows Notepad writes UTF-16 LE with
// BOM. The edit logic must decode, normalize, edit, and re-encode
// back as UTF-16 LE.
func TestEdit_UTF16LERoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win-utf16.txt")
	// Construct UTF-16 LE: BOM 0xff 0xfe, then "hello" each char as
	// two LE bytes.
	raw := []byte{0xff, 0xfe}
	for _, r := range "hello" {
		raw = append(raw, byte(r), byte(r>>8))
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"hello","new_string":"world"}`))
	if res.IsError {
		t.Fatalf("UTF-16 edit should succeed; got: %s", res.Content)
	}
	out, _ := os.ReadFile(path)
	if len(out) < 2 || out[0] != 0xff || out[1] != 0xfe {
		t.Errorf("UTF-16 BOM not preserved on write; got first bytes: %v", out[:min(2, len(out))])
	}
	// "world" UTF-16 LE = w(00) o(00) r(00) l(00) d(00).
	wantBody := []byte("w\x00o\x00r\x00l\x00d\x00")
	if !strings.Contains(string(out), string(wantBody)) {
		t.Errorf("expected 'world' encoded as UTF-16 LE in output; got: %v", out)
	}
}

// TestEdit_CurlyQuoteToStraight — file uses curly quotes, model sends
// old_string with straight quotes. findActualString normalizes both
// directions and the edit succeeds.
func TestEdit_CurlyQuoteToStraight(t *testing.T) {
	path := writeTempFile(t, "say “hello” loudly\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	// Model sends straight quotes — should match the curly version
	// in the file via normalizeQuotes.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"say \"hello\" loudly","new_string":"say \"hi\" quietly"}`))
	if res.IsError {
		t.Fatalf("curly-quote match should succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	// preserveQuoteStyle should put curly quotes back on the new_string.
	want := "say “hi” quietly\n"
	if string(got) != want {
		t.Errorf("preserveQuoteStyle failed: got %q want %q", string(got), want)
	}
}

// TestEdit_StraightQuoteToCurly — reverse direction: file has
// straight quotes, model accidentally sends curly. Match should
// still succeed via normalization. preserveQuoteStyle is a no-op
// here (the file's actual quote is straight, not curly).
func TestEdit_StraightQuoteToCurly(t *testing.T) {
	path := writeTempFile(t, `say "hello" loudly`+"\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	// old_string with curly, new_string with straight.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"say “hello” loudly","new_string":"say \"hi\" quietly"}`))
	if res.IsError {
		t.Fatalf("curly→straight match should succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"hi"`) {
		t.Errorf("expected straight quotes in result; got %q", string(got))
	}
}

// TestEdit_TrailingNewlineCleanupOnEmptyNewString — deleting a line
// via new_string="" should also consume the trailing newline so the
// file doesn't grow a blank line.
func TestEdit_TrailingNewlineCleanupOnEmptyNewString(t *testing.T) {
	path := writeTempFile(t, "line1\nline2\nline3\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"line2","new_string":""}`))
	if res.IsError {
		t.Fatalf("delete-line edit should succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	want := "line1\nline3\n"
	if string(got) != want {
		t.Errorf("trailing newline not cleaned up: got %q want %q", string(got), want)
	}
}

// TestEdit_StripTrailingWhitespaceOnNonMarkdown — ref strips trailing
// spaces/tabs from each line of new_string for non-markdown files.
func TestEdit_StripTrailingWhitespaceOnNonMarkdown(t *testing.T) {
	path := writeTempFile(t, "foo\n")
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	// new_string has trailing spaces on each line — should be stripped.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"foo","new_string":"bar   \n  baz  "}`))
	if res.IsError {
		t.Fatalf("edit should succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	want := "bar\n  baz\n"
	if string(got) != want {
		t.Errorf("trailing whitespace not stripped: got %q want %q", string(got), want)
	}
}

// TestEdit_MarkdownPreservesTrailingSpaces — .md files keep two
// trailing spaces (hard line break in markdown).
func TestEdit_MarkdownPreservesTrailingSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("foo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tr := NewReadTracker()
	recordFullRead(t, tr, path)
	tool := NewEdit(tr, "")

	// Two trailing spaces on the first line of new_string — should NOT
	// be stripped because this is markdown.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":"`+path+`","old_string":"foo","new_string":"line1  \nline2"}`))
	if res.IsError {
		t.Fatalf("edit should succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	want := "line1  \nline2\n"
	if string(got) != want {
		t.Errorf("markdown trailing spaces stripped (should be preserved): got %q want %q", string(got), want)
	}
}

func TestNormalizeQuotes(t *testing.T) {
	if got := normalizeQuotes("hello"); got != "hello" {
		t.Errorf("no-curly fast path failed: %q", got)
	}
	if got := normalizeQuotes("“hi” it's"); got != `"hi" it's` {
		t.Errorf("normalize failed: got %q", got)
	}
}

func TestFindActualString_ExactMatch(t *testing.T) {
	got, ok := findActualString("hello world", "world")
	if !ok || got != "world" {
		t.Errorf("got (%q,%v), want (\"world\", true)", got, ok)
	}
}

func TestFindActualString_QuoteNormalized(t *testing.T) {
	file := "say “hello” loudly"
	got, ok := findActualString(file, `say "hello" loudly`)
	if !ok {
		t.Fatal("normalized match should succeed")
	}
	if got != "say “hello” loudly" {
		t.Errorf("expected actual to include curly quotes: %q", got)
	}
}

func TestApplyEditToFile_TrailingNewlineCleanup(t *testing.T) {
	got := applyEditToFile("a\nb\nc\n", "b", "", false)
	if got != "a\nc\n" {
		t.Errorf("expected 'a\\nc\\n', got %q", got)
	}
}

func TestApplyEditToFile_NoCleanupWhenOldEndsInNewline(t *testing.T) {
	// When old_string already ends in \n, don't double-strip.
	got := applyEditToFile("a\nb\nc\n", "b\n", "", false)
	if got != "a\nc\n" {
		t.Errorf("expected 'a\\nc\\n', got %q", got)
	}
}

func TestStripTrailingWhitespacePerLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo   ", "foo"},
		{"foo  \nbar  \n", "foo\nbar\n"},
		{"foo  \r\nbar  \r\n", "foo\r\nbar\r\n"},
		{"\t\t\n", "\n"},
		{"", ""},
	}
	for _, c := range cases {
		got := stripTrailingWhitespacePerLine(c.in)
		if got != c.want {
			t.Errorf("stripTrailingWhitespacePerLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
