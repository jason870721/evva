package agent

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNoStructuredOutput is returned by Run / Continue when the agent was
// armed with a structured-output schema (WithStructuredOutput) but the model
// ended the run without calling the structured_output tool. The returned
// string is still the model's prose answer — callers that require the
// contract treat this as a soft failure and may retry or fall back.
var ErrNoStructuredOutput = errors.New("agent: run ended without structured output")

// WithStructuredOutput arms structured-output mode: the agent exposes a
// one-off structured_output tool whose input schema is schema, the tool's
// description steers the model to call it exactly once at the end, and Run
// returns the captured payload as a JSON string instead of prose.
//
// Headless-only by design: cmd/evva wires it exclusively on the -no-tui
// path, and SDK hosts must not combine it with an interactive UI — the run
// terminates on capture and the JSON becomes the final message. An invalid
// schema (empty, or not a JSON object) disarms the feature with a warning
// logged at construction rather than half-registering a broken tool.
func WithStructuredOutput(schema json.RawMessage) Option {
	return func(a *Agent) {
		if len(schema) == 0 {
			a.structuredSchemaErr = errors.New("empty schema")
			return
		}
		var obj map[string]any
		if err := json.Unmarshal(schema, &obj); err != nil {
			a.structuredSchemaErr = fmt.Errorf("schema must be a JSON object: %w", err)
			return
		}
		a.structuredSchema = schema
	}
}

// CaptureStructuredOutput implements structured.Sink: the structured_output
// tool hands its validated payload here, and the loop terminates the run
// after the current tool batch. Called from a tool-dispatch goroutine, so
// the payload lands under the mutex; if the model disobeys "exactly once"
// and calls the tool twice in one batch, the last capture wins.
func (a *Agent) CaptureStructuredOutput(payload json.RawMessage) {
	a.structuredMu.Lock()
	defer a.structuredMu.Unlock()
	a.structuredPayload = payload
	a.structuredSet = true
}

// structuredArmed reports whether this agent runs in structured-output mode.
// Set once at construction (WithStructuredOutput), never mutated after.
func (a *Agent) structuredArmed() bool { return len(a.structuredSchema) > 0 }

// structuredResult returns the captured payload and whether one landed.
func (a *Agent) structuredResult() (string, bool) {
	a.structuredMu.Lock()
	defer a.structuredMu.Unlock()
	if !a.structuredSet {
		return "", false
	}
	return string(a.structuredPayload), true
}

// resetStructured clears a prior run's captured payload so each Run's
// return reflects that run alone. Run calls it after winning the running
// CAS; Continue deliberately does not — an iter-limit resume keeps
// capturing into the same turn.
func (a *Agent) resetStructured() {
	a.structuredMu.Lock()
	defer a.structuredMu.Unlock()
	a.structuredPayload = nil
	a.structuredSet = false
}
