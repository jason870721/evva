package agent

import (
	"log/slog"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp"
)

// noServerManager builds a real *lsp.Manager with no configured servers, so
// DidChange and DiagnosticsForFile both take their fast no-op/timeout paths.
// This isolates the tests below to lspSyncAdapter's OWN dispatch/timing
// contract — Manager's wire-level correctness (DidChange payloads,
// DiagnosticsForFile's registry behavior) is covered directly in
// pkg/tools/lsp's own tests.
func noServerManager() *lsp.Manager {
	return lsp.NewManager(nil, "file:///test", slog.Default())
}

func TestLSPSyncAdapter_AsyncModeReturnsImmediately(t *testing.T) {
	adapter := newLSPSyncAdapter(noServerManager(), slog.Default(), func() bool { return false })

	start := time.Now()
	got := adapter.NotifyEdited("/project/main.go", "package main")
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("async tier should return \"\", got %q", got)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("async tier took %v, want near-instant (dispatch is on its own goroutine)", elapsed)
	}
}

func TestLSPSyncAdapter_NilGetSyncModeDefaultsToAsync(t *testing.T) {
	adapter := newLSPSyncAdapter(noServerManager(), slog.Default(), nil)

	start := time.Now()
	got := adapter.NotifyEdited("/project/main.go", "package main")
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("nil getSyncMode should behave like async (off), got %q", got)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("nil getSyncMode took %v, want near-instant", elapsed)
	}
}

func TestLSPSyncAdapter_SyncModeIsBoundedWhenNothingArrives(t *testing.T) {
	adapter := newLSPSyncAdapter(noServerManager(), slog.Default(), func() bool { return true })

	start := time.Now()
	got := adapter.NotifyEdited("/project/main.go", "package main")
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("expected \"\" when nothing was ever published, got %q", got)
	}
	// Must wait roughly the bounded window (proving it actually tried), but
	// never hang past it (proving the "never stalls a session" contract).
	if elapsed < 500*time.Millisecond {
		t.Errorf("sync tier returned too fast (%v) — expected it to use the bounded wait", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("sync tier took %v, want bounded by ~%v", elapsed, lspSyncOnEditTimeout)
	}
}

func TestLSPSyncAdapter_SyncModeCanToggleLiveViaGetter(t *testing.T) {
	sync := false
	adapter := newLSPSyncAdapter(noServerManager(), slog.Default(), func() bool { return sync })

	start := time.Now()
	adapter.NotifyEdited("/project/main.go", "v1")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("expected async behavior before toggling sync on, took %v", elapsed)
	}

	sync = true
	start = time.Now()
	adapter.NotifyEdited("/project/main.go", "v2")
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("expected the bounded synchronous wait after toggling sync on, took %v", elapsed)
	}
}
