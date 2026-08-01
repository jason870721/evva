package memdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMemory(t *testing.T, dir, name, description, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + strings.TrimSuffix(name, ".md") +
		"\ndescription: " + description + "\ntype: project\n---\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func row(name, hash, model string, vec ...float32) EmbedRow {
	return EmbedRow{Name: name, Hash: hash, Model: model, Dims: len(vec), Vec: vec}
}

func TestEmbedIndexRoundTrips(t *testing.T) {
	dir := t.TempDir()
	ix := NewEmbedIndex()
	ix.Put(row("a.md", "h1", "m", 1, 2, 3))
	ix.Put(row("b/c.md", "h2", "m", 4, 5, 6))

	if err := ix.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back := LoadEmbedIndex(dir)
	if back.Len() != 2 {
		t.Fatalf("rows after reload: got %d, want 2", back.Len())
	}
	got, ok := back.Get("b/c.md")
	if !ok {
		t.Fatal("row b/c.md missing after reload")
	}
	if got.Hash != "h2" || got.Dims != 3 || got.Vec[2] != 6 {
		t.Errorf("row round-trip wrong: %+v", got)
	}
}

// The index is a cache. Every unreadable state must degrade to "embed it
// again", never to a failure that stops a session opening.
func TestLoadEmbedIndexToleratesCorruption(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if got := LoadEmbedIndex(t.TempDir()).Len(); got != 0 {
			t.Errorf("missing index should load empty, got %d rows", got)
		}
	})

	t.Run("empty dir string", func(t *testing.T) {
		if got := LoadEmbedIndex("").Len(); got != 0 {
			t.Errorf("empty dir should load empty, got %d rows", got)
		}
	})

	t.Run("partially corrupt keeps the good rows", func(t *testing.T) {
		dir := t.TempDir()
		path := EmbedIndexPath(dir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"name":"good.md","hash":"h","model":"m","dims":2,"vec":[1,2]}` + "\n" +
			`{ this is not json` + "\n" +
			`` + "\n" +
			`{"name":"","hash":"h","model":"m","vec":[1]}` + "\n" +
			`{"name":"novec.md","hash":"h","model":"m"}` + "\n" +
			`{"name":"good2.md","hash":"h","model":"m","dims":2,"vec":[3,4]}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		ix := LoadEmbedIndex(dir)
		if ix.Len() != 2 {
			t.Fatalf("expected the 2 well-formed rows to survive, got %d", ix.Len())
		}
		for _, want := range []string{"good.md", "good2.md"} {
			if _, ok := ix.Get(want); !ok {
				t.Errorf("row %q should have survived", want)
			}
		}
	})

	t.Run("truncated file", func(t *testing.T) {
		dir := t.TempDir()
		path := EmbedIndexPath(dir)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(`{"name":"a.md","hash":"h","model":"m","vec":[1,2`), 0o644)
		if got := LoadEmbedIndex(dir).Len(); got != 0 {
			t.Errorf("a truncated row should be dropped, got %d rows", got)
		}
	})
}

// Vectors are long lines; the scanner's 64KB default would silently
// truncate the index at the first large row.
func TestLoadEmbedIndexHandlesLargeVectors(t *testing.T) {
	dir := t.TempDir()
	big := make([]float32, 3072)
	for i := range big {
		big[i] = float32(i) / 3072
	}
	ix := NewEmbedIndex()
	ix.Put(row("big.md", "h", "m", big...))
	ix.Put(row("small.md", "h", "m", 1))
	if err := ix.Save(dir); err != nil {
		t.Fatal(err)
	}

	back := LoadEmbedIndex(dir)
	if back.Len() != 2 {
		t.Fatalf("expected both rows to survive a 3072-dim vector, got %d", back.Len())
	}
	if got, _ := back.Get("big.md"); len(got.Vec) != 3072 {
		t.Errorf("large vector truncated: %d dims", len(got.Vec))
	}
}

func TestStaleNamesDetectsNewChangedAndModelSwap(t *testing.T) {
	headers := []MemoryHeader{{Filename: "a.md"}, {Filename: "b.md"}, {Filename: "c.md"}}
	texts := map[string]string{"a.md": "alpha", "b.md": "beta", "c.md": "gamma"}

	ix := NewEmbedIndex()
	ix.Put(row("a.md", HashText("alpha"), "m", 1))       // current
	ix.Put(row("b.md", HashText("OLD CONTENT"), "m", 1)) // edited since
	// c.md has no row at all — never embedded.

	stale := ix.StaleNames(headers, texts, "m")
	if len(stale) != 2 {
		t.Fatalf("stale: got %v, want b.md and c.md", stale)
	}

	// A model swap invalidates everything: vectors from two models are not
	// comparable, and scoring across them yields confident nonsense.
	all := ix.StaleNames(headers, texts, "different-model")
	if len(all) != 3 {
		t.Errorf("a model change should invalidate every row, got %v", all)
	}
}

// An unreadable file is not something re-embedding can fix, so it must not
// be reported stale forever.
func TestStaleNamesSkipsUnreadableFiles(t *testing.T) {
	headers := []MemoryHeader{{Filename: "a.md"}, {Filename: "gone.md"}}
	texts := map[string]string{"a.md": "alpha"} // gone.md absent
	ix := NewEmbedIndex()
	ix.Put(row("a.md", HashText("alpha"), "m", 1))

	if got := ix.StaleNames(headers, texts, "m"); len(got) != 0 {
		t.Errorf("expected nothing stale, got %v", got)
	}
}

