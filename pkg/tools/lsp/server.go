package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

// ServerState is the lifecycle state of one LSP server instance.
type ServerState int

const (
	StateStopped  ServerState = iota
	StateStarting
	StateRunning
	StateStopping
	StateError
)

func (s ServerState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

const (
	defaultMaxRestarts    = 3
	defaultStartupTimeout = 30 * time.Second
	shutdownTimeout       = 5 * time.Second
)

// Server wraps a single LSP server process with a state machine.
type Server struct {
	Name   string
	Config LspServerConfig

	mu           sync.RWMutex
	client       *Client
	capabilities protocol.ServerCapabilities
	state        ServerState
	restartCount int

	rootURI string

	maxRestarts    int
	startupTimeout time.Duration

	logger *slog.Logger

	// diagHandler is stored until the client is created, then re-registered
	// via Client.OnNotify in Start().
	diagHandler NotificationHandler
}

// NewServer creates a Server from the given config. The server is not started
// until Start is called.
func NewServer(name string, cfg LspServerConfig, rootURI string, logger *slog.Logger) *Server {
	maxRestarts := cfg.MaxRestarts
	if maxRestarts <= 0 {
		maxRestarts = defaultMaxRestarts
	}
	startupTimeout := parseDuration(cfg.StartupTimeout, defaultStartupTimeout)

	return &Server{
		Name:           name,
		Config:         cfg,
		state:          StateStopped,
		rootURI:        rootURI,
		maxRestarts:    maxRestarts,
		startupTimeout: startupTimeout,
		logger:         logger,
	}
}

// Start spawns the LSP process, performs the initialize handshake, and
// transitions to StateRunning. Returns an error if the server fails to
// start. Thread-safe; concurrent callers must serialize via singleflight
// in Manager.EnsureServerStarted.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state == StateRunning {
		s.mu.Unlock()
		return nil
	}
	if s.state == StateStopping {
		s.mu.Unlock()
		return fmt.Errorf("lsp server %q is shutting down", s.Name)
	}
	if s.state == StateError && s.restartCount >= s.maxRestarts {
		s.mu.Unlock()
		return fmt.Errorf("lsp server %q exceeded max restarts (%d)", s.Name, s.maxRestarts)
	}
	s.state = StateStarting
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Debug("lsp.server.start", "server", s.Name, "command", s.Config.Command)
	}

	startCtx, cancel := context.WithTimeout(ctx, s.startupTimeout)
	defer cancel()

	// Use the callers ctx for the process lifetime (not startCtx —
	// that would kill gopls when Start() returns). The handshake below
	// uses startCtx for its timeout.
	client, err := Start(ctx, s.Config.Command, s.Config.Args, s.logger)
	if err != nil {
		s.mu.Lock()
		s.state = StateError
		s.restartCount++
		s.mu.Unlock()
		if s.logger != nil {
			s.logger.Error("lsp.server.start_failed",
				"server", s.Name, "err", err, "restart_count", s.restartCount)
		}
		// Add install hint for "command not found" errors.
		if IsNotFoundError(err) {
			return fmt.Errorf("start %s: %s", s.Name, installHint(s.Config.Command))
		}
		return fmt.Errorf("start %s: %w", s.Name, err)
	}

	caps := protocol.DefaultClientCapabilities()
	initParams := protocol.InitializeParams{
		ProcessID:    int32(os.Getpid()),
		RootURI:      s.rootURI,
		Capabilities: caps,
	}

	raw, err := client.Request(startCtx, protocol.MethodInitialize, initParams)
	if err != nil {
		client.Close()
		s.mu.Lock()
		s.state = StateError
		s.restartCount++
		s.mu.Unlock()
		if s.logger != nil {
			s.logger.Error("lsp.server.start_failed",
				"server", s.Name, "err", err, "restart_count", s.restartCount)
		}
		return fmt.Errorf("initialize %s: %w", s.Name, err)
	}

	var initResult protocol.InitializeResult
	if err := json.Unmarshal(raw, &initResult); err != nil {
		client.Close()
		s.mu.Lock()
		s.state = StateError
		s.restartCount++
		s.mu.Unlock()
		return fmt.Errorf("initialize %s: parse result: %w", s.Name, err)
	}

	if err := client.Notify(startCtx, protocol.MethodInitialized, nil); err != nil {
		client.Close()
		s.mu.Lock()
		s.state = StateError
		s.restartCount++
		s.mu.Unlock()
		return fmt.Errorf("initialized %s: %w", s.Name, err)
	}

	s.mu.Lock()
	s.client = client
	s.capabilities = initResult.Capabilities
	s.state = StateRunning
	s.restartCount = 0

	// Re-register stored notification handlers on the live client.
	if s.diagHandler != nil {
		s.client.OnNotify(protocol.MethodPublishDiagnostics, s.diagHandler)
	}
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Info("lsp.server.started", "server", s.Name)
	}
	return nil
}

// Stop sends shutdown → exit, then kills the process after a timeout.
// Idempotent — safe to call on an already-stopped server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.state == StateStopped {
		s.mu.Unlock()
		return nil
	}
	if s.state == StateStopping {
		s.mu.Unlock()
		return nil
	}
	s.state = StateStopping
	client := s.client
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Info("lsp.server.stop", "server", s.Name)
	}

	if client != nil {
		// Graceful shutdown: send shutdown request, then exit notification.
		shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()

		_, _ = client.Request(shutdownCtx, protocol.MethodShutdown, nil)
		_ = client.Notify(context.Background(), protocol.MethodExit, nil)

		if err := client.Close(); err != nil && s.logger != nil {
			s.logger.Debug("lsp.server.close", "server", s.Name, "err", err)
		}
	}

	s.mu.Lock()
	s.client = nil
	s.state = StateStopped
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Info("lsp.server.stopped", "server", s.Name)
	}
	return nil
}

// IsHealthy returns true when the server is in StateRunning.
func (s *Server) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state == StateRunning
}

// State returns the current server state.
func (s *Server) State() ServerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Capabilities returns the server's advertised capabilities.
func (s *Server) Capabilities() protocol.ServerCapabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.capabilities
}

// Request sends a typed LSP request and returns the raw JSON result.
func (s *Server) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.RLock()
	client := s.client
	state := s.state
	s.mu.RUnlock()

	if state != StateRunning || client == nil {
		return nil, fmt.Errorf("lsp server %q is not running", s.Name)
	}

	return client.Request(ctx, method, params)
}

// Notify sends a notification to the server.
func (s *Server) Notify(ctx context.Context, method string, params any) error {
	s.mu.RLock()
	client := s.client
	state := s.state
	s.mu.RUnlock()

	if state != StateRunning || client == nil {
		return fmt.Errorf("lsp server %q is not running", s.Name)
	}

	return client.Notify(ctx, method, params)
}

// RestartCount returns the number of restarts since the last successful start.
func (s *Server) RestartCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.restartCount
}

// MaxRestarts returns the configured restart limit.
func (s *Server) MaxRestarts() int {
	return s.maxRestarts
}

// OnNotify registers a notification handler on the underlying client.
func (s *Server) OnNotify(method string, handler NotificationHandler) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client != nil {
		s.client.OnNotify(method, handler)
	}
}

// SetDiagHandler stores the publishDiagnostics handler for re-registration
// when the client is created in Start(). Called by Manager at construction.
func (s *Server) SetDiagHandler(h NotificationHandler) {
	s.diagHandler = h
}
