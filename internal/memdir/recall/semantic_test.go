package recall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
)

// fakeEmbedder maps known phrases to fixed vectors so ranking is
// deterministic. Anything unknown gets an orthogonal vector, which is how a
// test expresses "unrelated" without depending on a real model.
type fakeEmbedder struct {
	model string
	vecs  map[string][]float32
	err   error
	calls int
}

func (f *fakeEmbedder) EmbedModel() string { return f.model }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vecFor(t)
	}
	return out, nil
}

// vecFor picks a vector by the first known keyword the text contains.
func (f *fakeEmbedder) vecFor(text string) []float32 {
	lower := strings.ToLower(text)
	for key, v := range f.vecs {
		if strings.Contains(lower, key) {
			return v
		}
	}
	return []float32{0, 0, 1}
}

func writeMem(t *testing.T, dir, name, description, body string) {
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

// deployStore is the fixture behind the audit's central question: does a
// query phrased one way find a memory phrased another? The embedder maps
// "deploy" and "release" to the same vector, so semantic mode finds it and
// keyword mode does not.
func deployStore(t *testing.T) (string, *fakeEmbedder) {
	t.Helper()
	dir := t.TempDir()
	writeMem(t, dir, "ci-release-flow.md", "how the CI release flow works", "Tag on main, the workflow publishes.")
	writeMem(t, dir, "editor-prefs.md", "preferred editor settings", "Tabs, not spaces.")

	e := &fakeEmbedder{
		model: "fake-v1",
		vecs: map[string][]float32{
			"release": {1, 0, 0},
			"deploy":  {1, 0, 0}, // same vector — the whole point
			"editor":  {0, 1, 0},
		},
	}
	return dir, e
}

func TestSyncEmbedsOnlyWhatChanged(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)

	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if res.Embedded != 2 || res.Total != 2 {
		t.Fatalf("first sync: %+v, want 2 embedded", res)
	}

	// Nothing changed — a second pass must embed nothing.
	before := e.calls
	res, err = s.Sync(context.Background())
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if res.Embedded != 0 {
		t.Errorf("unchanged store re-embedded %d rows", res.Embedded)
	}
	if e.calls != before {
		t.Errorf("unchanged store still called the embedder (%d → %d)", before, e.calls)
	}

	// Touching ONE memory re-embeds exactly one row.
	writeMem(t, dir, "editor-prefs.md", "preferred editor settings", "Spaces, not tabs. Changed my mind.")
	res, err = s.Sync(context.Background())
	if err != nil {
		t.Fatalf("third Sync: %v", err)
	}
	if res.Embedded != 1 {
		t.Errorf("one edited memory should re-embed exactly 1 row, got %d", res.Embedded)
	}
}

func TestSyncDropsDeletedMemories(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "editor-prefs.md")); err != nil {
		t.Fatal(err)
	}
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync after delete: %v", err)
	}
	if res.Dropped != 1 || res.Total != 1 {
		t.Errorf("delete should drop exactly one row: %+v", res)
	}
}

// The audit's headline question. A paraphrased query must find the memory
// where a literal keyword search cannot.
func TestSemanticSearchFindsParaphrasedMemory(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	hits, mode := s.Search(context.Background(), "deploy a new build", 5, 0.5)
	if mode != ModeSemantic {
		t.Fatalf("mode: got %q, want semantic", mode)
	}
	if len(hits) != 1 || hits[0].Header.Filename != "ci-release-flow.md" {
		t.Fatalf("expected the release-flow memory, got %+v", hits)
	}

	// And confirm the premise: the same query finds nothing literally.
	kw := NewSearcher(dir, nil)
	khits, kmode := kw.Search(context.Background(), "deploy a new build", 5, 0.5)
	if kmode != ModeKeyword {
		t.Fatalf("no embedder should force keyword mode, got %q", kmode)
	}
	for _, h := range khits {
		if h.Header.Filename == "ci-release-flow.md" {
			t.Error("keyword search unexpectedly matched — the fixture no longer demonstrates the gap")
		}
	}
}

func TestSearchAppliesScoreFloor(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	// "editor" is orthogonal to "release"; a high floor must reject it
	// rather than return a confident-looking weak match.
	hits, _ := s.Search(context.Background(), "release process", 5, 0.9)
	for _, h := range hits {
		if h.Header.Filename == "editor-prefs.md" {
			t.Error("an orthogonal memory passed a 0.9 floor")
		}
	}
}

// Every embedder failure must fall back rather than surface as "no
// memories", which the model would read as a fact about the store.
func TestSearchFallsBackToKeywordOnEmbedderFailure(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	e.err = errors.New("ollama is not running")
	hits, mode := s.Search(context.Background(), "editor", 5, 0.1)
	if mode != ModeKeyword {
		t.Fatalf("a failed query embedding should fall back to keyword, got %q", mode)
	}
	if len(hits) == 0 {
		t.Error("keyword fallback found nothing for a literal term present in the store")
	}
}

func TestSearchWithNoIndexFallsBackToKeyword(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e) // note: never Synced, so the index is empty

	hits, mode := s.Search(context.Background(), "editor", 5, 0.1)
	if mode != ModeKeyword {
		t.Errorf("an empty index should fall back to keyword, got %q", mode)
	}
	if len(hits) == 0 {
		t.Error("keyword fallback found nothing")
	}
}