func TestPruneDropsDeletedMemories(t *testing.T) {
	ix := NewEmbedIndex()
	ix.Put(row("keep.md", "h", "m", 1))
	ix.Put(row("gone.md", "h", "m", 1))
	ix.Put(row("also-gone.md", "h", "m", 1))

	dropped := ix.Prune([]MemoryHeader{{Filename: "keep.md"}})
	if dropped != 2 {
		t.Errorf("dropped: got %d, want 2", dropped)
	}
	if ix.Len() != 1 {
		t.Errorf("rows left: got %d, want 1", ix.Len())
	}
	if _, ok := ix.Get("keep.md"); !ok {
		t.Error("the live row was pruned")
	}
}

// Save must be atomic: an interrupted write leaves the previous index
// readable rather than a half-file.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	ix := NewEmbedIndex()
	ix.Put(row("a.md", "h", "m", 1))
	if err := ix.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(dir); err != nil {
		t.Fatalf("second Save should overwrite cleanly: %v", err)
	}
	if _, err := os.Stat(EmbedIndexPath(dir) + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind after a successful save")
	}
	if LoadEmbedIndex(dir).Len() != 1 {
		t.Error("index unreadable after a second save")
	}
}

// The sidecar lives inside the memory dir, so the *.md scanner must not
// mistake it for a memory.
func TestIndexDirIsInvisibleToTheScanner(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "real.md", "a real memory", "body")
	ix := NewEmbedIndex()
	ix.Put(row("real.md", "h", "m", 1))
	if err := ix.Save(dir); err != nil {
		t.Fatal(err)
	}

	headers := ScanMemoryFiles(dir)
	if len(headers) != 1 || headers[0].Filename != "real.md" {
		t.Errorf("scanner picked up sidecar files: %+v", headers)
	}
}

func TestEmbedTextLeadsWithNameAndDescription(t *testing.T) {
	got := EmbedText("feedback/pr-flow.md", "how the user wants PRs opened", "Always target dev.")
	if !strings.HasPrefix(got, "feedback/pr-flow.md\nhow the user wants PRs opened") {
		t.Errorf("name and description should lead: %q", got)
	}
	if !strings.Contains(got, "Always target dev.") {
		t.Error("body missing")
	}
}

// The hash and the vector must describe the SAME text, or a change past the
// truncation point would look like a change the vector never saw.
func TestEmbedTextIsCappedAndHashMatchesWhatIsSent(t *testing.T) {
	body := strings.Repeat("x", 20000)
	got := EmbedText("a.md", "d", body)
	if len(got) > embedTextMaxBytes {
		t.Fatalf("embed text not capped: %d bytes", len(got))
	}
	// Two bodies differing only past the cap produce the same text, hence
	// the same hash — and correctly so, since the embedder saw neither tail.
	other := EmbedText("a.md", "d", body+"DIFFERENT TAIL")
	if HashText(got) != HashText(other) {
		t.Error("text past the cap changed the hash; the hash must describe what was actually embedded")
	}
}

func TestHashTextIsStable(t *testing.T) {
	// Built two different ways so the comparison is a real determinism
	// check rather than the compiler evaluating one expression twice.
	a := HashText("abc")
	b := HashText(strings.Join([]string{"a", "b", "c"}, ""))
	if a != b {
		t.Errorf("HashText is not deterministic: %s vs %s", a, b)
	}
	if a == HashText("abd") {
		t.Error("HashText collided on different input")
	}
}

func TestOriginKeyParsesFromFrontmatter(t *testing.T) {
	fm, body := ParseFrontmatter("---\nname: x\norigin: -Users-me-proj\n---\nbody here\n")
	if fm[OriginKey] != "-Users-me-proj" {
		t.Errorf("origin: got %q", fm[OriginKey])
	}
	if !strings.Contains(body, "body here") {
		t.Error("body lost")
	}
}

// Every memory written before v1.18 lacks the key; that must read as
// "unknown origin", not as an error.
func TestOriginAbsentOnLegacyFiles(t *testing.T) {
	fm, _ := ParseFrontmatter("---\nname: x\ndescription: d\n---\nbody\n")
	if fm[OriginKey] != "" {
		t.Errorf("expected empty origin on a legacy file, got %q", fm[OriginKey])
	}
}

func TestEmbedIndexPathEmptyDir(t *testing.T) {
	if got := EmbedIndexPath(""); got != "" {
		t.Errorf("empty dir should yield empty path, got %q", got)
	}
	if err := NewEmbedIndex().Save(""); err != nil {
		t.Errorf("Save on an empty dir should be a no-op, got %v", err)
	}
}

func TestRowsAreSortedForStableDiffs(t *testing.T) {
	ix := NewEmbedIndex()
	for _, n := range []string{"z.md", "a.md", "m.md"} {
		ix.Put(row(n, "h", "m", 1))
	}
	rows := ix.Rows()
	for i := 1; i < len(rows); i++ {
		if rows[i].Name < rows[i-1].Name {
			t.Errorf("rows not sorted: %s before %s", rows[i-1].Name, rows[i].Name)
		}
	}
}
