package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

const structuredTestSchema = `{"type":"object","required":["summary"],"properties":{"summary":{"type":"string"}}}`

// newStructuredAgent builds a root agent, optionally armed with a
// structured-output schema, against a hermetic setup: fake Anthropic creds
// so buildLLMClient succeeds, AppHome cleared so persistSession no-ops, and
// an empty memory snapshot so the recall side-query never fires. Callers
// swap a.llm for a stub before Run (the compact_test precedent).
func newStructuredAgent(t *testing.T, schema json.RawMessage) *Agent {
	t.Helper()
	withProviderAPI(t, constant.ANTHROPIC.Name, llm.APIConfig{
		ApiURL:    constant.ANTHROPIC.ApiUrl,
		ApiSecret: "fake-key",
	})
	cfg := config.Get().Clone()
	cfg.AppHome = ""
	prof := Profile{
		Type:        GENERAL_PURPOSE,
		ActiveTools: []tools.ToolName{},
		LLMProvider: constant.ANTHROPIC,
		LLMModel:    constant.SONNET_4_6,
	}
	opts := []Option{
		WithConfig(cfg),
		WithMemorySnapshot(memdir.Snapshot{}),
		WithMaxIterations(4),
	}
	if schema != nil {
		opts = append(opts, WithStructuredOutput(schema))
	}
	a, err := New(nil, prof, opts...)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	t.Cleanup(a.Shutdown)
	return a
}

// TestStructuredOutput_CaptureTerminatesRun is A3+A4: the model calls
// structured_output once; Run returns the captured payload as its result and
// the loop terminates after that tool batch — the LLM is never called again.
// It also transitively pins the permission posture: were structured_output
// not auto-allowed, the sink-less default broker would deny the call and no
// capture would happen.
func TestStructuredOutput_CaptureTerminatesRun(t *testing.T) {
	a := newStructuredAgent(t, json.RawMessage(structuredTestSchema))

	if _, ok := a.active[string(tools.STRUCTURED_OUTPUT)]; !ok {
		t.Fatalf("structured_output missing from active set; have %v", mapKeys(a.active))
	}

	payload := `{"summary":"all done"}`
	var calls atomic.Int32
	a.llm = &stubLLM{complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
		if calls.Add(1) > 1 {
			t.Error("loop did not terminate after structured_output capture")
			return llm.Response{}, nil
		}
		return llm.Response{
			Content: "handing back the structured result",
			ToolCalls: []*tools.Call{
				{ID: "t1", Name: string(tools.STRUCTURED_OUTPUT), Input: json.RawMessage(payload)},
			},
		}, nil
	}}

	out, err := a.Run(context.Background(), "do the thing")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != payload {
		t.Errorf("Run = %q, want the captured payload %q", out, payload)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("LLM completions = %d, want 1 (terminal on capture)", got)
	}
}

// TestStructuredOutput_AbsentByDefault is A7: without WithStructuredOutput
// no structured_output tool exists anywhere in the agent's tool surface.
func TestStructuredOutput_AbsentByDefault(t *testing.T) {
	a := newStructuredAgent(t, nil)
	if _, ok := a.active[string(tools.STRUCTURED_OUTPUT)]; ok {
		t.Fatal("structured_output must be absent without WithStructuredOutput")
	}
	for _, tl := range a.exposeTools {
		if tl.Name() == string(tools.STRUCTURED_OUTPUT) {
			t.Fatal("structured_output leaked into exposeTools")
		}
	}
	if a.structuredArmed() {
		t.Fatal("agent must not be armed without a schema")
	}
}

// TestStructuredOutput_InvalidSchemaDisarms is A8: a caller schema that is
// not a JSON object never half-registers a broken tool — the feature simply
// disarms (with a warning on the agent logger).
func TestStructuredOutput_InvalidSchemaDisarms(t *testing.T) {
	for name, schema := range map[string]string{
		"not json":   `{"type":`,
		"non-object": `[1,2,3]`,
	} {
		a := newStructuredAgent(t, json.RawMessage(schema))
		if a.structuredArmed() {
			t.Errorf("%s: invalid schema must disarm structured mode", name)
		}
		if _, ok := a.active[string(tools.STRUCTURED_OUTPUT)]; ok {
			t.Errorf("%s: tool must be absent for an invalid schema", name)
		}
	}
}

