package recall

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/llm"
)

// errVectorCountMismatch fires when a backend returns a different number of
// vectors than it was given inputs. Both shipped backends already reject
// this, so reaching it means a third-party Embedder broke the contract —
// worth refusing loudly rather than writing a cache in which every vector
// is attached to the wrong memory.
var errVectorCountMismatch = errors.New("recall: embedder returned a vector count that does not match its inputs")

// Semantic search over the memory store.
//
// This file joins the two halves the package boundary keeps apart:
// internal/memdir owns the on-disk vector cache (stdlib only), pkg/llm owns
// the Embedder and the similarity function, and a Searcher here uses both.
//
// The design point that matters most is the FALLBACK. An Embedder is
// optional everywhere — no Ollama running, no API key, an embedding call
// that fails mid-session — and in every one of those cases search degrades
// to keyword matching rather than erroring. Memory recall existed and
// worked before vectors; a wave that makes it a setup wall would be a
// regression for every operator who never configures one.

// Mode records how a result set was ranked, so callers can tell the model
// what it is looking at. A keyword hit and a semantic hit deserve different
// confidence.
const (
	ModeSemantic = "semantic"
	ModeKeyword  = "keyword"
)

// Hit is one ranked memory.
type Hit struct {
	Header memdir.MemoryHeader
	// Score is cosine similarity in semantic mode (roughly 0..1 for related
	// text) or a token-overlap fraction in keyword mode. The two are NOT
	// comparable across modes, which is why Mode travels with the results.
	Score  float64
	Origin string
	Mode   string
}

// Searcher ranks memories against a query. A nil Embedder is valid and
// selects keyword mode.
type Searcher struct {
	dir      string
	embedder llm.Embedder
}

// NewSearcher binds a searcher to a memory dir. embedder may be nil.
func NewSearcher(dir string, embedder llm.Embedder) *Searcher {
	return &Searcher{dir: dir, embedder: embedder}
}

// HasEmbedder reports whether semantic ranking is available.
func (s *Searcher) HasEmbedder() bool { return s != nil && s.embedder != nil }

// SyncResult reports what one index refresh did.
type SyncResult struct {
	Embedded int // rows (re)embedded this pass
	Dropped  int // rows removed because their file is gone
	Total    int // rows in the index afterwards
}

// Sync brings the sidecar index in line with the store: it embeds new and
// changed memories, drops rows for deleted ones, and saves.
//
// Returns a zero result and no error when there is no embedder — "nothing
// to do" is the correct outcome, not a failure. An embedding call that
// fails leaves the existing index untouched and returns the error for the
// caller to log; the next search still works in keyword mode.
func (s *Searcher) Sync(ctx context.Context) (SyncResult, error) {
	if !s.HasEmbedder() || s.dir == "" {
		return SyncResult{}, nil
	}
	headers := memdir.ScanMemoryFiles(s.dir)
	texts := s.readTexts(headers)

	ix := memdir.LoadEmbedIndex(s.dir)
	dropped := ix.Prune(headers)

	model := s.embedder.EmbedModel()
	stale := ix.StaleNames(headers, texts, model)
	if len(stale) == 0 {
		if dropped > 0 {
			_ = ix.Save(s.dir)
		}
		return SyncResult{Dropped: dropped, Total: ix.Len()}, nil
	}

	inputs := make([]string, len(stale))
	for i, name := range stale {
		inputs[i] = texts[name]
	}
	vecs, err := s.embedder.Embed(ctx, inputs)
	if err != nil {
		return SyncResult{Dropped: dropped, Total: ix.Len()}, err
	}
	// Embed's contract is one vector per input in input order; a backend
	// that breaks it would silently attach vectors to the wrong memories,
	// so refuse rather than write a corrupted cache.
	if len(vecs) != len(stale) {
		return SyncResult{Dropped: dropped, Total: ix.Len()}, errVectorCountMismatch
	}

	origins := s.readOrigins(headers)
	for i, name := range stale {
		ix.Put(memdir.EmbedRow{
			Name:   name,
			Hash:   memdir.HashText(texts[name]),
			Model:  model,
			Dims:   len(vecs[i]),
			Origin: origins[name],
			Vec:    vecs[i],
		})
	}
	if err := ix.Save(s.dir); err != nil {
		return SyncResult{Embedded: len(stale), Dropped: dropped, Total: ix.Len()}, err
	}
	return SyncResult{Embedded: len(stale), Dropped: dropped, Total: ix.Len()}, nil
}