// A model swap leaves rows that cannot be compared with the new query
// vector. Skipping them (rather than scoring them) is what keeps a
// half-migrated index from ranking on noise.
func TestSearchIgnoresRowsFromAnotherModel(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	e2 := &fakeEmbedder{model: "fake-v2", vecs: e.vecs}
	s2 := NewSearcher(dir, e2)
	hits, mode := s2.Search(context.Background(), "release process", 5, 0.1)
	if mode == ModeSemantic && len(hits) > 0 {
		t.Error("rows from the old model were scored against the new model's query vector")
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.md", "b.md", "c.md", "d.md"} {
		writeMem(t, dir, n, "shared topic release", "release notes body")
	}
	s := NewSearcher(dir, nil)
	hits, _ := s.Search(context.Background(), "release", 2, 0)
	if len(hits) != 2 {
		t.Errorf("limit not applied: got %d hits, want 2", len(hits))
	}
}

func TestSearchEmptyQueryAndDir(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if hits, _ := s.Search(context.Background(), "   ", 5, 0); len(hits) != 0 {
		t.Error("a blank query should return nothing")
	}
	empty := NewSearcher("", e)
	if hits, _ := empty.Search(context.Background(), "x", 5, 0); len(hits) != 0 {
		t.Error("an empty dir should return nothing")
	}
}

// Sync with no embedder is a no-op, not an error — keyword mode is a
// supported configuration.
func TestSyncWithoutEmbedderIsNoOp(t *testing.T) {
	dir, _ := deployStore(t)
	s := NewSearcher(dir, nil)
	res, err := s.Sync(context.Background())
	if err != nil || res.Embedded != 0 {
		t.Errorf("expected a clean no-op, got %+v, %v", res, err)
	}
	if s.HasEmbedder() {
		t.Error("HasEmbedder should be false with a nil embedder")
	}
}

// Narrow must fail OPEN: returning nil means "no narrowing", and the caller
// then sends the full manifest. Failing closed would silently shrink the
// model's memory with nothing reporting it.
func TestNarrowFailsOpen(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	headers := memdir.ScanMemoryFiles(dir)

	t.Run("no embedder", func(t *testing.T) {
		if got := NewSearcher(dir, nil).Narrow(context.Background(), "q", headers, 1); got != nil {
			t.Error("expected nil (no narrowing) with no embedder")
		}
	})

	t.Run("empty index", func(t *testing.T) {
		if got := s.Narrow(context.Background(), "q", headers, 1); got != nil {
			t.Error("expected nil (no narrowing) with an unsynced index")
		}
	})

	t.Run("embedder error", func(t *testing.T) {
		if _, err := s.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
		e.err = errors.New("down")
		if got := s.Narrow(context.Background(), "q", headers, 1); got != nil {
			t.Error("expected nil (no narrowing) when the query embedding fails")
		}
		e.err = nil
	})

	t.Run("keep >= len does nothing", func(t *testing.T) {
		if got := s.Narrow(context.Background(), "q", headers, len(headers)); got != nil {
			t.Error("narrowing to the full size should be a no-op")
		}
	})
}

// A partially-built index would drop the uncovered memories entirely —
// worse than not narrowing, because the loss is invisible.
func TestNarrowBailsOnPartialIndex(t *testing.T) {
	dir, e := deployStore(t)
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Add a third memory that the index does not know about yet.
	writeMem(t, dir, "new-thing.md", "a memory added after the last sync", "body")
	headers := memdir.ScanMemoryFiles(dir)

	if got := s.Narrow(context.Background(), "release", headers, 1); got != nil {
		t.Errorf("expected no narrowing while the index lags the store, got %d headers", len(got))
	}
}

func TestNarrowKeepsTopMatchesInInputOrder(t *testing.T) {
	dir := t.TempDir()
	writeMem(t, dir, "release.md", "release topic", "release body")
	writeMem(t, dir, "editor.md", "editor topic", "editor body")
	writeMem(t, dir, "other.md", "unrelated topic", "unrelated body")
	e := &fakeEmbedder{
		model: "fake-v1",
		vecs: map[string][]float32{
			"release": {1, 0, 0},
			"editor":  {0, 1, 0},
		},
	}
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	headers := memdir.ScanMemoryFiles(dir)
	got := s.Narrow(context.Background(), "release", headers, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 survivors, got %d", len(got))
	}
	// The best match must be present...
	var names []string
	for _, h := range got {
		names = append(names, h.Filename)
	}
	if !contains(names, "release.md") {
		t.Errorf("the top match was narrowed away: %v", names)
	}
	// ...and the survivors must keep the INPUT order, not score order, so
	// the downstream selector is not biased toward the first entries.
	inputPos := map[string]int{}
	for i, h := range headers {
		inputPos[h.Filename] = i
	}
	for i := 1; i < len(got); i++ {
		if inputPos[got[i].Filename] < inputPos[got[i-1].Filename] {
			t.Errorf("survivors were re-sorted by score: %v", names)
		}
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestSyncRecordsOrigin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scoped.md")
	content := "---\nname: scoped\ndescription: a scoped memory\ntype: project\norigin: -Users-me-projA\n---\n\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &fakeEmbedder{model: "m", vecs: map[string][]float32{"scoped": {1, 0, 0}}}
	s := NewSearcher(dir, e)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	hits, mode := s.Search(context.Background(), "scoped", 5, 0)
	if mode != ModeSemantic || len(hits) != 1 {
		t.Fatalf("expected one semantic hit, got %d in %s mode", len(hits), mode)
	}
	if hits[0].Origin != "-Users-me-projA" {
		t.Errorf("origin not carried through to the hit: %q", hits[0].Origin)
	}
}
