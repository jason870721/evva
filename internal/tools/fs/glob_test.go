package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/tools"
	"time"
)

func writeFixture(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func TestGlob_DoublestarMatchesRecursively(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.go", "")
	writeFixture(t, dir, "pkg/b.go", "")
	writeFixture(t, dir, "pkg/sub/c.go", "")
	writeFixture(t, dir, "ignore.txt", "")

	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"**/*.go","path":"`+dir+`"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	// Output is one path per line, no header.
	for _, want := range []string{"a.go", "b.go", "c.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in:\n%s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "ignore.txt") {
		t.Errorf("non-matching file should not appear: %s", res.Content)
	}
}

// TestGlob_MtimeSortedAscending — ref uses ripgrep `--sort=modified`
// which is ascending (oldest first). With more than 100 matches, the
// 100 OLDEST are returned. Truncated callers should narrow the
// pattern.
func TestGlob_MtimeSortedAscending(t *testing.T) {
	dir := t.TempDir()
	older := writeFixture(t, dir, "old.go", "")
	newer := writeFixture(t, dir, "new.go", "")

	now := time.Now()
	os.Chtimes(older, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	os.Chtimes(newer, now, now)

	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"*.go","path":"`+dir+`"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	olderIdx := strings.Index(res.Content, "old.go")
	newerIdx := strings.Index(res.Content, "new.go")
	if olderIdx < 0 || newerIdx < 0 {
		t.Fatalf("both files should appear; got: %s", res.Content)
	}
	if olderIdx > newerIdx {
		t.Errorf("older file should appear before newer (ascending sort, ref behavior); got:\n%s", res.Content)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.go", "")

	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"*.rs","path":"`+dir+`"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "No files found" {
		t.Errorf("expected ref empty message; got: %q", res.Content)
	}
}

func TestGlob_MissingPattern(t *testing.T) {
	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("expected error for missing pattern")
	}
}

func TestGlob_DirectoryDoesNotExist(t *testing.T) {
	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"*.go","path":"/no/such/dir"}`))
	if !res.IsError {
		t.Fatal("expected error for missing search root")
	}
	if !strings.Contains(res.Content, "directory does not exist") {
		t.Errorf("expected ref-style 'directory does not exist'; got: %s", res.Content)
	}
}

func TestGlob_PathNotDir(t *testing.T) {
	path := writeTempFile(t, "x")
	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"*.go","path":"`+path+`"}`))
	if !res.IsError {
		t.Fatal("expected error when search root is a file")
	}
	if !strings.Contains(res.Content, "not a directory") {
		t.Errorf("expected ref-style 'not a directory'; got: %s", res.Content)
	}
}

// TestGlob_AbsolutePatternHandled — the critical bug from the audit:
// an absolute pattern like "/abs/dir/**/*.go" used to return 0
// matches because doublestar matches against paths relative to the
// DirFS root. extractGlobBaseDirectory splits the absolute pattern
// into a search root + relative pattern.
func TestGlob_AbsolutePatternHandled(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "src/a.go", "")
	writeFixture(t, dir, "src/sub/b.go", "")

	tool := NewGlob("")
	// Pattern is absolute; path omitted entirely.
	pattern := filepath.Join(dir, "src", "**", "*.go")
	body, _ := json.Marshal(map[string]string{"pattern": pattern})
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), body)
	if res.IsError {
		t.Fatalf("absolute pattern should work: %s", res.Content)
	}
	for _, want := range []string{"a.go", "b.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("absolute pattern missed %q in:\n%s", want, res.Content)
		}
	}
}

// TestGlob_OutputFormatPlainFilenames — output is one path per line,
// no header / metadata.
func TestGlob_OutputFormatPlainFilenames(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.go", "")
	writeFixture(t, dir, "b.go", "")

	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"*.go","path":"`+dir+`"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.HasPrefix(res.Content, "[") {
		t.Errorf("output should not have a verbose header; got: %q", res.Content)
	}
	lines := strings.Split(res.Content, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (one per file); got %d lines: %q", len(lines), res.Content)
	}
}

// TestGlob_TruncationFooter — when more than globResultLimit matches,
// the truncation note is appended verbatim. Use a tiny limit override
// via many fixture files isn't ergonomic — exercise the constant
// directly with a stub.
func TestGlob_TruncationFooter(t *testing.T) {
	dir := t.TempDir()
	// Create globResultLimit + 5 fixture files to trigger truncation.
	for i := 0; i < globResultLimit+5; i++ {
		writeFixture(t, dir, "f"+strings.Repeat("x", i+1)+".go", "")
	}
	tool := NewGlob("")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"pattern":"*.go","path":"`+dir+`"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Results are truncated") {
		t.Errorf("expected truncation footer; got tail: %s", lastLines(res.Content, 3))
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Unit: extractGlobBaseDirectory
// =============================================================================

func TestExtractGlobBaseDirectory(t *testing.T) {
	cases := []struct {
		in       string
		wantBase string
		wantRel  string
	}{
		{"/abs/path/**/*.go", "/abs/path", "**/*.go"},
		{"/abs/file.go", "/abs", "file.go"},
		{"/*.go", "/", "*.go"},
		{"/abs/dir/*", "/abs/dir", "*"},
		{"**/*.go", "", "**/*.go"}, // no separator before glob → search-from-root
		{"src/**/*.ts", "src", "**/*.ts"},
		{"plain.txt", ".", "plain.txt"}, // literal path: dirname + basename
	}
	for _, c := range cases {
		gotBase, gotRel := extractGlobBaseDirectory(c.in)
		if gotBase != c.wantBase || gotRel != c.wantRel {
			t.Errorf("extractGlobBaseDirectory(%q) = (%q, %q), want (%q, %q)",
				c.in, gotBase, gotRel, c.wantBase, c.wantRel)
		}
	}
}

func TestToRelativeOrAbs(t *testing.T) {
	cases := []struct {
		abs, workDir, want string
	}{
		{"/home/u/proj/src/a.go", "/home/u/proj", "src/a.go"},
		{"/etc/passwd", "/home/u/proj", "/etc/passwd"}, // outside workdir → absolute
		{"/home/u/proj/a.go", "", "/home/u/proj/a.go"}, // empty workdir → absolute
	}
	for _, c := range cases {
		if got := toRelativeOrAbs(c.abs, c.workDir); got != c.want {
			t.Errorf("toRelativeOrAbs(%q, %q) = %q, want %q", c.abs, c.workDir, got, c.want)
		}
	}
}
