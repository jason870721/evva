//go:build integration

package lsp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp"
	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

func findModuleRoot() string {
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return ""
}

func TestIntegrationGopls(t *testing.T) {
	root := findModuleRoot()
	if root == "" {
		t.Skip("not in a Go module — run from evva project")
	}
	t.Logf("module root: %s", root)

	cfg := lsp.LspServerConfig{
		Command:        "gopls",
		Extensions:     map[string]string{".go": "go"},
		StartupTimeout: "120s",
		MaxRestarts:    2,
	}

	mgr := lsp.NewManager(map[string]lsp.LspServerConfig{"gopls": cfg}, "file://"+root, nil)
	defer mgr.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	testFile := filepath.Join(root, "pkg/tools/lsp/tool.go")
	uri := "file://" + filepath.ToSlash(testFile)

	srv, err := mgr.EnsureServerStarted(ctx, testFile)
	if err != nil {
		t.Fatalf("start gopls: %v", err)
	}
	if !srv.IsHealthy() {
		t.Fatal("server not healthy after start")
	}
	t.Logf("gopls started: definition=%v references=%v hover=%v symbols=%v",
		srv.Capabilities().DefinitionProvider,
		srv.Capabilities().ReferencesProvider,
		srv.Capabilities().HoverProvider,
		srv.Capabilities().DocumentSymbolProvider)

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if err := mgr.OpenFile(ctx, testFile, string(content)); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	defer mgr.CloseFile(ctx, testFile)

	// Give gopls time to finish initial workspace indexing.
	// gopls init responds quickly but package loading is async.
	t.Log("waiting 30s for gopls indexing...")
	time.Sleep(30 * time.Second)

	t.Run("definition", func(t *testing.T) {
		// Line 22 (1-indexed), character 20: `Manager` in `func NewTool(mgr *Manager, ...)`
		// Cross-file: → manager.go:19
		params := protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 21, Character: 19},
		}
		raw, err := srv.Request(ctx, protocol.MethodDefinition, params)
		if err != nil {
			t.Fatalf("definition: %v", err)
		}
		var locs []protocol.Location
		if err := json.Unmarshal(raw, &locs); err != nil {
			var loc protocol.Location
			if err := json.Unmarshal(raw, &loc); err != nil {
				t.Fatalf("unmarshal definition: %v (raw=%s)", err, string(raw)[:min(len(raw), 200)])
			}
			locs = []protocol.Location{loc}
		}
		if len(locs) == 0 || locs[0].URI == "" {
			t.Fatalf("expected non-empty definition, got raw=%s", string(raw)[:min(len(raw), 200)])
		}
		if !strings.Contains(locs[0].URI, "manager.go") {
			t.Errorf("expected definition in manager.go, got %s", locs[0].URI)
		}
		t.Logf("definition: %s:%d", locs[0].URI, locs[0].Range.Start.Line+1)
	})

	t.Run("references", func(t *testing.T) {
		// Same position as definition — find all usages of Manager.
		params := protocol.ReferenceParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri},
				Position:     protocol.Position{Line: 21, Character: 19},
			},
			Context: protocol.ReferenceContext{IncludeDeclaration: true},
		}
		raw, err := srv.Request(ctx, protocol.MethodReferences, params)
		if err != nil {
			t.Fatalf("references: %v", err)
		}
		var locs []protocol.Location
		if err := json.Unmarshal(raw, &locs); err != nil {
			t.Fatalf("unmarshal references: %v", err)
		}
		if len(locs) < 1 {
			t.Error("expected at least 1 reference")
		}
		t.Logf("references: %d locations", len(locs))
	})

	t.Run("hover", func(t *testing.T) {
		params := protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 21, Character: 19},
		}
		raw, err := srv.Request(ctx, protocol.MethodHover, params)
		if err != nil {
			t.Fatalf("hover: %v", err)
		}
		var hover protocol.Hover
		if err := json.Unmarshal(raw, &hover); err != nil {
			t.Fatalf("unmarshal hover: %v (raw=%s)", err, string(raw)[:min(len(raw), 200)])
		}
		if hover.Contents.Value == "" {
			t.Error("expected non-empty hover content")
		}
		t.Logf("hover: %s", hover.Contents.Value[:min(len(hover.Contents.Value), 80)])
	})

	t.Run("documentSymbols", func(t *testing.T) {
		params := protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		}
		raw, err := srv.Request(ctx, protocol.MethodDocumentSymbol, params)
		if err != nil {
			t.Fatalf("documentSymbols: %v", err)
		}
		var syms []protocol.DocumentSymbol
		if err := json.Unmarshal(raw, &syms); err != nil {
			t.Fatalf("unmarshal documentSymbols: %v", err)
		}
		if len(syms) == 0 {
			t.Error("expected at least 1 symbol")
		}
		t.Logf("documentSymbols: %d top-level symbols", len(syms))
	})

	t.Run("workspaceSymbol", func(t *testing.T) {
		params := protocol.WorkspaceSymbolParams{Query: "Manager"}
		raw, err := srv.Request(ctx, protocol.MethodWorkspaceSymbol, params)
		if err != nil {
			t.Fatalf("workspaceSymbol: %v", err)
		}
		var infos []protocol.SymbolInformation
		if err := json.Unmarshal(raw, &infos); err != nil {
			t.Fatalf("unmarshal workspaceSymbol: %v", err)
		}
		if len(infos) == 0 {
			t.Error("expected workspace symbols matching 'Manager'")
		}
		t.Logf("workspaceSymbol: %d results for 'Manager'", len(infos))
	})

	t.Run("callHierarchy", func(t *testing.T) {
		// Use manager.go for call hierarchy — drainDaemonSignals is called from the agent loop.
		managerFile := filepath.Join(root, "internal/agent/drain_daemons.go")
		managerURI := "file://" + filepath.ToSlash(managerFile)
		managerContent, err := os.ReadFile(managerFile)
		if err != nil {
			t.Fatalf("read drain_daemons.go: %v", err)
		}
		if err := mgr.OpenFile(ctx, managerFile, string(managerContent)); err != nil {
			t.Fatalf("didOpen drain_daemons.go: %v", err)
		}
		defer mgr.CloseFile(ctx, managerFile)

		// composeDaemonLifecycle function at line 124 (1-indexed)
		params := protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: managerURI},
			Position:     protocol.Position{Line: 123, Character: 5},
		}
		raw, err := srv.Request(ctx, protocol.MethodPrepareCallHierarchy, params)
		if err != nil {
			t.Fatalf("prepareCallHierarchy: %v", err)
		}
		var items []protocol.CallHierarchyItem
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("unmarshal callHierarchy: %v", err)
		}
		if len(items) == 0 {
			t.Fatal("expected at least 1 call hierarchy item")
		}
		t.Logf("callHierarchy: %d items", len(items))

		if len(items) > 0 {
			inParams := protocol.CallHierarchyIncomingCallsParams{Item: items[0]}
			raw, err = srv.Request(ctx, protocol.MethodIncomingCalls, inParams)
			if err != nil {
				t.Fatalf("incomingCalls: %v", err)
			}
			var inCalls []protocol.CallHierarchyIncomingCall
			if err := json.Unmarshal(raw, &inCalls); err == nil {
				t.Logf("incomingCalls: %d callers", len(inCalls))
			}

			outParams := protocol.CallHierarchyOutgoingCallsParams{Item: items[0]}
			raw, err = srv.Request(ctx, protocol.MethodOutgoingCalls, outParams)
			if err != nil {
				t.Fatalf("outgoingCalls: %v", err)
			}
			var outCalls []protocol.CallHierarchyOutgoingCall
			if err := json.Unmarshal(raw, &outCalls); err == nil {
				t.Logf("outgoingCalls: %d callees", len(outCalls))
			}
		}
	})

	t.Run("shutdown", func(t *testing.T) {
		if err := mgr.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if srv.IsHealthy() {
			t.Error("server still healthy after shutdown")
		}
	})
}

