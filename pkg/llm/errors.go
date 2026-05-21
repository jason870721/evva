package llm

import (
	"context"
	"errors"
)

// ErrInterrupted signals that the caller cancelled the request — typically the
// user pressing ESC in the TUI. Clients return this (wrapped) instead of the
// raw context.Canceled so callers can match without importing context.
//
// Use errors.Is(err, llm.ErrInterrupted) to detect.
var ErrInterrupted = errors.New("llm: interrupted")

// NormalizeErr maps context cancellation to ErrInterrupted and leaves every
// other error untouched. Provider clients call this on transport-layer errors
// so the agent loop and TUI can treat user-initiated cancellation uniformly.
func NormalizeErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return ErrInterrupted
	}
	return err
}
