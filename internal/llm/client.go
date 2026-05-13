package llm

import (
	"context"
	"errors"

	"github.com/johnny1110/evva/internal/tools"
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

// Role labels who emitted a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn of the conversation passed to and from the LLM.
// ToolCall is set when an assistant message represents a tool invocation request;
// ToolID pairs the assistant's request with the subsequent tool reply so providers
// that demand explicit pairing (Anthropic, OpenAI-style) can reconstruct it.
//
// Thinking is provider-specific reasoning text (currently only DeepSeek's
// reasoning_content). It is display-only — the TUI may render it, but clients
// MUST NOT echo it back in subsequent requests, since DeepSeek rejects that.
type Message struct {
	Role     Role
	Content  string
	Thinking string
	ToolCall *tools.Call
	ToolID   string
}

// Response is what the LLM returns on each completion turn.
// ToolCall is non-nil when the model wants to invoke a tool instead of replying;
// ToolID is the provider-issued identifier the agent must echo back with the result.
// Thinking carries any provider-specific reasoning trace; empty for providers
// that don't expose one. See Message.Thinking for the round-trip caveat.
type Response struct {
	Content  string
	Thinking string
	ToolCall *tools.Call
	ToolID   string
}

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
	// Apply tunes request parameters at runtime. Same options accepted by
	// NewXxx — see WithSystem, WithTemperature, etc.
	Apply(opts ...Option)
}
