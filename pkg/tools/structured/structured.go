// Package structured implements the structured_output tool: a per-run tool
// whose input schema IS a caller-supplied JSON schema, letting a headless
// host receive the agent's final answer as validated JSON instead of prose.
//
// The tool never appears in a static profile. The agent registers it
// dynamically — only when the host opts in via agent.WithStructuredOutput —
// and captures the payload through the Sink seam, ending the run (the
// analog of how EnterPlanMode reaches the agent through PlanModeController).
// Ported from ref/src/tools/SyntheticOutputTool/SyntheticOutputTool.ts.
package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
)

// Sink is the seam back to the owning agent. The tool hands the validated
// structured payload to the sink, which captures it and ends the run.
type Sink interface {
	CaptureStructuredOutput(payload json.RawMessage)
}

// SinkLookup is the late-bound factory closure passed to New. Returning nil
// disables the tool — Execute surfaces a clear "no sink installed" error
// instead of silently dropping the payload (mirrors mode.ControllerLookup).
type SinkLookup func() Sink

// prompt is ported verbatim from SyntheticOutputTool.ts.
const prompt = `Use this tool to return your final response in the requested structured format. You MUST call this tool exactly once at the end of your response to provide the structured output.`

// defaultSchema keeps Schema() total when no caller schema was supplied
// (metadata-only construction, e.g. toolset.Describe). A real run always
// carries the caller's schema.
const defaultSchema = `{"type":"object"}`

// Tool is the structured_output tool instance for one agent. The caller
// schema is baked in at construction and returned verbatim from Schema(),
// so the provider constrains the model's tool input to it server-side.
type Tool struct {
	schema json.RawMessage
	lookup SinkLookup
}

// New builds the tool around the caller's JSON schema. A nil/empty schema
// falls back to a permissive empty-object schema so metadata reads stay
// total; the agent layer only registers the tool when a real schema exists.
func New(schema json.RawMessage, lookup SinkLookup) *Tool {
	if len(schema) == 0 {
		schema = json.RawMessage(defaultSchema)
	}
	return &Tool{schema: schema, lookup: lookup}
}

func (t *Tool) Name() string        { return string(tools.STRUCTURED_OUTPUT) }
func (t *Tool) Description() string { return prompt }

// Schema returns the caller-supplied schema verbatim — it IS the tool's
// input schema, so schema-enforcing providers (Anthropic, OpenAI) constrain
// the model's payload to it by construction.
func (t *Tool) Schema() json.RawMessage { return t.schema }

func (t *Tool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	if err := lightValidate(input, t.schema); err != nil {
		// Recoverable: the model gets the error back and may retry the call.
		// Rare on schema-enforcing providers; this branch exists for the ones
		// that aren't (some Ollama models) and for hand-rolled clients.
		return tools.Result{IsError: true, Content: "structured_output: " + err.Error()}, nil
	}
	sink := (Sink)(nil)
	if t.lookup != nil {
		sink = t.lookup()
	}
	if sink == nil {
		return tools.Result{IsError: true, Content: "structured_output: no sink installed — this tool is only available on runs started with a structured-output schema"}, nil
	}
	sink.CaptureStructuredOutput(input)
	logger.Debug("structured_output: captured", "bytes", len(input))
	return tools.Result{Content: "Structured output recorded."}, nil
}

// lightValidate is the defensive check layered under provider-side schema
// enforcement: the payload must be a JSON object, and every top-level key the
// schema's "required" list names must be present. It is deliberately NOT a
// full JSON-Schema engine — the provider is the load-bearing validator where
// it supports tool schemas; this catches the non-enforcing edge cases.
func lightValidate(input, schema json.RawMessage) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return fmt.Errorf("payload must be a JSON object: %w", err)
	}
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil // unreadable schema: nothing further to check here
	}
	var missing []string
	for _, k := range s.Required {
		if _, ok := payload[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("payload is missing required key(s): %s", strings.Join(missing, ", "))
	}
	return nil
}
