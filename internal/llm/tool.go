package llm

import (
	"encoding/json"

	"github.com/johnny1110/evva/internal/tools"
)

// SchemaProvider is an optional interface for tools that publish a JSON schema
// describing their input. Tools without one fall back to an empty object schema,
// which still lets the model emit a (possibly empty) JSON argument blob.
type SchemaProvider interface {
	InputSchema() json.RawMessage
}

// ToolSchema returns the JSON schema for a tool's input, or a permissive default.
func ToolSchema(t tools.Tool) json.RawMessage {
	if sp, ok := t.(SchemaProvider); ok {
		if s := sp.InputSchema(); len(s) > 0 {
			return s
		}
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
