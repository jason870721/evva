package llm

import (
	"context"

	"github.com/johnny1110/evva/internal/tools"
)

// Client abstracts the LLM provider so the agent loop never imports a concrete SDK.
//
// Cancellation contract: Complete MUST honor ctx and abort the in-flight request
// promptly when ctx is cancelled. On cancellation the implementation must return
// an error matching ErrInterrupted (via errors.Is). The TUI binds ESC to ctx
// cancellation, so this contract is what makes user interrupts work end-to-end.
type Client interface {
	Name() string
	Model() string
	Complete(ctx context.Context, messages []Message, tools []tools.Tool) (Response, error)
	// Stream is the chunk-by-chunk variant of Complete. Implementations push
	// each text/thinking delta through sink as it arrives, then return the
	// fully assembled Response (content, thinking, signature, tool calls,
	// usage). Cancellation rules are identical to Complete.
	//
	// A provider that has no native streaming endpoint MAY fall back to a
	// buffered Complete and emit a single Chunk per kind at the end — this
	// keeps the contract uniform at the cost of no progressive output.
	Stream(ctx context.Context, messages []Message, tools []tools.Tool, sink ChunkSink) (Response, error)
	// Apply tunes request parameters at runtime. Same options accepted by
	// NewXxx — see WithSystem, WithTemperature, etc.
	Apply(opts ...Option)
}
