package llm

import (
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// Role labels who emitted a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn of the conversation passed to and from the LLM.
//
// ToolCalls is set on RoleAssistant turns when the model wants to invoke one
// or more tools. ToolResults is set on RoleTool turns and carries the result
// of each call, paired by ID with the corresponding ToolCall. A single
// RoleTool message carries every result for the preceding assistant turn —
// Anthropic mandates that fan-in, and the OpenAI-style converters fan it
// back out into per-call messages on the wire.
//
// Thinking is provider-specific reasoning text. The TUI may render it, and
// providers that require it MUST echo it back in subsequent requests:
//   - DeepSeek: reasoning_content
//   - Anthropic: thinking blocks (with ThinkingSignature) — required
//     whenever tool_use follows a thinking block, or the API errors 400.
//
// ThinkingSignature is the opaque crypto signature Anthropic ships with
// each thinking block. Carry it round-trip; treat as a black box.
type Message struct {
	Role              Role
	Content           string
	Thinking          string
	ThinkingSignature string
	ToolCalls         []*tools.Call
	ToolResults       []*ToolResult
}

// Response is what the LLM returns on each completion turn.
//
// ToolCalls is non-empty when the model wants to invoke tools instead of
// (or in addition to) replying with text. Each call carries the provider's
// id in Call.ID — the agent echoes that back in the matching ToolResult.
//
// Thinking carries any provider-specific reasoning trace; empty for
// providers that don't expose one. See Message.Thinking for the round-trip
// caveat.
type Response struct {
	Content           string
	Thinking          string
	ThinkingSignature string
	ToolCalls         []*tools.Call
	Usage             Usage
}

// ToolResult pairs a tool call's id with the result the agent dispatched.
// Lives on RoleTool messages so one message can carry N results from a
// parallel-dispatched assistant turn.
type ToolResult struct {
	ID            string
	Content       string
	IsError        bool
	ContentBlocks []tools.ContentBlock
}

// RenderContentBlocksAsText converts typed content blocks to a text-only
// representation for providers that do not support multimodal tool results.
// Image blocks are rendered as [Image: <mime>, <bytes>B] metadata stubs.
func RenderContentBlocksAsText(blocks []tools.ContentBlock) string {
	var b strings.Builder
	for i, cb := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch cb.Type {
		case tools.ContentBlockText:
			b.WriteString(cb.Text)
		case tools.ContentBlockImage:
			if cb.Image != nil {
				fmt.Fprintf(&b, "[Image: %s, %d bytes]", cb.Image.MIMEType, cb.Image.OriginalSize)
			}
		}
	}
	return b.String()
}
