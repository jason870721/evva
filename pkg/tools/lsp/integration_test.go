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
