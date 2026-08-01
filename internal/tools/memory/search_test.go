package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/memdir/recall"
	"github.com/johnny1110/evva/pkg/tools"
)

type stubProvider struct {
	hits     []recall.Hit
	mode     string
	embedder bool
	gotK     int
	gotQuery string
}

func (s *stubProvider) HasEmbedder() bool { return s.embedder }
func (s *stubProvider) Search(_ context.Context, query string, k int, _ float64) ([]recall.Hit, string) {
	s.gotQuery, s.gotK = query, k
	return s.hits, s.mode
}

func hit(name, desc, origin string, score float64) recall.Hit {
	return recall.Hit{
		Header: memdir.MemoryHeader{
			Filename:    name,
			Path:        "/mem/" + name,
			Description: desc,
			Type:        memdir.MemoryType("project"),
			ModTime:     time.Now(),
		},
		Score:  score,
		Origin: origin,
	}
}

func newTool(p Provider, origin string) *SearchTool {
	return NewSearch(func() Provider { return p }, func() string { return origin })
}

func run(t *testing.T, tool *SearchTool, input string) tools.Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("Execute returned a Go error (it should report through Result): %v", err)
	}
	return res
}

func TestSchemaIsValidJSON(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(NewSearch(nil, nil).Schema(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props := schema["properties"].(map[string]any)
	for _, k := range []string{"query", "limit", "scope"} {
		if _, ok := props[k]; !ok {
			t.Errorf("schema missing property %q", k)
		}
	}
	req := schema["required"].([]any)
	if len(req) != 1 || req[0] != "query" {
		t.Errorf("query should be the only required field, got %v", req)
	}
}

func TestNameMatchesTheRegisteredConstant(t *testing.T) {
	if got := NewSearch(nil, nil).Name(); got != string(tools.MEMORY_SEARCH) {
		t.Errorf("Name: got %q, want %q", got, tools.MEMORY_SEARCH)
	}
	if len(Names()) != 1 || Names()[0] != tools.MEMORY_SEARCH {
		t.Errorf("Names: got %v", Names())
	}
}

func TestRendersHitsWithScoreAndPath(t *testing.T) {
	p := &stubProvider{
		hits:     []recall.Hit{hit("ci.md", "how releases work", "", 0.82)},
		mode:     recall.ModeSemantic,
		embedder: true,
	}
	res := run(t, newTool(p, ""), `{"query":"deploys"}`)

	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	for _, want := range []string{"ci.md", "0.82", "how releases work", "/mem/ci.md", "semantic"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("output missing %q:\n%s", want, res.Content)
		}
	}
}

// The model must be told when it is looking at literal matches, or it will
// read "no results" as a fact about the store rather than a limit of the
// search it just ran.
func TestKeywordModeIsDisclosed(t *testing.T) {
	p := &stubProvider{
		hits:     []recall.Hit{hit("a.md", "d", "", 0.5)},
		mode:     recall.ModeKeyword,
		embedder: false,
	}
	res := run(t, newTool(p, ""), `{"query":"x"}`)
	if !strings.Contains(res.Content, "No embedding backend configured") {
		t.Errorf("keyword mode not disclosed:\n%s", res.Content)
	}

	empty := &stubProvider{mode: recall.ModeKeyword, embedder: false}
	res = run(t, newTool(empty, ""), `{"query":"x"}`)
	if !strings.Contains(res.Content, "keyword search") {
		t.Errorf("empty keyword result should explain the limitation:\n%s", res.Content)
	}
}

// With an embedder present, an empty result is a real answer and must not
// be hedged as a tooling limitation.
func TestEmptySemanticResultIsStatedPlainly(t *testing.T) {
	p := &stubProvider{mode: recall.ModeSemantic, embedder: true}
	res := run(t, newTool(p, ""), `{"query":"x"}`)
	if strings.Contains(res.Content, "keyword") {
		t.Errorf("a semantic miss should not mention keyword fallback:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "No memories matched") {
		t.Errorf("unexpected empty message:\n%s", res.Content)
	}
}

func TestLimitDefaultsAndClamps(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{`{"query":"x"}`, defaultLimit},
		{`{"query":"x","limit":0}`, defaultLimit},
		{`{"query":"x","limit":-3}`, defaultLimit},
		{`{"query":"x","limit":3}`, 3},
		{`{"query":"x","limit":9999}`, maxLimit},
	}
	for _, tc := range cases {
		p := &stubProvider{mode: recall.ModeKeyword}
		run(t, newTool(p, ""), tc.input)
		if p.gotK != tc.want {
			t.Errorf("%s → limit %d, want %d", tc.input, p.gotK, tc.want)
		}
	}
}

func TestScopeProjectFiltersByOrigin(t *testing.T) {
	p := &stubProvider{
		hits: []recall.Hit{
			hit("mine.md", "d", "-Users-me-projA", 0.9),
			hit("theirs.md", "d", "-Users-me-projB", 0.8),
			hit("unlabelled.md", "d", "", 0.7),
		},
		mode:     recall.ModeSemantic,
		embedder: true,
	}
	res := run(t, newTool(p, "-Users-me-projA"), `{"query":"x","scope":"project"}`)

	if !strings.Contains(res.Content, "mine.md") {
		t.Error("this project's memory was filtered out")
	}
	if strings.Contains(res.Content, "theirs.md") {
		t.Error("another project's memory survived scope:project")
	}
	// An unattributed memory is exactly what scope:project exists to
	// exclude — keeping it would defeat the point of asking.
	if strings.Contains(res.Content, "unlabelled.md") {
		t.Error("a memory with no origin survived scope:project")
	}
}

func TestScopeAllIsTheDefault(t *testing.T) {
	p := &stubProvider{
		hits:     []recall.Hit{hit("a.md", "d", "-other", 0.9)},
		mode:     recall.ModeSemantic,
		embedder: true,
	}
	if res := run(t, newTool(p, "-mine"), `{"query":"x"}`); !strings.Contains(res.Content, "a.md") {
		t.Error("default scope should not filter by origin")
	}
}

func TestOriginIsLabelledInOutput(t *testing.T) {
	p := &stubProvider{
		hits:     []recall.Hit{hit("a.md", "d", "-Users-me-projB", 0.9)},
		mode:     recall.ModeSemantic,
		embedder: true,
	}
	res := run(t, newTool(p, "-Users-me-projA"), `{"query":"x"}`)
	if !strings.Contains(res.Content, "from -Users-me-projB") {
		t.Errorf("a cross-project hit should be labelled with its origin:\n%s", res.Content)
	}
}

func TestRejectsBadInput(t *testing.T) {
	p := &stubProvider{mode: recall.ModeKeyword}
	if res := run(t, newTool(p, ""), `{not json`); !res.IsError {
		t.Error("malformed JSON should produce an error result")
	}
	if res := run(t, newTool(p, ""), `{"query":"   "}`); !res.IsError {
		t.Error("a blank query should produce an error result")
	}
}

// A subagent, or a session with auto-memory off, has no provider. That is
// not a fault — the tool says so and returns cleanly.
func TestUnavailableProviderIsNotAnError(t *testing.T) {
	for _, tool := range []*SearchTool{
		NewSearch(nil, nil),
		NewSearch(func() Provider { return nil }, nil),
	} {
		res := run(t, tool, `{"query":"x"}`)
		if res.IsError {
			t.Error("an absent provider should not be reported as a tool error")
		}
		if !strings.Contains(res.Content, "not available") {
			t.Errorf("unexpected message: %s", res.Content)
		}
	}
}

// scope:"project" with no resolvable origin must return nothing rather
// than silently degrading to an unfiltered search.
func TestScopeProjectWithUnknownOriginReturnsNothing(t *testing.T) {
	p := &stubProvider{
		hits:     []recall.Hit{hit("a.md", "d", "-somewhere", 0.9)},
		mode:     recall.ModeSemantic,
		embedder: true,
	}
	res := run(t, newTool(p, ""), `{"query":"x","scope":"project"}`)
	if strings.Contains(res.Content, "a.md") {
		t.Error("scope:project with no known origin should not fall back to an unfiltered search")
	}
}

func TestQueryIsPassedThroughVerbatim(t *testing.T) {
	p := &stubProvider{mode: recall.ModeKeyword}
	run(t, newTool(p, ""), `{"query":"how do we deploy web2"}`)
	if p.gotQuery != "how do we deploy web2" {
		t.Errorf("query mangled: %q", p.gotQuery)
	}
}
