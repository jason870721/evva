package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// NewStub creates a Tool whose Execute always returns a "not implemented"
// error. Useful for declaring a tool's name/description/schema before its
// behavior is built — the LLM sees a real tool definition, and any call
// surfaces a clear "TODO" instead of silently doing nothing.
//
// Once a stub's Execute is implemented, replace the variable with the real
// type and the LLM contract stays stable.
func NewStub(name ToolName, description, schema string) Tool {
	return &stubTool{
		name:   name,
		desc:   description,
		schema: json.RawMessage(schema),
	}
}

type stubTool struct {
	name   ToolName
	desc   string
	schema json.RawMessage
}

func (s *stubTool) Name() string            { return string(s.name) }
func (s *stubTool) Description() string     { return s.desc }
func (s *stubTool) Schema() json.RawMessage { return s.schema }

func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (Result, error) {
	return Result{
		IsError: true,
		Content: fmt.Sprintf("tool %q is not implemented yet", s.name),
	}, nil
}