// TestIntegrationSelfHealingEdits exercises DidChange/DiagnosticsForFile
// (docs/roadmap/PRD/edit-diagnostics-sync.md) against a real gopls process,
// end to end: a syntax error introduced via DidChange must produce a real
// diagnostic, and fixing it must not leave a ghost behind. This is the
// closest a `go test` run gets to the PRD's own manual TTY verification step
// ("edit a .go file to introduce a type error → next turn carries an LSP
// diagnostic; fix it → the ghost error does not reappear").
func TestIntegrationSelfHealingEdits(t *testing.T) {
	root := findModuleRoot()
	if root == "" {
		t.Skip("not in a Go module — run from evva project")
	}

	// A throwaway file inside the lsp package's own directory so gopls loads
	// it as a real part of the module (not an orphan file outside any
	// package) without needing the full multi-minute workspace index a
	// cross-file definition/reference lookup would — a syntax error is
	// reported from a per-file parse, which is fast.
	smokePath := filepath.Join(root, "pkg/tools/lsp", "zzz_editsync_smoke_temp.go")
	// Deliberately just a package clause — any declaration (even an unused
	// var) risks tripping one of gopls's lint-style analyzers (unusedwrite,
	// staticcheck, ...), which would masquerade as "the fix didn't clear the
	// diagnostic" below. A bare package clause is diagnostic-free by
	// construction.
	const validContent = "package lsp\n"
	if err := os.WriteFile(smokePath, []byte(validContent), 0o644); err != nil {
		t.Fatalf("seed smoke file: %v", err)
	}
	t.Cleanup(func() { os.Remove(smokePath) })

	cfg := lsp.LspServerConfig{
		Command:        "gopls",
		Extensions:     map[string]string{".go": "go"},
		StartupTimeout: "120s",
		MaxRestarts:    2,
	}
	mgr := lsp.NewManager(map[string]lsp.LspServerConfig{"gopls": cfg}, "file://"+root, nil)
	defer mgr.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if _, err := mgr.EnsureServerStarted(ctx, smokePath); err != nil {
		t.Fatalf("start gopls: %v", err)
	}

	// First touch: DidChange behaves like OpenFile with the valid content —
	// mirrors the fs edit/write tools' very first mutation of a session.
	if err := mgr.DidChange(ctx, smokePath, validContent); err != nil {
		t.Fatalf("DidChange (open): %v", err)
	}

	const brokenContent = "package lsp\n\nfunc zzzEditSyncSmokeTempBroken( {\n"
	if err := mgr.DidChange(ctx, smokePath, brokenContent); err != nil {
		t.Fatalf("DidChange (introduce syntax error): %v", err)
	}

	diags := mgr.DiagnosticsForFile(ctx, smokePath, 60*time.Second)
	if len(diags) == 0 || len(diags[0].Diagnostics) == 0 {
		t.Fatal("expected gopls to report at least one diagnostic for the broken content, got none")
	}
	t.Logf("gopls reported %d diagnostic(s) for the syntax error: %q",
		len(diags[0].Diagnostics), diags[0].Diagnostics[0].Message)

	// Fix it — the ghost error must not reappear (A5's ClearFile
	// invalidation, now proven against a real server rather than the
	// registry-level unit test).
	if err := mgr.DidChange(ctx, smokePath, validContent); err != nil {
		t.Fatalf("DidChange (fix): %v", err)
	}
	// gopls needs a moment to re-publish (an empty or absent set) for the
	// now-valid file; DiagnosticsForFile's bounded wait covers that — a
	// short timeout is enough since we're asserting ABSENCE, not waiting to
	// discover something.
	if stale := mgr.DiagnosticsForFile(ctx, smokePath, 5*time.Second); len(stale) != 0 {
		t.Errorf("expected no diagnostics after fixing the file, got %+v", stale)
	}
}
