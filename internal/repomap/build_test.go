package repomap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools/lsp"
)

// fakeSource is a hand-written Source for builder tests — no real LSP server.
type fakeSource struct {
	servers []string
	ws      []lsp.Symbol
	wsErr   error
	docs    map[string][]lsp.Symbol
}

func (f *fakeSource) Servers() []string { return f.servers }
func (f *fakeSource) WorkspaceSymbols(_ context.Context, _ string) ([]lsp.Symbol, error) {
	return f.ws, f.wsErr
}
func (f *fakeSource) DocumentSymbols(_ context.Context, p string) ([]lsp.Symbol, error) {
	return f.docs[p], nil
}

func sampleSource() *fakeSource {
	return &fakeSource{
		servers: []string{"gopls"},
		ws: []lsp.Symbol{
			{Name: "New", Kind: "Function", File: "/repo/pkg/foo/foo.go", Line: 10},
			{Name: "Foo", Kind: "Struct", File: "/repo/pkg/foo/foo.go", Line: 5},
			{Name: "Do", Kind: "Method", File: "/repo/pkg/foo/foo.go", Line: 20},
			{Name: "scratch", Kind: "Variable", File: "/repo/pkg/foo/foo.go", Line: 3},
			{Name: "Barer", Kind: "Interface", File: "/repo/pkg/bar/bar.go", Line: 8},
			{Name: "Helper", Kind: "Function", File: "/repo/pkg/bar/bar.go", Line: 12},
			{Name: "Outside", Kind: "Function", File: "/usr/lib/go/x.go", Line: 1},
		},
		docs: map[string][]lsp.Symbol{
			"/repo/pkg/foo/foo.go": {
				{Name: "Foo", Kind: "Struct", Detail: "struct{...}", File: "/repo/pkg/foo/foo.go", Line: 5},
				{Name: "New", Kind: "Function", Detail: "func() *Foo", File: "/repo/pkg/foo/foo.go", Line: 10},
				{Name: "Do", Kind: "Method", Container: "Foo", Detail: "func() error", File: "/repo/pkg/foo/foo.go", Line: 20},
			},
		},
	}
}

func TestBuild_GroupsFiltersAndRanks(t *testing.T) {
	out, err := Build(context.Background(), sampleSource(), "/repo", 4000)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Outside the repo root → filtered.
	if strings.Contains(out, "Outside") {
		t.Errorf("symbol outside root leaked into map:\n%s", out)
	}
	// Variable kind → dropped as noise.
	if strings.Contains(out, "scratch") {
		t.Errorf("Variable kind not filtered:\n%s", out)
	}
	// Both packages present, rendered relative to root.
	for _, want := range []string{"pkg/foo", "pkg/bar", "Foo", "Barer", "Helper"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in map:\n%s", want, out)
		}
	}
	// Ranking within pkg/foo: Struct before Function before Method.
	posFoo := strings.Index(out, "Struct Foo")
	posNew := strings.Index(out, "Function New")
	posDo := strings.Index(out, "Method Do")
	if !(posFoo >= 0 && posFoo < posNew && posNew < posDo) {
		t.Errorf("ranking wrong: Foo=%d New=%d Do=%d\n%s", posFoo, posNew, posDo, out)
	}
	// Bigger package (foo: 3 syms) ordered before smaller (bar: 2 syms).
	if strings.Index(out, "pkg/foo") > strings.Index(out, "pkg/bar") {
		t.Errorf("expected pkg/foo (richer) before pkg/bar:\n%s", out)
	}
}

func TestBuild_SignatureEnrichment(t *testing.T) {
	out, err := Build(context.Background(), sampleSource(), "/repo", 4000)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// document_symbols supplied a signature for New — it should be folded in.
	if !strings.Contains(out, "func() *Foo") {
		t.Errorf("expected enriched signature 'func() *Foo':\n%s", out)
	}
}

func TestBuild_BudgetTruncatesAtSymbolBoundary(t *testing.T) {
	out, err := Build(context.Background(), sampleSource(), "/repo", 12) // ~48 chars
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "… +") {
		t.Errorf("tiny budget should produce a truncation marker:\n%s", out)
	}
	// Never cut mid-line: every body line is whole (ends in newline).
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output should end on a symbol boundary:\n%s", out)
	}
}

func TestBuild_NoServerFallsBack(t *testing.T) {
	src := &fakeSource{servers: nil}
	if _, err := Build(context.Background(), src, "/repo", 4000); err == nil {
		t.Fatal("want error when no server, got nil")
	}
}

func TestBuildPath_OverviewVsFull(t *testing.T) {
	src := sampleSource()

	overview, err := BuildPath(context.Background(), src, "/repo", "pkg/foo", "overview", 4000)
	if err != nil {
		t.Fatalf("BuildPath overview: %v", err)
	}
	if strings.Contains(overview, "Do") {
		t.Errorf("overview should omit members (Do):\n%s", overview)
	}
	if !strings.Contains(overview, "Foo") {
		t.Errorf("overview should include top-level Foo:\n%s", overview)
	}

	full, err := BuildPath(context.Background(), src, "/repo", "pkg/foo", "full", 4000)
	if err != nil {
		t.Fatalf("BuildPath full: %v", err)
	}
	if !strings.Contains(full, "Foo.Do") {
		t.Errorf("full detail should include member Foo.Do:\n%s", full)
	}
}

func TestBuildFallback(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pkg", "svc", "svc.go"),
		"package svc\n\ntype Server struct{}\n\nfunc Exported() {}\n\nfunc unexported() {}\n")
	mustWrite(t, filepath.Join(root, "vendor", "dep", "dep.go"),
		"package dep\n\nfunc ShouldBeSkipped() {}\n")

	out, err := BuildFallback(context.Background(), root, 4000)
	if err != nil {
		t.Fatalf("BuildFallback: %v", err)
	}
	if !strings.Contains(out, "No language server") {
		t.Errorf("fallback must say it's a coarse outline:\n%s", out)
	}
	for _, want := range []string{"pkg/svc", "Server", "Exported"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in fallback:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unexported") {
		t.Errorf("unexported decl should not appear:\n%s", out)
	}
	if strings.Contains(out, "ShouldBeSkipped") {
		t.Errorf("vendor/ should be skipped:\n%s", out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
