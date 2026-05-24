package lsp

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

func TestManagerServerForFile(t *testing.T) {
	configs := map[string]LspServerConfig{
		"gopls": {
			Command:    "gopls",
			Extensions: map[string]string{".go": "go"},
		},
	}

	mgr := NewManager(configs, "file:///test", slog.Default())

	srv, ok := mgr.ServerForFile("/project/main.go")
	if !ok {
		t.Fatal("expected server for main.go")
	}
	if srv.Name != "gopls" {
		t.Errorf("expected server name 'gopls', got %q", srv.Name)
	}

	_, ok = mgr.ServerForFile("/project/main.rs")
	if ok {
		t.Error("expected no server for .rs file")
	}
}

func TestManagerEnsureServerStarted(t *testing.T) {
	// Create a mock connection, wire it into a server, and test EnsureServerStarted.
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
		Name:           "gopls",
		Config:         LspServerConfig{MaxRestarts: 2},
		state:          StateRunning,
		rootURI:        "file:///test",
		capabilities:   mockCapabilities().Capabilities,
		client:         client,
		maxRestarts:    2,
		startupTimeout: 5 * time.Second,
		logger:         slog.Default(),
	}

	mgr := &Manager{
		servers:   map[string]*Server{"gopls": srv},
		extMap:    map[string]string{".go": "gopls"},
		extLang:   map[string]string{".go": "go"},
		openFiles: make(map[string]string),
		logger:    slog.Default(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv2, err := mgr.EnsureServerStarted(ctx, "/project/main.go")
	if err != nil {
		t.Fatalf("EnsureServerStarted: %v", err)
	}
	if !srv2.IsHealthy() {
		t.Fatal("expected server to be healthy")
	}

	// Unknown extension.
	_, err = mgr.EnsureServerStarted(ctx, "/project/main.rs")
	if err == nil {
		t.Error("expected error for unknown extension")
	}
}

func TestManagerOpenCloseFile(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Open a file.
	if err := mgr.OpenFile(ctx, "/project/main.go", "package main"); err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	// Close the file.
	if err := mgr.CloseFile(ctx, "/project/main.go"); err != nil {
		t.Fatalf("CloseFile: %v", err)
	}

	// Closing an already-closed file should be a no-op.
	if err := mgr.CloseFile(ctx, "/project/main.go"); err != nil {
		t.Fatalf("second CloseFile: %v", err)
	}
}

func TestManagerServerNames(t *testing.T) {
	configs := map[string]LspServerConfig{
		"gopls": {
			Command:    "gopls",
			Extensions: map[string]string{".go": "go"},
		},
		"typescript": {
			Command:    "typescript-language-server",
			Extensions: map[string]string{".ts": "typescript", ".tsx": "typescriptreact"},
		},
	}

	mgr := NewManager(configs, "file:///test", slog.Default())
	names := mgr.Servers()

	if len(names) != 2 {
		t.Errorf("expected 2 servers, got %d: %v", len(names), names)
	}
}

func TestNormalizeExt(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{".go", ".go"},
		{"go", ".go"},
		{".tsx", ".tsx"},
		{"tsx", ".tsx"},
	}

	for _, tt := range tests {
		got := normalizeExt(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeExt(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFileURI(t *testing.T) {
	uri := fileURI("/project/main.go")
	expected := "file:///project/main.go"
	if uri != expected {
		t.Errorf("fileURI = %q, want %q", uri, expected)
	}
}

// Ensure protocol types referenced in tests don't cause unused import errors.
var _ = protocol.Position{}
var _ = time.Now
