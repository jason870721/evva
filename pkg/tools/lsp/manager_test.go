package lsp

import (
	"context"
	"encoding/json"
	"log/slog"
	"runtime"
	"strings"
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

func TestManagerFirstExtensionFor(t *testing.T) {
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

	if got := mgr.FirstExtensionFor("gopls"); got != ".go" {
		t.Errorf("FirstExtensionFor(gopls) = %q, want %q", got, ".go")
	}

	// Multiple extensions — returns first in map order (non-deterministic), so just check non-empty.
	if got := mgr.FirstExtensionFor("typescript"); got == "" {
		t.Error("FirstExtensionFor(typescript) should not be empty")
	}

	if got := mgr.FirstExtensionFor("nonexistent"); got != "" {
		t.Errorf("FirstExtensionFor(nonexistent) = %q, want \"\"", got)
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
	if runtime.GOOS == "windows" {
		// filepath.Abs prefixes the current drive on Windows; pin the
		// shape (file:///<drive>:/.../project/main.go), not the drive.
		if !strings.HasPrefix(uri, "file:///") || !strings.Contains(uri, ":/") ||
			!strings.HasSuffix(uri, "/project/main.go") {
			t.Errorf("fileURI = %q, want file:///<drive>:/.../project/main.go", uri)
		}
		return
	}
	expected := "file:///project/main.go"
	if uri != expected {
		t.Errorf("fileURI = %q, want %q", uri, expected)
	}
}

// ── DidChange / DiagnosticsForFile (edit→LSP diagnostics sync) ──────────

// newTestManagerWithMockServer builds a Manager backed by a single healthy
// "gopls" server wired to an in-process mock connection, for tests that
// exercise DidChange/DiagnosticsForFile against real (mocked) wire traffic.
// The caller must conn.Close() when done.
func newTestManagerWithMockServer(t *testing.T) (*Manager, *mockConn) {
	t.Helper()
	conn := newMockConn()
	t.Cleanup(conn.Close)

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
		servers:      map[string]*Server{"gopls": srv},
		extMap:       map[string]string{".go": "gopls"},
		extLang:      map[string]string{".go": "go"},
		openFiles:    make(map[string]string),
		diagRegistry: NewDiagnosticRegistry(),
		logger:       slog.Default(),
	}
	return mgr, conn
}

// waitForNotificationCount polls conn until at least n notifications matching
// method have been recorded, or fails the test after timeout. Needed because
// the mock connection parses/records on its own goroutine — a Notify write
// returning doesn't guarantee the mock has finished processing it yet.
func waitForNotificationCount(t *testing.T, conn *mockConn, method string, n int, timeout time.Duration) []capturedNotify {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var matched []capturedNotify
		for _, note := range conn.Notifications() {
			if note.Method == method {
				matched = append(matched, note)
			}
		}
		if len(matched) >= n {
			return matched
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d %q notification(s); got %d", n, method, len(matched))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestManagerDidChange_FirstTouchOpens(t *testing.T) {
	mgr, conn := newTestManagerWithMockServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.DidChange(ctx, "/project/main.go", "package main"); err != nil {
		t.Fatalf("DidChange (first touch): %v", err)
	}

	waitForNotificationCount(t, conn, protocol.MethodDidOpen, 1, 2*time.Second)
	if got := len(waitForZeroOrMore(conn, protocol.MethodDidChange)); got != 0 {
		t.Errorf("first touch should send didOpen, not didChange; got %d didChange notifications", got)
	}
}

// waitForZeroOrMore is a non-blocking snapshot filter — used where the
// assertion is "this method was never sent", so there's nothing to wait for.
func waitForZeroOrMore(conn *mockConn, method string) []capturedNotify {
	var matched []capturedNotify
	for _, note := range conn.Notifications() {
		if note.Method == method {
			matched = append(matched, note)
		}
	}
	return matched
}

func TestManagerDidChange_SubsequentBumpsVersion(t *testing.T) {
	mgr, conn := newTestManagerWithMockServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path := "/project/main.go"
	if err := mgr.DidChange(ctx, path, "package main\n"); err != nil {
		t.Fatalf("DidChange (open): %v", err)
	}
	if err := mgr.DidChange(ctx, path, "package main\n\nfunc main() {}\n"); err != nil {
		t.Fatalf("DidChange (change): %v", err)
	}
	if err := mgr.DidChange(ctx, path, "package main\n\nfunc main() { println(1) }\n"); err != nil {
		t.Fatalf("DidChange (change 2): %v", err)
	}

	notes := waitForNotificationCount(t, conn, protocol.MethodDidChange, 2, 2*time.Second)
	var changes []protocol.DidChangeTextDocumentParams
	for _, n := range notes {
		var p protocol.DidChangeTextDocumentParams
		if err := json.Unmarshal(n.Body, &struct {
			Params *protocol.DidChangeTextDocumentParams `json:"params"`
		}{Params: &p}); err != nil {
			t.Fatalf("decode didChange params: %v", err)
		}
		changes = append(changes, p)
	}
	if changes[0].TextDocument.Version < 2 {
		t.Errorf("first didChange version = %d, want >= 2 (didOpen already used v1)", changes[0].TextDocument.Version)
	}
	if changes[1].TextDocument.Version <= changes[0].TextDocument.Version {
		t.Errorf("version did not increase: %d then %d", changes[0].TextDocument.Version, changes[1].TextDocument.Version)
	}
	if got := changes[1].ContentChanges[0].Text; got != "package main\n\nfunc main() { println(1) }\n" {
		t.Errorf("didChange content = %q, want full new content", got)
	}
}

func TestManagerDidChange_ClearsStaleDiagnostics(t *testing.T) {
	mgr, _ := newTestManagerWithMockServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path := "/project/main.go"
	uri := fileURI(path)
	if err := mgr.DidChange(ctx, path, "package main\n"); err != nil {
		t.Fatalf("DidChange (open): %v", err)
	}

	// Simulate a stale diagnostic the server published against the old content.
	mgr.diagRegistry.Register("gopls", uri, []protocol.Diagnostic{
		{Message: "undefined: foo", Severity: protocol.SeverityError},
	})

	if err := mgr.DidChange(ctx, path, "package main\n\nfunc main() {}\n"); err != nil {
		t.Fatalf("DidChange (change): %v", err)
	}

	if diags := mgr.DrainDiagnostics(); len(diags) != 0 {
		t.Errorf("expected stale diagnostics cleared by DidChange, got %+v", diags)
	}
}

func TestManagerDidChange_NoServerConfigured(t *testing.T) {
	mgr, _ := newTestManagerWithMockServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.DidChange(ctx, "/project/main.rs", "fn main() {}"); err != nil {
		t.Errorf("DidChange with no server configured should be a no-op, got err: %v", err)
	}
}

func TestManagerDiagnosticsForFile_ReturnsWhatWasPublished(t *testing.T) {
	mgr, _ := newTestManagerWithMockServer(t)
	path := "/project/main.go"
	uri := fileURI(path)

	mgr.diagRegistry.Register("gopls", uri, []protocol.Diagnostic{
		{Message: "undefined: foo", Severity: protocol.SeverityError},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := mgr.DiagnosticsForFile(ctx, path, 500*time.Millisecond)
	if len(got) != 1 || len(got[0].Diagnostics) != 1 {
		t.Fatalf("DiagnosticsForFile = %+v, want one pending entry with one diagnostic", got)
	}

	// Must not be redelivered by the passive drain (no double-delivery).
	if diags := mgr.DrainDiagnostics(); len(diags) != 0 {
		t.Errorf("DrainDiagnostics redelivered what DiagnosticsForFile already took: %+v", diags)
	}
}

func TestManagerDiagnosticsForFile_TimesOutWhenNothingArrives(t *testing.T) {
	mgr, _ := newTestManagerWithMockServer(t)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := mgr.DiagnosticsForFile(ctx, "/project/never.go", 150*time.Millisecond)
	elapsed := time.Since(start)

	if got != nil {
		t.Errorf("expected nil (nothing published), got %+v", got)
	}
	if elapsed > time.Second {
		t.Errorf("DiagnosticsForFile took %v, want bounded by ~150ms timeout", elapsed)
	}
}

// Ensure protocol types referenced in tests don't cause unused import errors.
var _ = protocol.Position{}
var _ = time.Now