// Search returns the top-k memories for query, best first.
//
// Semantic mode requires both an embedder and a non-empty index; anything
// else falls through to keyword mode. minScore drops weak matches — silence
// beats noise, since every returned row costs context and invites the model
// to act on something irrelevant.
func (s *Searcher) Search(ctx context.Context, query string, k int, minScore float64) ([]Hit, string) {
	if s == nil || s.dir == "" || strings.TrimSpace(query) == "" {
		return nil, ModeKeyword
	}
	if k <= 0 {
		k = 5
	}
	headers := memdir.ScanMemoryFiles(s.dir)
	if len(headers) == 0 {
		return nil, ModeKeyword
	}

	if hits, ok := s.semanticSearch(ctx, query, headers, k, minScore); ok {
		return hits, ModeSemantic
	}
	return s.keywordSearch(query, headers, k), ModeKeyword
}

// Narrow implements Prefilter: it returns the `keep` headers closest to
// query by embedding similarity, preserving the input's newest-first order
// among survivors.
//
// Order is preserved rather than re-sorted by score because the selector
// downstream reads the manifest as a list and its prompt says nothing about
// ranking — handing it a score-ordered list would silently bias it toward
// the first entries. This step decides what the selector MAY consider, not
// what it should prefer.
//
// Returns nil on any failure (no embedder, empty index, embedding error),
// which the caller reads as "no narrowing" and falls back to the full
// manifest. Failing open matters here: failing closed would silently shrink
// the model's memory.
func (s *Searcher) Narrow(ctx context.Context, query string, headers []memdir.MemoryHeader, keep int) []memdir.MemoryHeader {
	if !s.HasEmbedder() || keep <= 0 || len(headers) <= keep {
		return nil
	}
	ix := memdir.LoadEmbedIndex(s.dir)
	if ix.Len() == 0 {
		return nil
	}
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil
	}
	qv := vecs[0]
	model := s.embedder.EmbedModel()

	type scored struct {
		name  string
		score float64
	}
	var ranked []scored
	for _, row := range ix.Rows() {
		if row.Model != model || len(row.Vec) != len(qv) {
			continue
		}
		ranked = append(ranked, scored{row.Name, llm.CosineSimilarity(qv, row.Vec)})
	}
	// An index that covers only part of the store would drop the uncovered
	// memories entirely — worse than not narrowing at all, because the loss
	// is invisible. Bail out until the index has caught up.
	if len(ranked) < len(headers) {
		return nil
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	survivors := make(map[string]bool, keep)
	for i := 0; i < keep && i < len(ranked); i++ {
		survivors[ranked[i].name] = true
	}
	out := make([]memdir.MemoryHeader, 0, keep)
	for _, h := range headers {
		if survivors[h.Filename] {
			out = append(out, h)
		}
	}
	return out
}

// semanticSearch ranks by cosine against the cached vectors. Reports ok=false
// when semantic ranking is unavailable for ANY reason — no embedder, an empty
// index, a failed query embedding — so the caller falls back rather than
// returning an empty result that reads as "no memories match".
func (s *Searcher) semanticSearch(ctx context.Context, query string, headers []memdir.MemoryHeader, k int, minScore float64) ([]Hit, bool) {
	if !s.HasEmbedder() {
		return nil, false
	}
	ix := memdir.LoadEmbedIndex(s.dir)
	if ix.Len() == 0 {
		return nil, false
	}
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, false
	}
	qv := vecs[0]
	model := s.embedder.EmbedModel()

	byName := make(map[string]memdir.MemoryHeader, len(headers))
	for _, h := range headers {
		byName[h.Filename] = h
	}

	var hits []Hit
	for _, row := range ix.Rows() {
		// A row from another model is not comparable with this query's
		// vector. Skipping it (rather than scoring it) means a half-migrated
		// index degrades gracefully instead of ranking on noise.
		if row.Model != model || len(row.Vec) != len(qv) {
			continue
		}
		h, ok := byName[row.Name]
		if !ok {
			continue // indexed but deleted since the last Sync
		}
		score := llm.CosineSimilarity(qv, row.Vec)
		if score < minScore {
			continue
		}
		hits = append(hits, Hit{Header: h, Score: score, Origin: row.Origin, Mode: ModeSemantic})
	}
	if len(hits) == 0 {
		// Distinguish "the index is unusable" from "nothing scored well".
		// The latter is a real answer and must not trigger a keyword retry
		// that would surface the weak matches the threshold just rejected.
		return nil, ix.Len() > 0
	}
	sortHits(hits)
	return truncate(hits, k), true
}

