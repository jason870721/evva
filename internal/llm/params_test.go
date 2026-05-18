package llm

import (
	"net/http"
	"reflect"
	"testing"
)

// Phase 1 analysis — params surface:
//   - Pointer fields (Temperature, TopP, TopK) preserve "explicitly unset"
//     semantics — each With* setter must allocate so the param can be
//     distinguished from zero.
//   - WithMaxTokens / WithSystem / WithEffort set value-type fields directly.
//   - WithStopSequences must COPY the input slice (caller mutation post-call
//     must not leak into params).
//   - WithHTTPClient stores the pointer.
//   - Apply runs every option in order; later options override earlier ones.
//   - LLMParams.HTTP returns the configured client, or http.DefaultClient
//     when nil.

func TestWithTemperature_SetsPointer(t *testing.T) {
	var p LLMParams
	WithTemperature(0.7)(&p)
	if p.Temperature == nil {
		t.Fatal("Temperature: got nil, want non-nil pointer")
	}
	if *p.Temperature != 0.7 {
		t.Errorf("Temperature: got %v, want 0.7", *p.Temperature)
	}
}

func TestWithTopP_SetsPointer(t *testing.T) {
	var p LLMParams
	WithTopP(0.9)(&p)
	if p.TopP == nil || *p.TopP != 0.9 {
		t.Errorf("TopP: got %v, want pointer to 0.9", p.TopP)
	}
}

func TestWithTopK_SetsPointer(t *testing.T) {
	var p LLMParams
	WithTopK(40)(&p)
	if p.TopK == nil || *p.TopK != 40 {
		t.Errorf("TopK: got %v, want pointer to 40", p.TopK)
	}
}

func TestWithMaxTokens_SetsValue(t *testing.T) {
	var p LLMParams
	WithMaxTokens(4096)(&p)
	if p.MaxTokens != 4096 {
		t.Errorf("MaxTokens: got %d, want 4096", p.MaxTokens)
	}
}

func TestWithSystem_SetsValue(t *testing.T) {
	var p LLMParams
	WithSystem("you are helpful")(&p)
	if p.System != "you are helpful" {
		t.Errorf("System: got %q", p.System)
	}
}

func TestWithEffort_SetsValue(t *testing.T) {
	var p LLMParams
	WithEffort(3)(&p)
	if p.Effort != 3 {
		t.Errorf("Effort: got %d, want 3", p.Effort)
	}
}

func TestWithStopSequences_CopiesInput(t *testing.T) {
	// Caller-mutation safety: if the caller's slice changes after the
	// option runs, the params must NOT change with it. Otherwise we'd
	// have spooky cross-coupling.
	src := []string{"END", "STOP"}
	var p LLMParams
	WithStopSequences(src...)(&p)

	src[0] = "MUTATED"
	if !reflect.DeepEqual(p.StopSequences, []string{"END", "STOP"}) {
		t.Errorf("StopSequences leaked caller mutation: got %v", p.StopSequences)
	}
}

func TestWithStopSequences_EmptyVariadicYieldsEmpty(t *testing.T) {
	// append([]string(nil), <empty variadic>...) returns nil — that's
	// the actual contract. Either nil or empty non-nil is fine for
	// providers, but lock down which one we ship so a refactor noticeable.
	var p LLMParams
	WithStopSequences()(&p)
	if len(p.StopSequences) != 0 {
		t.Errorf("empty variadic should yield zero-length StopSequences; got %v", p.StopSequences)
	}
}

func TestWithHTTPClient_SetsPointer(t *testing.T) {
	c := &http.Client{}
	var p LLMParams
	WithHTTPClient(c)(&p)
	if p.HTTPClient != c {
		t.Errorf("HTTPClient: got %p, want %p", p.HTTPClient, c)
	}
}

func TestApply_RunsOptionsInOrder(t *testing.T) {
	var p LLMParams
	p.Apply(
		WithSystem("first"),
		WithSystem("second"),
		WithMaxTokens(1000),
	)
	if p.System != "second" {
		t.Errorf("System: got %q, want %q (later option must win)", p.System, "second")
	}
	if p.MaxTokens != 1000 {
		t.Errorf("MaxTokens: got %d, want 1000", p.MaxTokens)
	}
}

func TestApply_IgnoresNilOptions(t *testing.T) {
	// Defensive: nil entries in the options slice (e.g. an early-returning
	// builder) must not crash Apply.
	var p LLMParams
	p.Apply(nil, WithMaxTokens(42), nil)
	if p.MaxTokens != 42 {
		t.Errorf("expected MaxTokens=42 around nil entries; got %d", p.MaxTokens)
	}
}

func TestApply_EmptyOptionListNoOp(t *testing.T) {
	var p LLMParams
	p.Apply()
	if !reflect.DeepEqual(p, LLMParams{}) {
		t.Errorf("Apply() with no options should leave zero-value; got %+v", p)
	}
}

func TestHTTP_DefaultsToDefaultClient(t *testing.T) {
	var p LLMParams
	got := p.HTTP()
	if got != http.DefaultClient {
		t.Errorf("HTTP() with nil HTTPClient: got %p, want http.DefaultClient (%p)", got, http.DefaultClient)
	}
}

func TestHTTP_ReturnsConfiguredClient(t *testing.T) {
	custom := &http.Client{}
	p := LLMParams{HTTPClient: custom}
	if got := p.HTTP(); got != custom {
		t.Errorf("HTTP(): got %p, want %p", got, custom)
	}
}

func TestParseEffort_ValidNames(t *testing.T) {
	cases := []struct {
		name  string
		level int
	}{
		{"low", 1},
		{"medium", 2},
		{"high", 3},
		{"ultra", 4},
	}
	for _, c := range cases {
		got := ParseEffort(c.name)
		if got != c.level {
			t.Errorf("ParseEffort(%q) = %d, want %d", c.name, got, c.level)
		}
	}
}

func TestParseEffort_Unknown(t *testing.T) {
	if got := ParseEffort("unknown"); got != 0 {
		t.Errorf("ParseEffort(unknown) = %d, want 0", got)
	}
	if got := ParseEffort(""); got != 0 {
		t.Errorf("ParseEffort(\"\") = %d, want 0", got)
	}
}

func TestEffortString_RoundTrip(t *testing.T) {
	for _, name := range EffortNames() {
		n := ParseEffort(name)
		back := EffortString(n)
		if back != name {
			t.Errorf("EffortString(%d) = %q, want %q", n, back, name)
		}
	}
}

func TestEffortString_Unknown(t *testing.T) {
	if got := EffortString(0); got != "medium" {
		t.Errorf("EffortString(0) = %q, want medium", got)
	}
	if got := EffortString(99); got != "medium" {
		t.Errorf("EffortString(99) = %q, want medium", got)
	}
}

func TestEffortNames_Count(t *testing.T) {
	names := EffortNames()
	if len(names) != 4 {
		t.Errorf("EffortNames() len = %d, want 4", len(names))
	}
}
