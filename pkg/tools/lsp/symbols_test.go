package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManager_WorkspaceSymbols(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()
	mgr := wiredManager(wiredServer(conn))

	syms, err := mgr.WorkspaceSymbols(context.Background(), "")
	if err != nil {
		t.Fatalf("WorkspaceSymbols: %v", err)
	}
	// The mock returns ServeHTTP (Function), Server (Class), handleRequest (Method).
	if len(syms) != 3 {
		t.Fatalf("want 3 symbols, got %d: %+v", len(syms), syms)
	}
	byName := make(map[string]Symbol, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}
	srv, ok := byName["Server"]
	if !ok {
		t.Fatalf("missing Server symbol in %+v", syms)
	}
	if srv.Kind != "Class" {
		t.Errorf("Server kind = %q, want Class", srv.Kind)
	}
	if srv.File != "/project/server.go" {
		t.Errorf("Server file = %q, want /project/server.go", srv.File)
	}
	if srv.Line != 11 { // protocol line 10 (0-indexed) → 11 (1-indexed)
		t.Errorf("Server line = %d, want 11", srv.Line)
	}
}

func TestManager_WorkspaceSymbols_NoServer(t *testing.T) {
	mgr := wiredManager(wiredServer(newMockConn()))
	mgr.servers = map[string]*Server{} // drop the server
	if _, err := mgr.WorkspaceSymbols(context.Background(), ""); err == nil {
		t.Fatal("want error when no server configured, got nil")
	}
}

func TestManager_DocumentSymbols_FlattensHierarchy(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()
	mgr := wiredManager(wiredServer(conn))

	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	syms, err := mgr.DocumentSymbols(context.Background(), path)
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	// The mock returns main (Function) with a child helper (Function).
	if len(syms) != 2 {
		t.Fatalf("want 2 flattened symbols, got %d: %+v", len(syms), syms)
	}
	if syms[0].Name != "main" || syms[0].Container != "" {
		t.Errorf("first symbol = %+v, want main at top level", syms[0])
	}
	if syms[1].Name != "helper" || syms[1].Container != "main" {
		t.Errorf("second symbol = %+v, want helper contained by main", syms[1])
	}
	if syms[0].File != path {
		t.Errorf("file = %q, want %q", syms[0].File, path)
	}
}
