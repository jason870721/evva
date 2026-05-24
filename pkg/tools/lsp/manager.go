package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
	"golang.org/x/sync/singleflight"
)

// Manager routes files to LSP servers based on extension and manages server
// lifecycles (lazy start, document sync, shutdown).
type Manager struct {
	servers map[string]*Server // server name → server
	extMap  map[string]string  // ".go" → "gopls"
	extLang map[string]string  // ".go" → "go"

	mu        sync.RWMutex
	openFiles map[string]string // file URI → server name

	startGroup   singleflight.Group
	diagRegistry *DiagnosticRegistry
	daemonState  *daemon.DaemonState

	logger *slog.Logger
}

// NewManager creates a Manager from a list of server configs. Servers are
// registered but not started — they start lazily on first EnsureServerStarted.
func NewManager(configs map[string]LspServerConfig, rootURI string, logger *slog.Logger) *Manager {
	m := &Manager{
		servers:      make(map[string]*Server),
		extMap:       make(map[string]string),
		extLang:      make(map[string]string),
		openFiles:    make(map[string]string),
		diagRegistry: NewDiagnosticRegistry(),
		logger:       logger,
	}

	for name, cfg := range configs {
		srv := NewServer(name, cfg, rootURI, logger)
		m.servers[name] = srv

		for ext, lang := range cfg.Extensions {
			normalized := normalizeExt(ext)
			m.extMap[normalized] = name
			m.extLang[normalized] = lang
		}

		// Wire the diagnostics handler for this server.
		m.wireDiagnosticsHandler(name, srv)
	}

	return m
}

// wireDiagnosticsHandler registers the publishDiagnostics handler on a server.
// The handler unmarshals the notification params and feeds diagnostics into
// the DiagnosticRegistry.
func (m *Manager) wireDiagnosticsHandler(serverName string, srv *Server) {
	srv.SetDiagHandler(func(params json.RawMessage) {
		var p protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		m.diagRegistry.Register(serverName, p.URI, p.Diagnostics)
	})
}

// DrainDiagnostics returns all pending diagnostics and clears the queue.
// Called by the agent loop at the start of each iteration.
func (m *Manager) DrainDiagnostics() []PendingDiagnostic {
	if m.diagRegistry == nil {
		return nil
	}
	return m.diagRegistry.Drain()
}

// NotifyFileChanged clears diagnostics for a file. Call this after external
// file modifications (write, edit, bash) to invalidate stale diagnostics.
func (m *Manager) NotifyFileChanged(filePath string) {
	if m.diagRegistry == nil {
		return
	}
	m.diagRegistry.ClearFile(fileURI(filePath))
}

// ServerForFile returns the server responsible for the given file path and
// whether one was found.
func (m *Manager) ServerForFile(filePath string) (*Server, bool) {
	ext := normalizeExt(filepath.Ext(filePath))
	m.mu.RLock()
	name, ok := m.extMap[ext]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return m.servers[name], true
}

// EnsureServerStarted lazily starts the server responsible for filePath.
// Uses singleflight to prevent duplicate starts — all concurrent callers
// for the same server share one start attempt and result.
func (m *Manager) EnsureServerStarted(ctx context.Context, filePath string) (*Server, error) {
	ext := normalizeExt(filepath.Ext(filePath))

	m.mu.RLock()
	serverName, ok := m.extMap[ext]
	m.mu.RUnlock()
	if !ok {
		hint := ""
		if s := SuggestServerForExt(filepath.Ext(filePath)); s != "" {
			hint = fmt.Sprintf(". Install %s or configure a server in .evva/lsp_servers.yml", s)
		}
		return nil, fmt.Errorf("no LSP server configured for extension %q%s", filepath.Ext(filePath), hint)
	}

	srv, exists := m.servers[serverName]
	if !exists {
		return nil, fmt.Errorf("server %q not configured", serverName)
	}

	if srv.IsHealthy() {
		return srv, nil
	}

	result, err, _ := m.startGroup.Do(serverName, func() (any, error) {
		if srv.IsHealthy() {
			return srv, nil
		}
		if srv.State() == StateError && srv.RestartCount() >= srv.MaxRestarts() {
			return nil, fmt.Errorf("lsp server %q exceeded max restarts (%d)", serverName, srv.MaxRestarts())
		}
		if err := srv.Start(ctx); err != nil {
			return nil, err
		}
		// Register with daemon state for visibility in daemon_list.
		if m.daemonState != nil {
			d := newLspDaemon(srv, m.logger)
			m.daemonState.Register(d)
		}
		return srv, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*Server), nil
}

// OpenFile sends textDocument/didOpen to the server responsible for filePath
// and tracks the open state.
func (m *Manager) OpenFile(ctx context.Context, filePath, content string) error {
	srv, ok := m.ServerForFile(filePath)
	if !ok {
		return fmt.Errorf("no LSP server for %s", filePath)
	}
	if !srv.IsHealthy() {
		return fmt.Errorf("lsp server %q is not running", srv.Name)
	}

	uri := fileURI(filePath)
	languageID := m.languageForFile(filePath)

	params := protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: languageID,
			Version:    1,
			Text:       content,
		},
	}

	if err := srv.Notify(ctx, protocol.MethodDidOpen, params); err != nil {
		return err
	}

	m.mu.Lock()
	m.openFiles[uri] = srv.Name
	m.mu.Unlock()
	return nil
}

// CloseFile sends textDocument/didClose for a previously opened file.
func (m *Manager) CloseFile(ctx context.Context, filePath string) error {
	uri := fileURI(filePath)

	m.mu.Lock()
	serverName, ok := m.openFiles[uri]
	if ok {
		delete(m.openFiles, uri)
	}
	m.mu.Unlock()

	if !ok {
		return nil // not open, nothing to do
	}

	srv, exists := m.servers[serverName]
	if !exists || !srv.IsHealthy() {
		return nil // server gone, skip
	}

	params := protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}
	return srv.Notify(ctx, protocol.MethodDidClose, params)
}

// Shutdown stops all managed servers gracefully.
func (m *Manager) Shutdown(ctx context.Context) error {
	var firstErr error
	for name, srv := range m.servers {
		if err := srv.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("shutdown %s: %w", name, err)
		}
	}
	return firstErr
}

// Servers returns all managed server names.
func (m *Manager) Servers() []string {
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	return names
}

// SetDaemonState installs the daemon catalog for LSP lifecycle tracking.
// When set, LSP servers are registered as daemons on start so daemon_list
// and daemon_output can introspect their state.
func (m *Manager) SetDaemonState(ds *daemon.DaemonState) {
	m.daemonState = ds
}

// languageForFile returns the language ID for a file based on its extension.
func (m *Manager) languageForFile(filePath string) string {
	ext := normalizeExt(filepath.Ext(filePath))
	m.mu.RLock()
	lang := m.extLang[ext]
	m.mu.RUnlock()
	if lang != "" {
		return lang
	}
	// Fallback: strip the leading dot.
	if len(ext) > 1 {
		return ext[1:]
	}
	return ext
}

// fileURI converts an absolute file path to a file:// URI.
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + filepath.ToSlash(abs)
}

// normalizeExt ensures the extension starts with a dot.
func normalizeExt(ext string) string {
	if ext == "" {
		return ext
	}
	if ext[0] != '.' {
		return "." + ext
	}
	return ext
}

// fileExists is a test seam for mocking path existence.
var fileExists = func(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
