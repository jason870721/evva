package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

func TestToolGoToDefinition(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	srv := &Server{
		Name:    "gopls",
		state:   StateRunning,
		rootURI: "file:///test",
		client:  client,
		logger:  slog.Default(),
	}

	mgr := &Manager{
		servers:   map[string]*Server{"gopls": srv},
		extMap:    map[string]string{".go": "gopls"},
		extLang:   map[string]string{".go": "go"},
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	// Create a temp file so fileExists returns true.
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	tool := NewTool(mgr, tmpDir)

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"go_to_definition","filePath":"main.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "file:///project/other.go") {
		t.Errorf("expected definition location, got: %s", result.Content)
	}
}

func TestToolFindReferences(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	srv := &Server{
		Name:    "gopls",
		state:   StateRunning,
		rootURI: "file:///test",
		client:  client,
		logger:  slog.Default(),
	}

	mgr := &Manager{
		servers:   map[string]*Server{"gopls": srv},
		extMap:    map[string]string{".go": "gopls"},
		extLang:   map[string]string{".go": "go"},
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	tool := NewTool(mgr, tmpDir)

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"find_references","filePath":"main.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "References") {
		t.Errorf("expected references, got: %s", result.Content)
	}
}

func TestToolHover(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	srv := &Server{
		Name:    "gopls",
		state:   StateRunning,
		rootURI: "file:///test",
		client:  client,
		logger:  slog.Default(),
	}

	mgr := &Manager{
		servers:   map[string]*Server{"gopls": srv},
		extMap:    map[string]string{".go": "gopls"},
		extLang:   map[string]string{".go": "go"},
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	tool := NewTool(mgr, tmpDir)

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"hover","filePath":"main.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "func hello") {
		t.Errorf("expected hover content, got: %s", result.Content)
	}
}

func TestToolDocumentSymbols(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	srv := &Server{
		Name:    "gopls",
		state:   StateRunning,
		rootURI: "file:///test",
		client:  client,
		logger:  slog.Default(),
	}

	mgr := &Manager{
		servers:   map[string]*Server{"gopls": srv},
		extMap:    map[string]string{".go": "gopls"},
		extLang:   map[string]string{".go": "go"},
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	tool := NewTool(mgr, tmpDir)

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"document_symbols","filePath":"main.go"}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "main") {
		t.Errorf("expected symbol 'main', got: %s", result.Content)
	}
}

func TestToolUnknownOperation(t *testing.T) {
	mgr := &Manager{
		servers:   make(map[string]*Server),
		extMap:    make(map[string]string),
		extLang:   make(map[string]string),
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	tool := NewTool(mgr, "/tmp")

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"unknown_op","filePath":"/tmp/main.go"}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown operation")
	}
}

func TestToolMissingFile(t *testing.T) {
	mgr := &Manager{
		servers:   make(map[string]*Server),
		extMap:    make(map[string]string),
		extLang:   make(map[string]string),
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	tool := NewTool(mgr, "/tmp")

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"go_to_definition","filePath":"/nonexistent.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing file")
	}
}

