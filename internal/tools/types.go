package tools

import (
	"context"
	"encoding/json"
)

// Tool is the contract every tool must satisfy.
// Stateless tools are typically package-level singletons (fs.Read, shell.Bash).
// Stateful tools carry their backing state as struct fields, supplied by the
// profile builder that constructs them.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (Result, error)
}

// Result is what every tool returns to the agent.
type Result struct {
	Content string
	IsError bool
}

// Call is what the LLM emits when it wants to invoke a tool.
type Call struct {
	Name  string
	Input json.RawMessage
}
