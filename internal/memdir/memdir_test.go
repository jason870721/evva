package memdir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- Load -------------------------------------------------------------------

func TestLoad_EVVAMissing_AutoMemoryOff(t *testing.T) {
	workdir, appHome := t.TempDir(), t.TempDir()
	snap := Load(workdir, appHome, false)
	if snap.WorkdirMemory != "" || snap.MemoryIndex != "" || snap.MemoryDir != "" {
		t.Errorf("all-empty snapshot expected; got %+v", snap)
	}
	if len(snap.Warnings) != 0 {
		t.Errorf("no warnings expected; got %v", snap.Warnings)
	}
	// Auto-memory off must NOT create the dir (A9).
	if _, err := os.Stat(MemoryDir(appHome)); !os.IsNotExist(err) {
		t.Errorf("memory dir must not be created when auto-memory is off")
	}
}

func TestLoad_EVVAReadRegardlessOfAutoMemory(t *testing.T) {
	workdir, appHome := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workdir, ProjectMemoryFile), "use gofmt")
	snap := Load(workdir, appHome, false)
	if snap.WorkdirMemory != "use gofmt" {
		t.Errorf("EVVA.md should load even with auto-memory off; got %q", snap.WorkdirMemory)
	}
}

func TestLoad_AutoMemoryOn_CreatesDirAndReadsIndex(t *testing.T) {
	workdir, appHome := t.TempDir(), t.TempDir()
	writeFile(t, MemoryIndexPath(appHome), "- [role](user/role.md) — senior Go dev\n")

	snap := Load(workdir, appHome, true)
	if snap.MemoryDir != MemoryDir(appHome) {
		t.Errorf("MemoryDir: got %q, want %q", snap.MemoryDir, MemoryDir(appHome))
	}
	if !strings.Contains(snap.MemoryIndex, "senior Go dev") {
		t.Errorf("MemoryIndex should carry the index body; got %q", snap.MemoryIndex)
	}
	if _, err := os.Stat(MemoryDir(appHome)); err != nil {
		t.Errorf("memory dir should exist after Load: %v", err)
	}
}

func TestLoad_AutoMemoryOn_NoIndexYet(t *testing.T) {
	workdir, appHome := t.TempDir(), t.TempDir()
	snap := Load(workdir, appHome, true)
	// Dir is ensured even with no index file yet (so the prompt's
	// "already exists" claim is true), but the index body is empty.
	if snap.MemoryDir == "" {
		t.Errorf("MemoryDir should be set once the dir is ensured")
	}
	if snap.MemoryIndex != "" {
		t.Errorf("MemoryIndex should be empty when MEMORY.md is absent; got %q", snap.MemoryIndex)
	}
}

func TestLoad_OversizeEVVATruncates(t *testing.T) {
	workdir, appHome := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workdir, ProjectMemoryFile), strings.Repeat("x", MaxFileBytes+1024))
	snap := Load(workdir, appHome, false)
	if len(snap.WorkdirMemory) != MaxFileBytes {
		t.Errorf("length: got %d, want %d", len(snap.WorkdirMemory), MaxFileBytes)
	}
	if len(snap.Warnings) != 1 || !strings.Contains(snap.Warnings[0], "truncated") {
		t.Errorf("expected one truncation warning; got %v", snap.Warnings)
	}
}

func TestLoad_EmptyPaths(t *testing.T) {
	snap := Load("", "", true)
	if snap.WorkdirMemory != "" || snap.MemoryIndex != "" || snap.MemoryDir != "" {
		t.Errorf("empty paths → empty snapshot; got %+v", snap)
	}
}

// --- ParseFrontmatter (A2) --------------------------------------------------

func TestParseFrontmatter_RoundTrips(t *testing.T) {
	content := "---\nname: role\ndescription: senior Go: ten years\ntype: user\n---\n\nbody line 1\nbody line 2"
	fm, body := ParseFrontmatter(content)
	if fm["name"] != "role" {
		t.Errorf("name: got %q", fm["name"])
	}
	if fm["description"] != "senior Go: ten years" { // value keeps its inner colon
		t.Errorf("description: got %q", fm["description"])
	}
	if fm["type"] != "user" {
		t.Errorf("type: got %q", fm["type"])
	}
	if body != "body line 1\nbody line 2" {
		t.Errorf("body: got %q", body)
	}
}

func TestParseFrontmatter_NoFrontmatterIsAllBody(t *testing.T) {
	content := "# just markdown\nno frontmatter here"
	fm, body := ParseFrontmatter(content)
	if len(fm) != 0 {
		t.Errorf("expected empty map; got %v", fm)
	}
	if body != content {
		t.Errorf("body should equal full content; got %q", body)
	}
}

