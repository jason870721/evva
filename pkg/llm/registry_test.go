package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/tools"
)

// stubClient is the minimal llm.Client implementation that lets us prove a
// registered factory is wired through Build and into the agent layer
// without dragging real provider IO into the test.
type stubClient struct {
	name  string
	model string
	api   APIConfig
}

func (s *stubClient) Name() string  { return s.name }
func (s *stubClient) Model() string { return s.model }
func (s *stubClient) Complete(_ context.Context, _ []Message, _ []tools.Tool) (Response, error) {
	return Response{Content: "stub: " + s.model}, nil
}
func (s *stubClient) Stream(_ context.Context, _ []Message, _ []tools.Tool, _ ChunkSink) (Response, error) {
	return Response{Content: "stub-stream: " + s.model}, nil
}
func (s *stubClient) Apply(_ ...Option) {}

func TestRegistry_RegisterAndBuild(t *testing.T) {
	r := NewRegistry()

	err := r.Register("custom", func(cfg APIConfig, model string, opts ...Option) (Client, error) {
		return &stubClient{name: "custom", model: model, api: cfg}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !r.Has("custom") {
		t.Errorf("Has(custom) should be true")
	}

	client, err := r.Build("custom", "stub-model", APIConfig{ApiURL: "http://x"}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if client.Name() != "custom" {
		t.Errorf("Name(): got %q, want custom", client.Name())
	}
	if client.Model() != "stub-model" {
		t.Errorf("Model(): got %q, want stub-model", client.Model())
	}
}

func TestRegistry_DuplicateRegistrationFails(t *testing.T) {
	r := NewRegistry()
	factory := func(APIConfig, string, ...Option) (Client, error) { return nil, nil }

	if err := r.Register("dup", factory); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register("dup", factory)
	if err == nil {
		t.Fatal("second Register should fail")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate; got %q", err.Error())
	}
}

func TestRegistry_NilFactoryFails(t *testing.T) {
	r := NewRegistry()
	err := r.Register("nilfac", nil)
	if err == nil {
		t.Fatal("Register with nil factory should fail")
	}
}

func TestRegistry_EmptyNameFails(t *testing.T) {
	r := NewRegistry()
	err := r.Register("", func(APIConfig, string, ...Option) (Client, error) { return nil, nil })
	if err == nil {
		t.Fatal("Register with empty name should fail")
	}
}

func TestRegistry_BuildUnknownProviderErrors(t *testing.T) {
	r := NewRegistry()
	_, err := r.Build("nope", "m", APIConfig{}, nil)
	if err == nil {
		t.Fatal("Build for unknown provider should error")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error should mention unknown provider; got %q", err.Error())
	}
}

func TestRegistry_FactoryErrorPropagates(t *testing.T) {
	r := NewRegistry()
	sentinel := errors.New("factory boom")
	_ = r.Register("boom", func(APIConfig, string, ...Option) (Client, error) {
		return nil, sentinel
	})
	_, err := r.Build("boom", "m", APIConfig{}, nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestRegistry_NamesSorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"zeta", "alpha", "mu"} {
		_ = r.Register(n, func(APIConfig, string, ...Option) (Client, error) { return nil, nil })
	}
	names := r.Names()
	if len(names) != 3 || names[0] != "alpha" || names[1] != "mu" || names[2] != "zeta" {
		t.Errorf("Names should be sorted; got %v", names)
	}
}

func TestDefaultRegistry_SameInstance(t *testing.T) {
	a := DefaultRegistry()
	b := DefaultRegistry()
	if a != b {
		t.Errorf("DefaultRegistry should return the same pointer across calls")
	}
}
