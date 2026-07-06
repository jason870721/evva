package structured

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

type captureSink struct {
	payload json.RawMessage
	calls   int
}

func (c *captureSink) CaptureStructuredOutput(p json.RawMessage) {
	c.payload = p
	c.calls++
}

const testSchema = `{"type":"object","required":["summary","count"],"properties":{"summary":{"type":"string"},"count":{"type":"integer"}}}`

func TestSchemaReturnsCallerSchemaVerbatim(t *testing.T) {
	tl := New(json.RawMessage(testSchema), nil)
	if got := string(tl.Schema()); got != testSchema {
		t.Errorf("Schema() = %q, want caller schema verbatim", got)
	}
	if tl.Name() != string(tools.STRUCTURED_OUTPUT) {
		t.Errorf("Name() = %q", tl.Name())
	}
	if !strings.Contains(tl.Description(), "exactly once at the end") {
		t.Errorf("Description() missing the ported steer: %q", tl.Description())
	}
}

func TestNilSchemaFallsBackToEmptyObject(t *testing.T) {
	tl := New(nil, nil)
	if got := string(tl.Schema()); got != defaultSchema {
		t.Errorf("Schema() = %q, want %q", got, defaultSchema)
	}
}

func TestExecuteCapturesPayload(t *testing.T) {
	sink := &captureSink{}
	tl := New(json.RawMessage(testSchema), func() Sink { return sink })

	in := json.RawMessage(`{"summary":"ok","count":3}`)
	res, err := tl.Execute(context.Background(), tools.NopLogger(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if sink.calls != 1 {
		t.Fatalf("sink calls = %d, want 1", sink.calls)
	}
	if string(sink.payload) != string(in) {
		t.Errorf("captured payload = %s, want %s", sink.payload, in)
	}
}

func TestExecuteRejectsMissingRequiredKey(t *testing.T) {
	sink := &captureSink{}
	tl := New(json.RawMessage(testSchema), func() Sink { return sink })

	res, err := tl.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"summary":"ok"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for missing required key")
	}
	if !strings.Contains(res.Content, "count") {
		t.Errorf("error should name the missing key: %q", res.Content)
	}
	if sink.calls != 0 {
		t.Errorf("sink must not capture a rejected payload (calls=%d)", sink.calls)
	}
}

func TestExecuteRejectsNonObjectPayload(t *testing.T) {
	tl := New(json.RawMessage(testSchema), func() Sink { return &captureSink{} })
	res, err := tl.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`[1,2,3]`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for a non-object payload")
	}
}

func TestExecuteWithoutSinkIsCleanError(t *testing.T) {
	for _, lookup := range []SinkLookup{nil, func() Sink { return nil }} {
		tl := New(json.RawMessage(`{"type":"object"}`), lookup)
		res, err := tl.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError || !strings.Contains(res.Content, "no sink installed") {
			t.Errorf("want clean no-sink error, got IsError=%v %q", res.IsError, res.Content)
		}
	}
}

func TestLightValidateToleratesSchemaWithoutRequired(t *testing.T) {
	if err := lightValidate(json.RawMessage(`{"any":"thing"}`), json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Errorf("lightValidate: %v", err)
	}
}