func TestToolNoManager(t *testing.T) {
	tool := NewTool(nil, "/tmp")

	ctx := context.Background()
	input := json.RawMessage(`{"operation":"go_to_definition","filePath":"/tmp/main.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when manager is nil")
	}
}

func TestLSPToolInterface(t *testing.T) {
	// Verify the tool satisfies the tools.Tool interface.
	mgr := &Manager{
		servers:   make(map[string]*Server),
		extMap:    make(map[string]string),
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}
	tool := NewTool(mgr, "/tmp")

	var _ tools.Tool = tool

	if tool.Name() != "lsp_request" {
		t.Errorf("expected Name 'lsp_request', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty Description")
	}
	if len(tool.Schema()) == 0 {
		t.Error("expected non-empty Schema")
	}
}

func TestSchemaIsValidJSON(t *testing.T) {
	mgr := &Manager{
		servers:   make(map[string]*Server),
		extMap:    make(map[string]string),
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}
	tool := NewTool(mgr, "/tmp")
	schema := tool.Schema()

	var js json.RawMessage
	if err := json.Unmarshal(schema, &js); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}

	// Verify it has the oneOf discriminator.
	var obj map[string]any
	if err := json.Unmarshal(schema, &obj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if _, ok := obj["oneOf"]; !ok {
		t.Error("expected 'oneOf' in schema")
	}
}

// Avoid unused import warnings.
var _ = protocol.Position{}

func TestToolGoToImplementation(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	srv := wiredServer(conn)
	mgr := wiredManager(srv)
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	tool := NewTool(mgr, tmpDir)
	ctx := context.Background()
	input := json.RawMessage(`{"operation":"go_to_implementation","filePath":"main.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "file:///project/impl_a.go") {
		t.Errorf("expected implementation location, got: %s", result.Content)
	}
}

func TestToolPrepareCallHierarchy(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	srv := wiredServer(conn)
	mgr := wiredManager(srv)
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	tool := NewTool(mgr, tmpDir)
	ctx := context.Background()
	input := json.RawMessage(`{"operation":"prepare_call_hierarchy","filePath":"main.go","line":1,"character":1}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "main") {
		t.Errorf("expected call hierarchy item 'main', got: %s", result.Content)
	}
}

func TestToolIncomingCalls(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	srv := wiredServer(conn)
	mgr := wiredManager(srv)
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	// We need a server for the item's URI. Ensure the server is started.
	srv2, _ := mgr.EnsureServerStarted(context.Background(), goFile)
	if srv2 != nil {
		_ = srv2
	}

	tool := NewTool(mgr, tmpDir)
	ctx := context.Background()

	item := protocol.CallHierarchyItem{
		Name: "main", Kind: protocol.SKFunction,
		URI: "file://" + goFile,
		Range:          protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 10, Character: 1}},
		SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 4}},
	}
	itemJSON, _ := json.Marshal(item)

	raw := json.RawMessage(fmt.Sprintf(`{"operation":"incoming_calls","item":%s}`, itemJSON))
	result, err := tool.Execute(ctx, slog.Default(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "caller") {
		t.Errorf("expected incoming call from 'caller', got: %s", result.Content)
	}
}

func TestToolOutgoingCalls(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	srv := wiredServer(conn)
	mgr := wiredManager(srv)
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	// Ensure the server is started for the item's URI.
	mgr.EnsureServerStarted(context.Background(), goFile)

	tool := NewTool(mgr, tmpDir)
	ctx := context.Background()

	item := protocol.CallHierarchyItem{
		Name: "main", Kind: protocol.SKFunction,
		URI: "file://" + goFile,
		Range:          protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 10, Character: 1}},
		SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 4}},
	}
	itemJSON, _ := json.Marshal(item)

	raw := json.RawMessage(fmt.Sprintf(`{"operation":"outgoing_calls","item":%s}`, itemJSON))
	result, err := tool.Execute(ctx, slog.Default(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "callee") {
		t.Errorf("expected outgoing call to 'callee', got: %s", result.Content)
	}
}

func TestToolWorkspaceSymbol(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	srv := wiredServer(conn)
	mgr := wiredManager(srv)
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	tool := NewTool(mgr, tmpDir)
	ctx := context.Background()
	input := json.RawMessage(`{"operation":"workspace_symbol","query":"Server","filePath":"main.go"}`)

	result, err := tool.Execute(ctx, slog.Default(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ServeHTTP") {
		t.Errorf("expected workspace symbol 'ServeHTTP', got: %s", result.Content)
	}
}

// wiredServer creates a Server connected to a mockConn.
func wiredServer(conn *mockConn) *Server {
	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)
	return &Server{
		Name: "gopls", state: StateRunning, rootURI: "file:///test",
		client: client, logger: slog.Default(),
	}
}

// wiredManager creates a Manager with the given server pre-registered.
func wiredManager(srv *Server) *Manager {
	return &Manager{
		servers:   map[string]*Server{"gopls": srv},
		extMap:    map[string]string{".go": "gopls"},
		extLang:   map[string]string{".go": "go"},
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}
}