func TestParseFrontmatter_UnterminatedIsAllBody(t *testing.T) {
	content := "---\nname: x\nno closing fence"
	fm, body := ParseFrontmatter(content)
	if len(fm) != 0 || body != content {
		t.Errorf("unterminated frontmatter should yield empty map + full body; got fm=%v body=%q", fm, body)
	}
}

func TestParseFrontmatter_NestedMetadataFlattens(t *testing.T) {
	content := "---\nname: x\ndescription: d\nmetadata:\n  type: feedback\n---\nbody"
	fm, _ := ParseFrontmatter(content)
	if fm["type"] != "feedback" {
		t.Errorf("nested metadata type should flatten; got %q", fm["type"])
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	content := "---\nname: \"quoted name\"\ndescription: 'single'\ntype: user\n---\nb"
	fm, _ := ParseFrontmatter(content)
	if fm["name"] != "quoted name" || fm["description"] != "single" {
		t.Errorf("quotes should be stripped; got name=%q desc=%q", fm["name"], fm["description"])
	}
}

// --- ParseMemoryType --------------------------------------------------------

func TestParseMemoryType(t *testing.T) {
	for _, ty := range []string{"user", "feedback", "project", "reference"} {
		if got, ok := ParseMemoryType(ty); !ok || string(got) != ty {
			t.Errorf("ParseMemoryType(%q) = (%q,%v)", ty, got, ok)
		}
	}
	for _, bad := range []string{"", "USER", "decision", "team"} {
		if got, ok := ParseMemoryType(bad); ok || got != "" {
			t.Errorf("ParseMemoryType(%q) should be untyped; got (%q,%v)", bad, got, ok)
		}
	}
}

// --- age (A7) ---------------------------------------------------------------

func TestAgeHelpers(t *testing.T) {
	now := time.Now()
	cases := []struct {
		mtime    time.Time
		wantDays int
		wantAge  string
		fresh    bool // FreshnessText == ""
	}{
		{now, 0, "today", true},
		{now.Add(-25 * time.Hour), 1, "yesterday", true},
		{now.Add(-3 * 24 * time.Hour), 3, "3 days ago", false},
		{now.Add(48 * time.Hour), 0, "today", true}, // future clamps to 0
	}
	for _, c := range cases {
		if got := AgeDays(c.mtime); got != c.wantDays {
			t.Errorf("AgeDays = %d, want %d", got, c.wantDays)
		}
		if got := Age(c.mtime); got != c.wantAge {
			t.Errorf("Age = %q, want %q", got, c.wantAge)
		}
		if fresh := FreshnessText(c.mtime) == ""; fresh != c.fresh {
			t.Errorf("FreshnessText fresh=%v, want %v (days=%d)", fresh, c.fresh, c.wantDays)
		}
	}
}

func TestFreshnessTextContent(t *testing.T) {
	old := time.Now().Add(-60 * 24 * time.Hour)
	txt := FreshnessText(old)
	if !strings.Contains(txt, "60 days old") || !strings.Contains(txt, "Verify against current code") {
		t.Errorf("freshness caveat wording off: %q", txt)
	}
	note := FreshnessNote(old)
	if !strings.HasPrefix(note, "<system-reminder>") || !strings.HasSuffix(note, "</system-reminder>\n") {
		t.Errorf("FreshnessNote should self-wrap; got %q", note)
	}
}

// --- ScanMemoryFiles (A3) ---------------------------------------------------

func TestScanMemoryFiles_OrderExcludeRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "old.md"), "---\ntype: user\ndescription: old\n---\nb")
	writeFile(t, filepath.Join(dir, "sub", "new.md"), "---\ntype: feedback\ndescription: new\n---\nb")
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- index line") // excluded
	writeFile(t, filepath.Join(dir, "notes.txt"), "ignored")      // non-md ignored

	old := time.Now().Add(-10 * 24 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "old.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "sub", "new.md"), recent, recent); err != nil {
		t.Fatal(err)
	}

	hs := ScanMemoryFiles(dir)
	if len(hs) != 2 {
		t.Fatalf("want 2 headers (MEMORY.md + .txt excluded); got %d: %+v", len(hs), hs)
	}
	if hs[0].Filename != "sub/new.md" {
		t.Errorf("newest-first ordering: want sub/new.md first; got %q", hs[0].Filename)
	}
	if hs[1].Filename != "old.md" {
		t.Errorf("second should be old.md; got %q", hs[1].Filename)
	}
	if hs[0].Type != TypeFeedback || hs[0].Description != "new" {
		t.Errorf("header fields off: %+v", hs[0])
	}
}

func TestScanMemoryFiles_MissingDirAndMalformed(t *testing.T) {
	if got := ScanMemoryFiles(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Errorf("missing dir → nil; got %+v", got)
	}
	dir := t.TempDir()
	// A file with no frontmatter is still scanned (untyped, no description),
	// never an error.
	writeFile(t, filepath.Join(dir, "bare.md"), "just a body, no frontmatter")
	hs := ScanMemoryFiles(dir)
	if len(hs) != 1 || hs[0].Type != "" || hs[0].Description != "" {
		t.Errorf("bare file should scan as untyped/empty-desc; got %+v", hs)
	}
}

