package memdir

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The embedding sidecar: one vector per memory file, cached on disk so a
// session only pays to embed what actually changed.
//
// This file stays inside the package's stdlib-only charter. It owns STORAGE
// and STALENESS — load, save, hash-diff, prune — and deliberately not
// ranking, because ranking needs a similarity function that lives next to
// the Embedder in pkg/llm. The recall sub-package joins the two, the same
// split that already puts the LLM side-query there.
//
// Corruption is never fatal. The index is a cache: any unreadable state
// degrades to "embed everything again", which costs time and nothing else.
// A memory store that refuses to open because a cache file is truncated
// would be a strictly worse product than one with no cache at all.

const (
	// EmbedIndexDirName is the sidecar directory inside the memory dir. Dot-
	// prefixed so ScanMemoryFiles' *.md walk never mistakes its contents for
	// memories, and so it stays out of the operator's way.
	EmbedIndexDirName = ".index"

	// EmbedIndexFileName is the JSONL vector cache. Line-oriented so a
	// truncated write costs one row rather than the whole file — an
	// interrupted session is the common case, not a rare one.
	EmbedIndexFileName = "embeddings.jsonl"

	// embedTextMaxBytes bounds what is sent to the embedder per memory.
	// Embedding models have their own token ceilings and silently truncate;
	// doing it here makes the hash and the vector describe the same text,
	// which is what keeps staleness detection honest.
	embedTextMaxBytes = 8 * 1024
)

// EmbedRow is one cached vector.
type EmbedRow struct {
	// Name is the memory's dir-relative filename — the same key
	// MemoryHeader.Filename uses everywhere else in this package.
	Name string `json:"name"`
	// Hash is the SHA-256 of the exact text that produced Vec. Comparing it
	// against a freshly-built text is what detects an out-of-band edit; mtime
	// alone would re-embed on every `touch`.
	Hash string `json:"hash"`
	// Model is the embedding model that produced Vec. Vectors from two
	// different models are not comparable, and cosine between them yields a
	// confident number that means nothing — so a model change invalidates
	// every row.
	Model string `json:"model"`
	Dims  int    `json:"dims"`
	// Origin is the project the memory was written in, when known. Empty for
	// rows written before provenance existed.
	Origin string    `json:"origin,omitempty"`
	Vec    []float32 `json:"vec"`
}

// EmbedIndex is the in-memory view of the sidecar, keyed by row name.
type EmbedIndex struct {
	rows map[string]EmbedRow
}

// NewEmbedIndex returns an empty index.
func NewEmbedIndex() *EmbedIndex {
	return &EmbedIndex{rows: map[string]EmbedRow{}}
}

// EmbedIndexPath returns <dir>/.index/embeddings.jsonl, or "" for an empty dir.
func EmbedIndexPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, EmbedIndexDirName, EmbedIndexFileName)
}

// LoadEmbedIndex reads the sidecar. It NEVER returns an error: a missing
// file, an unreadable one, or a partially-corrupt one all yield whatever
// rows could be parsed. Callers treat a thin index as "more to embed",
// which is exactly the right recovery.
func LoadEmbedIndex(dir string) *EmbedIndex {
	ix := NewEmbedIndex()
	path := EmbedIndexPath(dir)
	if path == "" {
		return ix
	}
	f, err := os.Open(path)
	if err != nil {
		return ix
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Vectors are long lines: 1536 float32s render to well over the 64KB
	// default. Without this the scanner would stop at the first big row and
	// silently return a truncated index.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row EmbedRow
		if json.Unmarshal([]byte(line), &row) != nil || row.Name == "" || len(row.Vec) == 0 {
			continue // one bad line costs one row, not the file
		}
		ix.rows[row.Name] = row
	}
	return ix
}

// Save writes the sidecar, creating the directory if needed. Writes to a
// temp file and renames so an interrupted save leaves the previous index
// intact rather than a half-written one.
func (ix *EmbedIndex) Save(dir string) error {
	path := EmbedIndexPath(dir)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, name := range ix.Names() {
		if err := enc.Encode(ix.rows[name]); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Names returns the row names, sorted, so Save produces a stable file and
// diffs stay readable.
func (ix *EmbedIndex) Names() []string {
	out := make([]string, 0, len(ix.rows))
	for n := range ix.rows {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Len is the number of cached rows.
func (ix *EmbedIndex) Len() int { return len(ix.rows) }

// Get returns a row by name.
func (ix *EmbedIndex) Get(name string) (EmbedRow, bool) {
	r, ok := ix.rows[name]
	return r, ok
}

// Put inserts or replaces a row.
func (ix *EmbedIndex) Put(row EmbedRow) {
	if row.Name == "" {
		return
	}
	if ix.rows == nil {
		ix.rows = map[string]EmbedRow{}
	}
	ix.rows[row.Name] = row
}

// Rows returns every cached row, in Names order.
func (ix *EmbedIndex) Rows() []EmbedRow {
	names := ix.Names()
	out := make([]EmbedRow, 0, len(names))
	for _, n := range names {
		out = append(out, ix.rows[n])
	}
	return out
}

// Prune drops rows whose memory file no longer exists, and reports how many
// went. Called with the same header list the caller already scanned, so it
// costs no extra IO.
func (ix *EmbedIndex) Prune(headers []MemoryHeader) int {
	live := make(map[string]bool, len(headers))
	for _, h := range headers {
		live[h.Filename] = true
	}
	dropped := 0
	for name := range ix.rows {
		if !live[name] {
			delete(ix.rows, name)
			dropped++
		}
	}
	return dropped
}

// StaleNames returns the memories that need embedding under model: those
// with no row, a row from a different model, or a row whose hash no longer
// matches the file's current text.
//
// texts must be keyed by the same dir-relative filename as headers; a
// memory missing from texts is skipped rather than treated as stale, since
// an unreadable file is not something re-embedding can fix.
func (ix *EmbedIndex) StaleNames(headers []MemoryHeader, texts map[string]string, model string) []string {
	var out []string
	for _, h := range headers {
		text, ok := texts[h.Filename]
		if !ok {
			continue
		}
		row, found := ix.rows[h.Filename]
		if !found || row.Model != model || row.Hash != HashText(text) {
			out = append(out, h.Filename)
		}
	}
	return out
}

// EmbedText builds the string that represents a memory to the embedder.
//
// Name and description lead because they are the highest-signal,
// human-curated summary of what the memory is for; the body follows for the
// detail a paraphrased query might match on. The whole thing is capped at
// embedTextMaxBytes so the hash and the vector always describe the same
// text — if the model truncated internally and we hashed the full body, a
// change past the cutoff would look like a change the vector never saw.
func EmbedText(name, description, body string) string {
	var b strings.Builder
	b.WriteString(name)
	if description != "" {
		b.WriteString("\n")
		b.WriteString(description)
	}
	if body = strings.TrimSpace(body); body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	s := b.String()
	if len(s) > embedTextMaxBytes {
		s = s[:embedTextMaxBytes]
	}
	return s
}

// HashText is the staleness key: SHA-256 of the exact embedded text.
func HashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
