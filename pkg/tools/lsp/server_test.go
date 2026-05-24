package lsp

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestServerStartStop(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	// Build a Server that wraps a Client connected to the mock.
	srv := &Server{
		Name:           "test-server",
		Config:         LspServerConfig{Command: "mock", MaxRestarts: 2},
		state:          StateStopped,
		rootURI:        "file:///test",
		maxRestarts:    2,
		startupTimeout: 5 * time.Second,
		logger:         slog.Default(),
	}

	// Wire up the client directly.
	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	srv.client = client
	srv.state = StateRunning
	srv.capabilities = mockCapabilities().Capabilities

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if !srv.IsHealthy() {
		t.Fatal("expected server to be healthy")
	}
	if srv.State() != StateRunning {
		t.Fatalf("expected StateRunning, got %s", srv.State())
	}

	// Send a definition request.
	_, err := srv.Request(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]string{"uri": "file:///test/main.go"},
		"position":     map[string]int{"line": 5, "character": 10},
	})
	if err != nil {
		t.Fatalf("definition request: %v", err)
	}

	// Stop.
	srv.state = StateStopping
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if srv.IsHealthy() {
		t.Fatal("expected server to not be healthy after stop")
	}
}

func TestServerMaxRestarts(t *testing.T) {
	srv := NewServer("bad-server", LspServerConfig{
		Command:    "/nonexistent/binary",
		MaxRestarts: 1,
	}, "file:///test", slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	if err == nil {
		t.Error("expected error for nonexistent binary")
	}
	if srv.State() != StateError {
		t.Errorf("expected StateError, got %s", srv.State())
	}
}

func TestServerStopIdempotent(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	srv := &Server{
		Name:        "test-server",
		Config:      LspServerConfig{MaxRestarts: 2},
		state:       StateRunning,
		rootURI:     "file:///test",
		maxRestarts: 2,
		logger:      slog.Default(),
	}

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)
	srv.client = client

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Multiple stops should be safe.
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestServerRequestNotHealthy(t *testing.T) {
	srv := &Server{
		Name:   "test-server",
		state:  StateStopped,
		logger: slog.Default(),
	}

	ctx := context.Background()
	_, err := srv.Request(ctx, "textDocument/definition", nil)
	if err == nil {
		t.Error("expected error when server is not running")
	}
}