func TestFormatManifest(t *testing.T) {
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	hs := []MemoryHeader{
		{Filename: "a.md", ModTime: ts, Type: TypeUser, Description: "desc a"},
		{Filename: "b.md", ModTime: ts}, // no type, no desc
	}
	got := FormatManifest(hs)
	wantA := "- [user] a.md (2026-05-01T12:00:00Z): desc a"
	wantB := "- b.md (2026-05-01T12:00:00Z)"
	if !strings.Contains(got, wantA) || !strings.Contains(got, wantB) {
		t.Errorf("manifest off:\n%s", got)
	}
}

// --- index (A4 truncation) --------------------------------------------------

func TestReadIndex_UnderCap(t *testing.T) {
	appHome := t.TempDir()
	writeFile(t, MemoryIndexPath(appHome), "  - [a](a.md) — hook\n")
	body, warn := ReadIndex(appHome)
	if warn != "" {
		t.Errorf("no warning expected under cap; got %q", warn)
	}
	if body != "- [a](a.md) — hook" {
		t.Errorf("body should be trimmed index; got %q", body)
	}
}

func TestReadIndex_Missing(t *testing.T) {
	body, warn := ReadIndex(t.TempDir())
	if body != "" || warn != "" {
		t.Errorf("missing index → empty; got body=%q warn=%q", body, warn)
	}
}

func TestTruncateIndex_LineCap(t *testing.T) {
	var b strings.Builder
	for range MaxIndexLines + 50 {
		b.WriteString("- line\n")
	}
	body, warn := truncateIndex(b.String())
	if warn == "" || !strings.Contains(warn, "lines") {
		t.Errorf("expected a line-cap warning; got %q", warn)
	}
	if !strings.Contains(body, "> WARNING:") {
		t.Errorf("truncated body should append the warning; got tail %q", body[len(body)-80:])
	}
	if strings.Count(body, "- line") > MaxIndexLines {
		t.Errorf("body should be capped to %d lines", MaxIndexLines)
	}
}

func TestTruncateIndex_ByteCap(t *testing.T) {
	// A few very long lines blow the byte cap without hitting the line cap.
	long := strings.Repeat("x", 9000)
	raw := long + "\n" + long + "\n" + long // 3 lines, ~27KB
	body, warn := truncateIndex(raw)
	if warn == "" || !strings.Contains(warn, "bytes") {
		t.Errorf("expected a byte-cap warning; got %q", warn)
	}
	if len(body) > MaxIndexBytes+300 { // body = truncated + short warning line
		t.Errorf("body should be near the byte cap; got %d", len(body))
	}
}

// --- paths / IsInMemoryDir --------------------------------------------------

func TestMemoryPaths(t *testing.T) {
	if MemoryDir("") != "" || MemoryIndexPath("") != "" {
		t.Errorf("empty appHome → empty paths")
	}
	if got := MemoryDir("/home/x/.evva"); got != filepath.Join("/home/x/.evva", "memory") {
		t.Errorf("MemoryDir = %q", got)
	}
	if got := MemoryIndexPath("/home/x/.evva"); got != filepath.Join("/home/x/.evva", "memory", "MEMORY.md") {
		t.Errorf("MemoryIndexPath = %q", got)
	}
}

func TestIsInMemoryDir(t *testing.T) {
	appHome := t.TempDir()
	dir := MemoryDir(appHome)
	if !IsInMemoryDir(appHome, filepath.Join(dir, "feedback", "x.md")) {
		t.Errorf("nested path should be inside")
	}
	if IsInMemoryDir(appHome, dir) {
		t.Errorf("dir root itself should not count as inside")
	}
	if IsInMemoryDir(appHome, filepath.Join(dir, "..", "escape.md")) {
		t.Errorf("traversal escape should not be inside")
	}
	if IsInMemoryDir("", filepath.Join(dir, "x.md")) {
		t.Errorf("empty appHome → not inside")
	}
}

func TestEnsureMemoryDir(t *testing.T) {
	appHome := t.TempDir()
	if err := EnsureMemoryDir(appHome); err != nil {
		t.Fatalf("EnsureMemoryDir: %v", err)
	}
	info, err := os.Stat(MemoryDir(appHome))
	if err != nil || !info.IsDir() {
		t.Fatalf("memory dir should exist as a dir: %v", err)
	}
	// Idempotent.
	if err := EnsureMemoryDir(appHome); err != nil {
		t.Errorf("second EnsureMemoryDir should be a no-op; got %v", err)
	}
	if runtime.GOOS != "" && EnsureMemoryDir("") != nil {
		t.Errorf("empty appHome should be a no-op")
	}
}
