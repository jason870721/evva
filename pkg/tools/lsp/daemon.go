package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// lspDaemon wraps an LSP Server as a daemon.Daemon so the agent's DaemonState
// catalog tracks it. It is NOT registered by default — callers who want
// daemon visibility (TUI strip, daemon_list) register it explicitly.
type lspDaemon struct {
	mu     sync.RWMutex
	id     string
	server *Server
	logger *slog.Logger
}

func newLspDaemon(srv *Server, logger *slog.Logger) *lspDaemon {
	return &lspDaemon{
		id:     daemon.GenerateID(daemon.KindLSP),
		server: srv,
		logger: logger,
	}
}

func (d *lspDaemon) ID() string { return d.id }

func (d *lspDaemon) Snapshot() daemon.DaemonSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	exitCode := new(int)
	*exitCode = 0

	meta := daemon.LSPMeta{
		ServerName:   d.server.Name,
		Command:      d.server.Config.Command,
		State:        d.server.State().String(),
		ExitCode:     exitCode,
		RestartCount: d.server.RestartCount(),
		MaxRestarts:  d.server.MaxRestarts(),
	}
	return daemon.DaemonSnapshot{
		ID:          d.id,
		Kind:        daemon.KindLSP,
		Status:      daemon.StatusRunning,
		Description: fmt.Sprintf("lsp:%s", d.server.Name),
		Metadata:    meta,
	}
}

func (d *lspDaemon) Kill(ctx context.Context) error {
	return d.server.Stop(ctx)
}

func (d *lspDaemon) Output() string {
	snap := d.Snapshot()
	meta := snap.Metadata.(daemon.LSPMeta)
	return fmt.Sprintf("daemon %s [lsp/%s] server=%s state=%s restarts=%d/%d",
		snap.ID, snap.Status, meta.ServerName, meta.State, meta.RestartCount, meta.MaxRestarts)
}
