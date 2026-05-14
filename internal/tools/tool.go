package tools

import (
	"context"
	"encoding/json"
)

// Tool is the contract every tool must satisfy.
// Stateless tools are typically package-level singletons (shell.Bash).
// Stateful tools receive backing state via constructor (fs.NewRead, task.NewCreate).
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
	ID    string
	Name  string
	Input json.RawMessage
}
