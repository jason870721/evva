package fs

// LSPSyncSink is notified after the edit/write tools mutate a file, so the
// runtime's LSP layer can re-analyze it and the model sees real, fresh
// diagnostics for what it just wrote — instead of editing blind until a
// later bash build happens to trip over the error. Declared consumer-side
// (mirrors CheckpointSink) so pkg/tools/fs imports neither pkg/tools/lsp nor
// internal/*; the runtime wires a concrete implementation in via
// WithLSPSync. A nil sink disables it entirely — the tools nil-check before
// calling, and the runtime only installs a sink when an LSP manager exists —
// so the feature has zero cost on the edit hot path when LSP is off.
type LSPSyncSink interface {
	// NotifyEdited reports that absPath now holds newContent. The didChange
	// dispatch itself is best-effort and, by default, asynchronous — it must
	// never block or fail the edit. The returned string is a rendered
	// diagnostics block to append to the tool's own Result.Content; it is
	// non-empty only when the runtime's synchronous opt-in tier is enabled
	// and diagnostics for this file arrived within its bounded wait. Empty in
	// every other case (including whenever the synchronous tier is off).
	NotifyEdited(absPath, newContent string) string
}
