package repomap

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

func TestTool_Metadata(t *testing.T) {
	tool := NewTool(nil, t.TempDir(), 2000)
	if tool.Name() != string(tools.REPO_MAP) {
		t.Errorf("Name() = %q, want %q", tool.Name(), tools.REPO_MAP)
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}
}

// TestTool_FallsBackWithoutLSP exercises the tool's no-server path: with a nil
// manager it must degrade to the glob outline of the requested subtree rather
// than erroring (A5/A7).
func TestTool_FallsBackWithoutLSP(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pkg", "svc", "svc.go"),
		"package svc\n\ntype Server struct{}\n\nfunc Exported() {}\n")

	tool := NewTool(nil, root, 2000) // no LSP manager

	raw, _ := json.Marshal(repoMapInput{Path: "pkg/svc"})
	res, err := tool.Execute(context.Background(), slog.Default(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	for _, want := range []string{"No language server", "Server", "Exported"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in fallback zoom:\n%s", want, res.Content)
		}
	}
}

func TestTool_RejectsBadJSON(t *testing.T) {
	tool := NewTool(nil, t.TempDir(), 2000)
	res, err := tool.Execute(context.Background(), slog.Default(), json.RawMessage(`{bad`))
	if err != nil {
		t.Fatalf("Execute returned a hard error: %v", err)
	}
	if !res.IsError {
		t.Error("malformed JSON should yield an error result")
	}
}
