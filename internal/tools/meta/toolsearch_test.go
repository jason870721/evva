package meta

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

// ---- test fixtures ----

func testDescriptors() []tools.Descriptor {
	return []tools.Descriptor{
		{Name: "notebook_edit", Tags: []string{"notebook", "jupyter", "ipynb", "cell", "edit"}},
		{Name: "web_search", Tags: []string{"web", "search", "google", "internet", "lookup"}},
		{Name: "web_fetch", Tags: []string{"http", "url", "web", "fetch", "scrape"}},
		{Name: "task_create", Tags: []string{"task", "todo", "create", "track", "plan"}},
		{Name: "task_list", Tags: []string{"task", "todo", "list", "all", "overview"}},
		{Name: "task_update", Tags: []string{"task", "todo", "update", "status"}},
		{Name: "calc", Tags: []string{"math", "calculate", "sum", "product"}},
		{Name: "json_query", Tags: []string{"json", "query", "filter", "extract"}},
		{Name: "monitor", Tags: []string{"watch", "tail", "follow", "stream"}},
		{Name: "cron_create", Tags: []string{"schedule", "cron", "recurring", "timer"}},
	}
}

func eq(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ---- select: form ----

func TestSelectByName_ExactMatch(t *testing.T) {
	got := selectByName("notebook_edit", testDescriptors(), 5)
	if !eq(got, []string{"notebook_edit"}) {
		t.Fatalf("expected [notebook_edit], got %v", got)
	}
}

func TestSelectByName_CaseInsensitive(t *testing.T) {
	got := selectByName("NOTEBOOK_EDIT", testDescriptors(), 5)
	if !eq(got, []string{"notebook_edit"}) {
		t.Fatalf("expected [notebook_edit], got %v", got)
	}
}

func TestSelectByName_MultipleAndUnknown(t *testing.T) {
	got := selectByName("notebook_edit, nonexistent , calc", testDescriptors(), 10)
	want := []string{"notebook_edit", "calc"}
	if !eq(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSelectByName_RespectsMaxResults(t *testing.T) {
	got := selectByName("notebook_edit, web_search, web_fetch, task_create", testDescriptors(), 2)
	want := []string{"notebook_edit", "web_search"}
	if !eq(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSelectByName_EmptyList(t *testing.T) {
	got := selectByName("", testDescriptors(), 5)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

// ---- keyword search ----

func TestSearch_ExactTagMatch(t *testing.T) {
	got := searchDescriptors("notebook", 5, testDescriptors())
	if len(got) == 0 || got[0] != "notebook_edit" {
		t.Fatalf("expected notebook_edit top via exact tag, got %v", got)
	}
}

func TestSearch_TagSubstring(t *testing.T) {
	got := searchDescriptors("calcu", 5, testDescriptors())
	// "calcu" is substring of tag "calculate" on calc -> score 2.
	if len(got) == 0 || got[0] != "calc" {
		t.Fatalf("expected calc top via substring, got %v", got)
	}
}

func TestSearch_NameSubstring(t *testing.T) {
	got := searchDescriptors("task", 5, testDescriptors())
	if len(got) < 3 {
		t.Fatalf("expected >=3 task tools, got %v", got)
	}
	for _, name := range got {
		if !strings.HasPrefix(name, "task_") {
			t.Fatalf("expected only task_* tools, got %s", name)
		}
	}
}

func TestSearch_FuzzyTypo(t *testing.T) {
	got := searchDescriptors("noteboook", 5, testDescriptors())
	// "noteboook" (extra 'o') has levenshtein=1 from tag "notebook" -> +2.
	if len(got) == 0 || got[0] != "notebook_edit" {
		t.Fatalf("expected notebook_edit via typo, got %v", got)
	}
}

func TestSearch_FuzzySubsequence(t *testing.T) {
	got := searchDescriptors("jpyter", 5, testDescriptors())
	if len(got) == 0 || got[0] != "notebook_edit" {
		t.Fatalf("expected notebook_edit via subsequence, got %v", got)
	}
}

func TestSearch_NoMatch(t *testing.T) {
	got := searchDescriptors("zzzznonexistent", 5, testDescriptors())
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

// ---- required (+term) filtering ----

func TestSearch_RequiredTermFilters(t *testing.T) {
	got := searchDescriptors("+web search", 5, testDescriptors())
	if len(got) == 0 {
		t.Fatal("expected at least web_search")
	}
	for _, name := range got {
		if !strings.Contains(name, "web") {
			t.Fatalf("expected only web_* tools, got %s", name)
		}
	}
}

func TestSearch_RequiredTermTypo(t *testing.T) {
	got := searchDescriptors("+ntebook", 5, testDescriptors())
	if len(got) == 0 || got[0] != "notebook_edit" {
		t.Fatalf("expected notebook_edit via fuzzy required, got %v", got)
	}
}

func TestSearch_OnlyRequiredTerms(t *testing.T) {
	got := searchDescriptors("+web +search", 5, testDescriptors())
	if len(got) == 0 || got[0] != "web_search" {
		t.Fatalf("expected web_search, got %v", got)
	}
}

func TestSearch_RequiredTermNoMatch(t *testing.T) {
	got := searchDescriptors("+nonexistent web", 5, testDescriptors())
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

// ---- max_results cap & ranking ----

func TestSearch_RespectsMaxResults(t *testing.T) {
	got := searchDescriptors("task", 2, testDescriptors())
	if len(got) != 2 {
		t.Fatalf("expected cap at 2, got %d: %v", len(got), got)
	}
}

func TestSearch_RankingByScore(t *testing.T) {
	descs := []tools.Descriptor{
		{Name: "a", Tags: []string{"exactmatch"}},
		{Name: "b", Tags: []string{"exactmatcx"}}, // typo
		{Name: "c", Tags: []string{"other"}},      // no hit
	}
	got := searchDescriptors("exactmatch", 3, descs)
	if len(got) < 2 {
		t.Fatalf("expected >=2, got %v", got)
	}
	if got[0] != "a" {
		t.Fatalf("expected 'a' top (exact tag +4), got %s", got[0])
	}
	if got[1] != "b" {
		t.Fatalf("expected 'b' second (typo +2), got %s", got[1])
	}
}

// ---- named-part scoring (Phase 2 port from ref TS) ----

func TestSearch_ExactNamePartBeatsDescriptionHit(t *testing.T) {
	// "edit" exactly matches notebook_edit's name part (+10) and is a
	// substring of task_update's description (only score +0 from name).
	descs := []tools.Descriptor{
		{Name: "notebook_edit"},
		{Name: "task_update", Description: "edit a task"},
	}
	got := searchDescriptors("edit", 5, descs)
	if len(got) == 0 || got[0] != "notebook_edit" {
		t.Fatalf("expected notebook_edit (exact name part), got %v", got)
	}
}

func TestSearch_SearchHintScoresHigherThanDescription(t *testing.T) {
	descs := []tools.Descriptor{
		{Name: "a", SearchHint: "fast shell command runner"},
		{Name: "b", Description: "fast shell command runner"},
	}
	got := searchDescriptors("shell", 5, descs)
	if len(got) < 2 {
		t.Fatalf("expected 2 results, got %v", got)
	}
	if got[0] != "a" {
		t.Fatalf("expected 'a' (hint +4) before 'b' (desc +2), got %v", got)
	}
}

func TestSearch_FullNameFallback(t *testing.T) {
	// "webfetch" doesn't match any single part of "web_fetch" but its full
	// joined name "web fetch" contains "web" — covered by part match.
	// Test the fallback: query "webfetch" (no part match, no desc match)
	// against "web_fetch" should... actually parts are ["web","fetch"], so
	// "webfetch" matches neither part. The full-name fallback ("web fetch"
	// contains "webfetch"?) is false too. So no match. Confirm no false
	// positive.
	descs := []tools.Descriptor{
		{Name: "web_fetch"},
	}
	got := searchDescriptors("webfetch", 5, descs)
	if len(got) != 0 {
		t.Fatalf("expected no match for 'webfetch' against 'web_fetch', got %v", got)
	}
}

func TestSearch_McpPrefixFastPath(t *testing.T) {
	descs := []tools.Descriptor{
		{Name: "mcp__notion__search"},
		{Name: "mcp__notion__create"},
		{Name: "mcp__github__list_repos"},
		{Name: "web_search"},
	}
	got := searchDescriptors("mcp__notion", 10, descs)
	if len(got) != 2 {
		t.Fatalf("expected 2 notion tools, got %v", got)
	}
	for _, name := range got {
		if !strings.HasPrefix(name, "mcp__notion") {
			t.Fatalf("expected only mcp__notion__* tools, got %s", name)
		}
	}
}

func TestSearch_BareNameFastPath(t *testing.T) {
	// Model types a bare tool name instead of "select:bash". The fast path
	// should return it directly without scoring.
	descs := testDescriptors()
	got := searchDescriptors("calc", 5, descs)
	if len(got) == 0 || got[0] != "calc" {
		t.Fatalf("expected calc top via bare-name fast path, got %v", got)
	}
}

// ---- ToolSearchTool.Execute integration ----

type fakeLookup struct {
	descs []tools.Descriptor
}

func (f *fakeLookup) DeferredNames() []tools.ToolName {
	out := make([]tools.ToolName, len(f.descs))
	for i, d := range f.descs {
		out[i] = tools.ToolName(d.Name)
	}
	return out
}

func (f *fakeLookup) Describe(name tools.ToolName) (tools.Descriptor, error) {
	for _, d := range f.descs {
		if string(name) == d.Name {
			return d, nil
		}
	}
	return tools.Descriptor{}, nil
}

func newToolSearchWith(descs []tools.Descriptor) *ToolSearchTool {
	return NewToolSearch(func() DeferredLookup {
		return &fakeLookup{descs: descs}
	})
}

func decodeSearchOutput(t *testing.T, body string) searchOutput {
	t.Helper()
	// The result body is: JSON line + "\n\n<functions>...</functions>".
	// Parse only the first line (the JSON envelope).
	jsonPart := body
	if idx := strings.Index(body, "\n\n<functions>"); idx >= 0 {
		jsonPart = body[:idx]
	}
	var out searchOutput
	if err := json.Unmarshal([]byte(jsonPart), &out); err != nil {
		t.Fatalf("decode output: %v\nbody: %s", err, body)
	}
	return out
}

func TestExecute_SelectReturnsCompactJSON(t *testing.T) {
	ts := newToolSearchWith(testDescriptors())
	input := json.RawMessage(`{"query":"select:calc,web_search"}`)
	res, err := ts.Execute(nil, tools.NopLogger(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := decodeSearchOutput(t, res.Content)
	if !eq(out.Matches, []string{"calc", "web_search"}) {
		t.Fatalf("expected matches [calc,web_search], got %v", out.Matches)
	}
	if out.Query != "select:calc,web_search" {
		t.Fatalf("unexpected query: %s", out.Query)
	}
	if out.TotalDeferredTools != len(testDescriptors()) {
		t.Fatalf("unexpected total: %d", out.TotalDeferredTools)
	}
}

func TestExecute_KeywordReturnsTopMatch(t *testing.T) {
	ts := newToolSearchWith(testDescriptors())
	input := json.RawMessage(`{"query":"notebook jupyter"}`)
	res, err := ts.Execute(nil, tools.NopLogger(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := decodeSearchOutput(t, res.Content)
	if len(out.Matches) == 0 || out.Matches[0] != "notebook_edit" {
		t.Fatalf("expected notebook_edit, got %v", out.Matches)
	}
}

func TestExecute_EmptyQueryError(t *testing.T) {
	ts := newToolSearchWith(testDescriptors())
	input := json.RawMessage(`{"query":"   "}`)
	res, _ := ts.Execute(nil, tools.NopLogger(), input)
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
	if !strings.Contains(res.Content, "query is required") {
		t.Fatalf("expected 'query is required', got: %s", res.Content)
	}
}

func TestExecute_NilLookupError(t *testing.T) {
	ts := NewToolSearch(nil)
	input := json.RawMessage(`{"query":"task"}`)
	res, _ := ts.Execute(nil, tools.NopLogger(), input)
	if !res.IsError {
		t.Fatal("expected error for nil lookup")
	}
}

func TestExecute_LookupReturnsNilError(t *testing.T) {
	ts := NewToolSearch(func() DeferredLookup { return nil })
	input := json.RawMessage(`{"query":"task"}`)
	res, _ := ts.Execute(nil, tools.NopLogger(), input)
	if !res.IsError {
		t.Fatal("expected error when lookup returns nil")
	}
}

func TestExecute_NoDeferredToolsReturnsEmptyMatches(t *testing.T) {
	ts := newToolSearchWith(nil)
	input := json.RawMessage(`{"query":"task"}`)
	res, _ := ts.Execute(nil, tools.NopLogger(), input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := decodeSearchOutput(t, res.Content)
	if len(out.Matches) != 0 {
		t.Fatalf("expected empty matches, got %v", out.Matches)
	}
	if out.TotalDeferredTools != 0 {
		t.Fatalf("expected total=0, got %d", out.TotalDeferredTools)
	}
}

func TestExecute_NoMatchReturnsEmptyMatches(t *testing.T) {
	ts := newToolSearchWith(testDescriptors())
	input := json.RawMessage(`{"query":"zzzznonexistent"}`)
	res, _ := ts.Execute(nil, tools.NopLogger(), input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := decodeSearchOutput(t, res.Content)
	if len(out.Matches) != 0 {
		t.Fatalf("expected empty matches, got %v", out.Matches)
	}
	if out.TotalDeferredTools != len(testDescriptors()) {
		t.Fatalf("expected total=%d, got %d", len(testDescriptors()), out.TotalDeferredTools)
	}
}
