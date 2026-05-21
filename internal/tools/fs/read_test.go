package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/tools"
)

// writeTempFile is the shared fixture builder used across fs tests —
// writes content to a fresh tempdir and returns the absolute path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// writeTempPNG writes a minimal valid 1x1 white PNG to the given path.
// Produces a proper PNG that both image.Decode and detectImageMIME can parse.
func writeTempPNG(t *testing.T, path string) {
	t.Helper()
	// Minimal valid 1x1 white PNG (67 bytes).
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk len + tag
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 px
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, 0x0E,
		0xFC, 0x1F, 0xA7, 0x00, 0x00, 0x00, 0x00, 0x49, // IEND chunk
		0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatalf("write PNG fixture: %v", err)
	}
}

// recordFullRead simulates a successful full-file read on the tracker
// without going through the Read tool. Captures the current on-disk
// mtime so subsequent edits / writes won't trip the drift guard.
func recordFullRead(t *testing.T, tr *ReadTracker, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	tr.Record(path, info.ModTime(), false)
}

func TestRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.txt")
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+missing+`"}`))
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("expected not-found; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

// TestRead_DirectoryErrors — the new tool refuses directory paths and
// points the caller at `ls` (matches ref FileReadTool behavior;
// previous evva versions delegated to shell.Tree).
func TestRead_DirectoryErrors(t *testing.T) {
	dir := t.TempDir()
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+dir+`"}`))
	if !res.IsError {
		t.Fatalf("expected directory rejection; got content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "directory") {
		t.Errorf("error should mention 'directory'; got %q", res.Content)
	}
}

