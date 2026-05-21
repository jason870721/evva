package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/johnny1110/evva/internal/tools"
)

// Phase 1 analysis — ToolSchema code paths:
//   - tool.Schema() non-empty → returned verbatim
//   - tool.Schema() empty/nil → permissive default returned

// fakeTool implements just enough of tools.Tool to drive ToolSchema. The
// other methods are stubs the tests don't exercise.
type fakeTool struct {
	schema json.RawMessage
}

func (f fakeTool) Name() string            { return "fake" }
func (f fakeTool) Description() string     { return "test tool" }
func (f fakeTool) Schema() json.RawMessage { return f.schema }
func (f fakeTool) Execute(_ context.Context, _ *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	return tools.Result{}, nil
}

func TestToolSchema_PassesThroughNonEmpty(t *testing.T) {
	want := json.RawMessage(`{"type":"object","required":["q"]}`)
	got := ToolSchema(fakeTool{schema: want})
	if string(got) != string(want) {
		t.Errorf("ToolSchema: got %q, want %q", got, want)
	}
}

func TestToolSchema_FallsBackOnEmpty(t *testing.T) {
	got := ToolSchema(fakeTool{schema: json.RawMessage{}})
	if string(got) != `{"type":"object","properties":{}}` {
		t.Errorf("ToolSchema fallback: got %q", got)
	}
}

func TestToolSchema_FallsBackOnNil(t *testing.T) {
	got := ToolSchema(fakeTool{schema: nil})
	if string(got) != `{"type":"object","properties":{}}` {
		t.Errorf("ToolSchema(nil schema): got %q", got)
	}
}

func TestToolSchema_FallbackIsValidJSON(t *testing.T) {
	// The fallback string is hand-typed; lock down that it parses as
	// valid JSON so providers serializing it don't 400.
	got := ToolSchema(fakeTool{schema: nil})
	var v map[string]any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("fallback not valid JSON: %v", err)
	}
	if v["type"] != "object" {
		t.Errorf("fallback type: got %v, want \"object\"", v["type"])
	}
}