// keywordSearch is the no-embedder path: token overlap over the name,
// description and body. Crude, but it needs no setup and it is honest —
// results are labeled ModeKeyword so nobody mistakes them for semantic ones.
func (s *Searcher) keywordSearch(query string, headers []memdir.MemoryHeader, k int) []Hit {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	var hits []Hit
	for _, h := range headers {
		hay := strings.ToLower(h.Filename + " " + h.Description + " " + readBody(h.Path))
		matched := 0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		hits = append(hits, Hit{
			Header: h,
			Score:  float64(matched) / float64(len(terms)),
			Mode:   ModeKeyword,
		})
	}
	sortHits(hits)
	return truncate(hits, k)
}

// readTexts builds the embed text for every header, keyed by filename.
// Unreadable files are omitted — StaleNames skips what it cannot see.
func (s *Searcher) readTexts(headers []memdir.MemoryHeader) map[string]string {
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		raw, err := os.ReadFile(h.Path)
		if err != nil {
			continue
		}
		_, body := memdir.ParseFrontmatter(string(raw))
		out[h.Filename] = memdir.EmbedText(h.Filename, h.Description, body)
	}
	return out
}

// readOrigins pulls each memory's provenance from frontmatter.
func (s *Searcher) readOrigins(headers []memdir.MemoryHeader) map[string]string {
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		raw, err := os.ReadFile(h.Path)
		if err != nil {
			continue
		}
		fm, _ := memdir.ParseFrontmatter(string(raw))
		if o := fm[memdir.OriginKey]; o != "" {
			out[h.Filename] = o
		}
	}
	return out
}

func readBody(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, body := memdir.ParseFrontmatter(string(raw))
	return body
}

// sortHits orders by score descending, then filename, so equal scores
// produce a stable order across runs.
func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Header.Filename < hits[j].Header.Filename
	})
}

func truncate(hits []Hit, k int) []Hit {
	if k > 0 && len(hits) > k {
		return hits[:k]
	}
	return hits
}

// stopwords are the words a natural-language query is made of rather than
// about. Without this list, "how does the deploy pipeline work" scores a
// hit on any memory containing "how" or "the" — which is nearly all of
// them — and the resulting ranking is dominated by document length rather
// than relevance. A length filter alone does not catch these: every word
// here is three characters or more.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "was": true, "were": true,
	"how": true, "what": true, "when": true, "where": true, "why": true, "who": true,
	"does": true, "did": true, "can": true, "should": true, "would": true, "could": true,
	"this": true, "that": true, "these": true, "those": true, "there": true, "here": true,
	"with": true, "from": true, "into": true, "about": true, "have": true, "has": true,
	"had": true, "not": true, "you": true, "your": true, "our": true, "any": true,
	"all": true, "use": true, "using": true, "used": true, "need": true, "want": true,
	"get": true, "got": true, "its": true, "will": true, "just": true,
	"but": true, "out": true, "own": true, "off": true, "via": true, "per": true,
}

// tokenize lowercases, splits on non-alphanumerics, and drops both very
// short tokens and stopwords, so a score reflects topical overlap rather
// than shared grammar.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	seen := map[string]bool{}
	for _, f := range fields {
		if len(f) < 3 || seen[f] || stopwords[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}
