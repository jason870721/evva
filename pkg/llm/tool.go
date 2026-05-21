package llm

import (
	"encoding/json"

	"github.com/johnny1110/evva/internal/tools"
)

// ToolSchema returns the JSON schema for a tool's input, or a permissive default.
func ToolSchema(t tools.Tool) json.RawMessage {
	if s := t.Schema(); len(s) > 0 {
		return s
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