// TestStructuredOutput_ProseEndReturnsError is §5.5: the model ends the run
// without calling the tool → Run returns the prose plus ErrNoStructuredOutput
// so the caller can tell "got JSON" from "model declined the channel".
func TestStructuredOutput_ProseEndReturnsError(t *testing.T) {
	a := newStructuredAgent(t, json.RawMessage(structuredTestSchema))
	a.llm = &stubLLM{complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
		return llm.Response{Content: "here is a prose answer"}, nil
	}}

	out, err := a.Run(context.Background(), "do the thing")
	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("err = %v, want ErrNoStructuredOutput", err)
	}
	if out != "here is a prose answer" {
		t.Errorf("Run = %q, want the prose answer alongside the error", out)
	}
}

// TestStructuredOutput_MalformedPayloadRetries is A5: a payload failing the
// light validation surfaces as an IsError tool result, nothing is captured,
// and the loop CONTINUES — the model gets the error and corrects itself.
func TestStructuredOutput_MalformedPayloadRetries(t *testing.T) {
	a := newStructuredAgent(t, json.RawMessage(structuredTestSchema))

	good := `{"summary":"fixed"}`
	var calls atomic.Int32
	a.llm = &stubLLM{complete: func(_ context.Context, msgs []llm.Message, _ []tools.Tool) (llm.Response, error) {
		switch calls.Add(1) {
		case 1:
			// Missing the schema-required "summary" key.
			return llm.Response{ToolCalls: []*tools.Call{
				{ID: "t1", Name: string(tools.STRUCTURED_OUTPUT), Input: json.RawMessage(`{"wrong":true}`)},
			}}, nil
		case 2:
			// The previous tool result must have come back as an error.
			last := msgs[len(msgs)-1]
			if last.Role != llm.RoleTool || len(last.ToolResults) != 1 || !last.ToolResults[0].IsError {
				t.Errorf("expected an IsError tool result after the malformed payload, got %+v", last)
			}
			return llm.Response{ToolCalls: []*tools.Call{
				{ID: "t2", Name: string(tools.STRUCTURED_OUTPUT), Input: json.RawMessage(good)},
			}}, nil
		default:
			t.Error("loop did not terminate after the corrected capture")
			return llm.Response{}, nil
		}
	}}

	out, err := a.Run(context.Background(), "do the thing")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != good {
		t.Errorf("Run = %q, want %q", out, good)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("LLM completions = %d, want 2 (error round-trip then capture)", got)
	}
}

// TestStructuredOutput_SecondRunResets: a capture from run 1 must not leak
// into run 2's result — each Run's outcome reflects that run alone.
func TestStructuredOutput_SecondRunResets(t *testing.T) {
	a := newStructuredAgent(t, json.RawMessage(structuredTestSchema))

	payload := `{"summary":"first run"}`
	a.llm = &stubLLM{complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
		return llm.Response{ToolCalls: []*tools.Call{
			{ID: "t1", Name: string(tools.STRUCTURED_OUTPUT), Input: json.RawMessage(payload)},
		}}, nil
	}}
	if out, err := a.Run(context.Background(), "first"); err != nil || out != payload {
		t.Fatalf("run 1: out=%q err=%v", out, err)
	}

	a.llm = &stubLLM{complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
		return llm.Response{Content: "prose only this time"}, nil
	}}
	out, err := a.Run(context.Background(), "second")
	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("run 2: err = %v, want ErrNoStructuredOutput (stale capture leaked?)", err)
	}
	if out != "prose only this time" {
		t.Errorf("run 2: out = %q", out)
	}
}
