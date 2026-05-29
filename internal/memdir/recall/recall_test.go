package recall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// fakeClient is a minimal llm.Client that returns a canned response (or error)
// and records the last user message it was sent, so tests can assert the
// manifest prompt shape without a live model.
type fakeClient struct {
	reply   string
	err     error
	lastMsg string
	calls   int
}

func (f *fakeClient) Name() string               { return "fake" }
func (f *fakeClient) Model() string              { return "fake-model" }
func (f *fakeClient) SupportsDeferLoading() bool { return false }
func (f *fakeClient) Apply(...llm.Option)        {}
func (f *fakeClient) Stream(ctx context.Context, m []llm.Message, t []tools.Tool, s llm.ChunkSink) (llm.Response, error) {
	return f.Complete(ctx, m, t)
}

func (f *fakeClient) Complete(_ context.Context, msgs []llm.Message, _ []tools.Tool) (llm.Response, error) {
	f.calls++
	if len(msgs) > 0 {
		f.lastMsg = msgs[len(msgs)-1].Content
	}
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return llm.Response{Content: f.reply}, nil
}

func writeMemory(t *testing.T, dir, rel, typ, desc, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + strings.TrimSuffix(rel, ".md") +
		"\ndescription: " + desc + "\ntype: " + typ + "\n---\n\n" + body
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindRelevant_FiltersToValidAndExcludesHallucinations(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "feedback", "no db mocks", "body a")
	writeMemory(t, dir, "b.md", "user", "user is a Go dev", "body b")
	// MEMORY.md must never be a candidate even if the model names it.
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- index"), 0o644); err != nil {
		t.Fatal(err)
	}

	fc := &fakeClient{reply: `{"selected_memories": ["a.md", "ghost.md", "MEMORY.md"]}`}
	got := FindRelevant(context.Background(), fc, constant.SONNET_4_6, "add a test", dir, nil, nil)

	if len(got) != 1 || got[0].Filename != "a.md" {
		t.Fatalf("want exactly [a.md], got %+v", got)
	}
}

func TestFindRelevant_ManifestShapeAndRecentTools(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "feedback", "integration tests hit a real db", "body")

	fc := &fakeClient{reply: `{"selected_memories": []}`}
	FindRelevant(context.Background(), fc, constant.SONNET_4_6, "fix the migration", dir, []string{"bash", "edit"}, nil)

	for _, want := range []string{
		"Query: fix the migration",
		"Available memories:",
		"[feedback] a.md",
		"integration tests hit a real db",
		"Recently used tools: bash, edit",
	} {
		if !strings.Contains(fc.lastMsg, want) {
			t.Errorf("manifest message missing %q\n--- got ---\n%s", want, fc.lastMsg)
		}
	}
}

func TestFindRelevant_ClientErrorReturnsNil(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "user", "x", "body")
	fc := &fakeClient{err: errors.New("boom")}
	if got := FindRelevant(context.Background(), fc, constant.SONNET_4_6, "q", dir, nil, nil); got != nil {
		t.Fatalf("client error should yield nil, got %+v", got)
	}
}

func TestFindRelevant_EmptyDirSkipsSideQuery(t *testing.T) {
	dir := t.TempDir() // no .md files
	fc := &fakeClient{reply: `{"selected_memories": ["a.md"]}`}
	if got := FindRelevant(context.Background(), fc, constant.SONNET_4_6, "q", dir, nil, nil); got != nil {
		t.Fatalf("empty dir should yield nil, got %+v", got)
	}
	if fc.calls != 0 {
		t.Errorf("empty dir must not spend a side-query; calls=%d", fc.calls)
	}
}

func TestFindRelevant_AlreadySurfacedExcludedBeforeSelection(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "feedback", "already shown", "body a")
	writeMemory(t, dir, "b.md", "user", "fresh one", "body b")

	fc := &fakeClient{reply: `{"selected_memories": ["a.md", "b.md"]}`}
	got := FindRelevant(context.Background(), fc, constant.SONNET_4_6, "q", dir,
		nil, map[string]bool{"a.md": true})

	// a.md was filtered before the side-query (so it isn't even in the manifest)
	// and can't be returned; only b.md survives.
	if strings.Contains(fc.lastMsg, "a.md") {
		t.Errorf("alreadySurfaced file should not appear in manifest:\n%s", fc.lastMsg)
	}
	if len(got) != 1 || got[0].Filename != "b.md" {
		t.Fatalf("want [b.md], got %+v", got)
	}
}

func TestFindRelevant_NilClientOrDir(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "user", "x", "body")
	if got := FindRelevant(context.Background(), nil, constant.SONNET_4_6, "q", dir, nil, nil); got != nil {
		t.Errorf("nil client should yield nil, got %+v", got)
	}
	fc := &fakeClient{reply: `{"selected_memories":["a.md"]}`}
	if got := FindRelevant(context.Background(), fc, constant.SONNET_4_6, "q", "", nil, nil); got != nil {
		t.Errorf("empty dir should yield nil, got %+v", got)
	}
}

func TestFindRelevant_ToleratesFencedJSON(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "user", "x", "body")
	fc := &fakeClient{reply: "Sure!\n```json\n{\"selected_memories\": [\"a.md\"]}\n```"}
	got := FindRelevant(context.Background(), fc, constant.SONNET_4_6, "q", dir, nil, nil)
	if len(got) != 1 || got[0].Filename != "a.md" {
		t.Fatalf("should parse fenced JSON, got %+v", got)
	}
}
