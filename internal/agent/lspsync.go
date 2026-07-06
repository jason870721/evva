package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp"
)

// lspSyncDispatchTimeout bounds the core tier's async didChange dispatch —
// long enough for a healthy language server to accept the notification,
// short enough that a wedged server can't leak the goroutine indefinitely.
const lspSyncDispatchTimeout = 5 * time.Second

// lspSyncOnEditTimeout bounds the opt-in synchronous tier's whole window
// (didChange dispatch + the wait for diagnostics on that file). Short enough
// that a slow/silent server never meaningfully delays the model's next turn;
// the passive between-turns drain (Agent.drainLSPDiagnostics) still delivers
// the diagnostics later regardless of whether this window catches them.
const lspSyncOnEditTimeout = 750 * time.Millisecond

// lspSyncAdapter adapts *lsp.Manager to fs.LSPSyncSink — the write-side
// counterpart to drainLSPDiagnostics' read side (agent.go). NotifyEdited
// always dispatches didChange; by default (the core tier) it does so
// asynchronously and returns immediately ("" — the passive drain delivers
// diagnostics on the model's next turn regardless). When the synchronous
// opt-in tier is enabled (getSyncMode reports true), it instead dispatches
// inline and waits a short bounded window for diagnostics on that specific
// file, returning them rendered so the model can see its own compile error
// on the same turn it introduced it.
//
// getSyncMode is called on every NotifyEdited, not captured once at
// construction, so toggling LSPDiagnosticsOnEdit via /config takes effect on
// the very next edit — no profile rebuild required.
type lspSyncAdapter struct {
	mgr         *lsp.Manager
	logger      *slog.Logger
	getSyncMode func() bool
}

// newLSPSyncAdapter builds the adapter. mgr must be non-nil (callers install
// this sink only when an LSP Manager was actually constructed).
func newLSPSyncAdapter(mgr *lsp.Manager, logger *slog.Logger, getSyncMode func() bool) *lspSyncAdapter {
	return &lspSyncAdapter{mgr: mgr, logger: logger, getSyncMode: getSyncMode}
}

// NotifyEdited implements fs.LSPSyncSink.
func (s *lspSyncAdapter) NotifyEdited(absPath, newContent string) string {
	if s.getSyncMode == nil || !s.getSyncMode() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), lspSyncDispatchTimeout)
			defer cancel()
			if err := s.mgr.DidChange(ctx, absPath, newContent); err != nil {
				s.logger.Debug("lsp: didChange", "path", absPath, "err", err)
			}
		}()
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), lspSyncOnEditTimeout)
	defer cancel()
	if err := s.mgr.DidChange(ctx, absPath, newContent); err != nil {
		s.logger.Debug("lsp: didChange (sync)", "path", absPath, "err", err)
		return ""
	}
	diags := s.mgr.DiagnosticsForFile(ctx, absPath, lspSyncOnEditTimeout)
	if len(diags) == 0 {
		return ""
	}
	return lsp.FormatDiagnosticsReminder(diags)
}