func TestRead_EmptyFile(t *testing.T) {
	path := writeTempFile(t, "")
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "0 lines") {
		t.Errorf("empty file should report 0 lines; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "<system-reminder>") {
		t.Errorf("empty file should use system-reminder framing; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "exists but the contents are empty") {
		t.Errorf("expected empty-file warning text; got %q", res.Content)
	}
}

func TestRead_HappyPath_CatNFormat(t *testing.T) {
	path := writeTempFile(t, "alpha\nbeta\ngamma\n")
	tr := NewReadTracker()
	tool := NewRead(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{
		"3 lines",
		"     1\talpha",
		"     2\tbeta",
		"     3\tgamma",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q\nfull:\n%s", want, res.Content)
		}
	}
	entry, ok := tr.Lookup(path)
	if !ok {
		t.Fatal("ReadTracker not recorded after successful read")
	}
	if entry.IsPartialView {
		t.Error("full read should record IsPartialView=false")
	}
}

func TestRead_OffsetAndLimitSlice(t *testing.T) {
	path := writeTempFile(t, "l1\nl2\nl3\nl4\nl5\n")
	tr := NewReadTracker()
	tool := NewRead(tr, "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","offset":2,"limit":2}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{
		"showing lines 2-3",
		"     2\tl2",
		"     3\tl3",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q\nfull:\n%s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "     1\tl1") {
		t.Error("offset=2 should have skipped line 1")
	}
	if strings.Contains(res.Content, "     4\tl4") {
		t.Error("limit=2 should have stopped after line 3")
	}
	// Partial-view should be recorded so Edit/Write force a re-read.
	entry, ok := tr.Lookup(path)
	if !ok {
		t.Fatal("tracker not updated on partial read")
	}
	if !entry.IsPartialView {
		t.Error("partial read should record IsPartialView=true")
	}
}

func TestRead_OffsetPastEOF(t *testing.T) {
	path := writeTempFile(t, "only-line\n")
	tool := NewRead(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","offset":99}`))

	if res.IsError {
		t.Fatalf("offset past EOF should be a graceful message, not an error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "<system-reminder>") {
		t.Errorf("expected system-reminder framing; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "shorter than the provided offset") {
		t.Errorf("expected past-EOF warning text; got %q", res.Content)
	}
}

func TestRead_NegativeOffsetClampsToOne(t *testing.T) {
	path := writeTempFile(t, "x\ny\n")
	tool := NewRead(NewReadTracker(), "")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","offset":-5}`))

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "     1\tx") {
		t.Errorf("negative offset should clamp to 1; got %q", res.Content)
	}
}

func TestRead_DecodeError(t *testing.T) {
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{bogus`))
	if !res.IsError || !strings.Contains(res.Content, "decode") {
		t.Errorf("expected decode error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

// TestRead_UnchangedStub — re-reading an unchanged file with no
// offset/limit returns the cache-hint stub instead of re-dumping the
// content (matches Claude Code's FILE_UNCHANGED_STUB behavior).
func TestRead_UnchangedStub(t *testing.T) {
	path := writeTempFile(t, "stable content\n")
	tr := NewReadTracker()
	tool := NewRead(tr, "")

	first, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if first.IsError {
		t.Fatalf("first read failed: %s", first.Content)
	}
	if !strings.Contains(first.Content, "stable content") {
		t.Errorf("first read should contain content; got %q", first.Content)
	}

	second, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if second.IsError {
		t.Fatalf("second read failed: %s", second.Content)
	}
	if !strings.Contains(second.Content, "File unchanged since last read") {
		t.Errorf("second identical read should return unchanged stub; got %q", second.Content)
	}
}

// TestRead_StubResetsOnMtimeBump — bumping mtime invalidates the stub
// and the next read returns full content.
func TestRead_StubResetsOnMtimeBump(t *testing.T) {
	path := writeTempFile(t, "v1\n")
	tr := NewReadTracker()
	tool := NewRead(tr, "")

	tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))

	// Move mtime forward and overwrite the file from underneath the
	// tracker.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := os.WriteFile(path, []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	// Reapply mtime since WriteFile reset it.
	os.Chtimes(path, later, later)

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if strings.Contains(res.Content, "File unchanged since last read") {
		t.Errorf("expected fresh content after mtime bump, got stub: %q", res.Content)
	}
	if !strings.Contains(res.Content, "v2") {
		t.Errorf("expected updated content 'v2' in re-read; got %q", res.Content)
	}
}

// TestRead_Image_PNG — reading a PNG returns an image content block.
func TestRead_Image_PNG(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "screenshot.png")
	// Minimal valid 1x1 white PNG.
	writeTempPNG(t, imgPath)

	tool := NewRead(NewReadTracker(), "")
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+imgPath+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success for image read; got error: %s", res.Content)
	}
	if len(res.ContentBlocks) != 1 {
		t.Fatalf("expected 1 ContentBlock; got %d", len(res.ContentBlocks))
	}
	cb := res.ContentBlocks[0]
	if cb.Type != tools.ContentBlockImage {
		t.Fatalf("expected image content block; got %s", cb.Type)
	}
	if cb.Image == nil {
		t.Fatal("Image should not be nil")
	}
	if cb.Image.MIMEType != "image/png" {
		t.Errorf("expected image/png; got %s", cb.Image.MIMEType)
	}
	if cb.Image.OriginalSize <= 0 {
		t.Errorf("expected positive original size; got %d", cb.Image.OriginalSize)
	}
	if cb.Image.Base64Data == "" {
		t.Error("expected non-empty base64 data")
	}
}

// TestRead_Image_TooLarge — images over the size limit are rejected.
func TestRead_Image_TooLarge(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "big.jpg")
	// Fake a file larger than maxImageBytes.
	f, _ := os.Create(imgPath)
	f.Truncate(maxImageBytes + 1)
	f.Close()

	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+imgPath+`"}`))
	if !res.IsError {
		t.Fatalf("expected rejection for too-large image; got content=%q", res.Content)
	}
}

// TestRead_SVG_ReadAsText — SVG files are not in imageExts and should
// be read as text, not routed to the image path.
func TestRead_SVG_ReadAsText(t *testing.T) {
	path := writeTempFile(t, "<svg></svg>\n")
	if err := os.Rename(path, path+".svg"); err != nil {
		t.Fatal(err)
	}
	svgPath := path + ".svg"

	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+svgPath+`"}`))
	if res.IsError {
		t.Fatalf("SVG should be read as text, not rejected; got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "<svg>") {
		t.Errorf("expected SVG content in text output; got %q", res.Content)
	}
}

// TestRead_PagesOnNonPDFRejected — `pages` only applies to PDFs.
func TestRead_PagesOnNonPDFRejected(t *testing.T) {
	path := writeTempFile(t, "text\n")
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"file_path":"`+path+`","pages":"1-5"}`))
	if !res.IsError {
		t.Fatal("expected error for pages on non-PDF")
	}
	if !strings.Contains(res.Content, "only valid for PDF") {
		t.Errorf("error should mention PDF-only; got %q", res.Content)
	}
}

// =============================================================================
// 1:1 ref port: encoding, line endings, binary, device.
// =============================================================================

// TestRead_CRLFNormalizedToLF — Windows files (CRLF terminators) read
// as clean LF-only lines so the model doesn't see "\r" attached to
// each line.
func TestRead_CRLFNormalizedToLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if res.IsError {
		t.Fatalf("CRLF read should succeed: %s", res.Content)
	}
	if strings.Contains(res.Content, "\r") {
		t.Errorf("CRLF should be normalized — output still contains \\r: %q", res.Content)
	}
	for _, want := range []string{
		"     1\talpha",
		"     2\tbeta",
		"     3\tgamma",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q after CRLF normalization", want)
		}
	}
}

// TestRead_UTF16LEDecoded — Notepad-saved UTF-16 LE file (BOM ff fe)
// decodes into normal UTF-8 text for the model.
func TestRead_UTF16LEDecoded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notepad.txt")
	// Build UTF-16 LE: BOM + "hi" (each char 2 LE bytes).
	raw := []byte{0xff, 0xfe, 'h', 0, 'i', 0}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if res.IsError {
		t.Fatalf("UTF-16 read should succeed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "     1\thi") {
		t.Errorf("UTF-16 not decoded — got: %q", res.Content)
	}
}

// TestRead_UTF8BOMStripped — UTF-8 BOM (ef bb bf) does not appear in
// the output content.
func TestRead_UTF8BOMStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	raw := append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\n")...)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewRead(NewReadTracker(), "")
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
	if res.IsError {
		t.Fatalf("BOM-prefixed read should succeed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "     1\thello") {
		t.Errorf("BOM not stripped — got: %q", res.Content)
	}
	if strings.Contains(res.Content, "\xef\xbb\xbf") {
		t.Errorf("BOM bytes leaked into output: %q", res.Content)
	}
}

// TestRead_RejectsBinaryExtensions — .exe/.zip/.so etc. error out so
// their bytes don't corrupt the conversation context.
func TestRead_RejectsBinaryExtensions(t *testing.T) {
	for _, ext := range []string{".exe", ".zip", ".so", ".dylib", ".class", ".sqlite"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "f"+ext)
		os.WriteFile(path, []byte("\x00\x01\x02junk"), 0o644)
		tool := NewRead(NewReadTracker(), "")
		res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+path+`"}`))
		if !res.IsError {
			t.Errorf("expected binary rejection for %s; got: %s", ext, res.Content)
		}
		if !strings.Contains(res.Content, "binary") {
			t.Errorf("%s error should mention 'binary'; got %q", ext, res.Content)
		}
	}
}

// TestRead_BlockedDevicePathsRejected — /dev/zero etc. error before
// any I/O so we never block on infinite output.
func TestRead_BlockedDevicePathsRejected(t *testing.T) {
	for _, dev := range []string{"/dev/zero", "/dev/random", "/dev/urandom", "/dev/stdin", "/dev/tty"} {
		tool := NewRead(NewReadTracker(), "")
		res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"file_path":"`+dev+`"}`))
		if !res.IsError {
			t.Errorf("expected device rejection for %s", dev)
		}
		if !strings.Contains(res.Content, "device") {
			t.Errorf("%s error should mention 'device'; got %q", dev, res.Content)
		}
	}
}

func TestHasBinaryExtension(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/x/main.go", false},
		{"/x/main.exe", true},
		{"/x/archive.ZIP", true}, // case-insensitive
		{"/x/photo.jpg", true},
		{"/x/data.sqlite3", true},
		{"/x/no_extension", false},
	}
	for _, c := range cases {
		if got := hasBinaryExtension(c.path); got != c.want {
			t.Errorf("hasBinaryExtension(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsBlockedDevicePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/dev/zero", true},
		{"/dev/random", true},
		{"/dev/null", false},
		{"/dev/stdin", true},
		{"/proc/self/fd/0", true},
		{"/proc/123/fd/2", true},
		{"/proc/123/maps", false},
		{"/home/user/file.txt", false},
	}
	for _, c := range cases {
		if got := isBlockedDevicePath(c.path); got != c.want {
			t.Errorf("isBlockedDevicePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
