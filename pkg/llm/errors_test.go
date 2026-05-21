package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

// Phase 1 analysis — NormalizeErr code paths:
//   - nil → nil
//   - context.Canceled (direct or wrapped) → ErrInterrupted
//   - any other error → returned unchanged
//
// Contract: callers detect interruption via errors.Is(err, ErrInterrupted).

func TestNormalizeErr_NilPassesThrough(t *testing.T) {
	if got := NormalizeErr(nil); got != nil {
		t.Errorf("NormalizeErr(nil): got %v, want nil", got)
	}
}

func TestNormalizeErr_DirectCanceledMapsToInterrupted(t *testing.T) {
	got := NormalizeErr(context.Canceled)
	if !errors.Is(got, ErrInterrupted) {
		t.Errorf("expected ErrInterrupted; got %v", got)
	}
}

func TestNormalizeErr_WrappedCanceledMapsToInterrupted(t *testing.T) {
	// Realistic shape: a provider wraps context.Canceled with extra
	// diagnostic text. NormalizeErr must still unwrap it correctly.
	wrapped := fmt.Errorf("ollama: stream read: %w", context.Canceled)
	got := NormalizeErr(wrapped)
	if !errors.Is(got, ErrInterrupted) {
		t.Errorf("wrapped context.Canceled should normalize to ErrInterrupted; got %v", got)
	}
}

func TestNormalizeErr_ContextWithCancelEndToEnd(t *testing.T) {
	// Sanity: produce a real context.Canceled via ctx.Err() rather than
	// importing the sentinel directly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := NormalizeErr(ctx.Err())
	if !errors.Is(got, ErrInterrupted) {
		t.Errorf("ctx.Err() after cancel should normalize to ErrInterrupted; got %v", got)
	}
}

func TestNormalizeErr_DeadlineExceededDoesNotMap(t *testing.T) {
	// context.DeadlineExceeded is a separate sentinel — only Canceled is
	// mapped. This is by design: a deadline is "we waited too long", not
	// "the user pressed ESC", and the agent loop wants to report them
	// differently.
	got := NormalizeErr(context.DeadlineExceeded)
	if errors.Is(got, ErrInterrupted) {
		t.Errorf("DeadlineExceeded should NOT map to ErrInterrupted; got %v", got)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("DeadlineExceeded should pass through unchanged; got %v", got)
	}
}

func TestNormalizeErr_GenericErrorPassesThrough(t *testing.T) {
	want := io.EOF
	if got := NormalizeErr(want); got != want {
		t.Errorf("generic err should pass through; got %v, want %v", got, want)
	}
}

func TestErrInterrupted_HasReadableMessage(t *testing.T) {
	// Smoke check the sentinel itself — its message is user/log-facing.
	if msg := ErrInterrupted.Error(); msg != "llm: interrupted" {
		t.Errorf("ErrInterrupted message: got %q, want %q", msg, "llm: interrupted")
	}
}
