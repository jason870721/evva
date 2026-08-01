package llm_test

import (
	"context"
	"math"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

type stubEmbedder struct{ model string }

func (s stubEmbedder) EmbedModel() string { return s.model }
func (s stubEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}

func TestEmbedderRegistryRejectsDuplicatesAndNils(t *testing.T) {
	r := llm.NewEmbedderRegistry()
	f := func(llm.APIConfig, string) (llm.Embedder, error) { return stubEmbedder{}, nil }

	if err := r.Register("x", f); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register("x", f); err == nil {
		t.Error("duplicate registration should fail — a typo silently rerouting a model is worse than an error")
	}
	if err := r.Register("", f); err == nil {
		t.Error("empty name should fail")
	}
	if err := r.Register("y", nil); err == nil {
		t.Error("nil factory should fail")
	}
}

func TestEmbedderRegistryBuildAndLookup(t *testing.T) {
	r := llm.NewEmbedderRegistry()
	r.MustRegister("local", func(llm.APIConfig, string) (llm.Embedder, error) {
		return stubEmbedder{model: "m"}, nil
	})

	if !r.Has("local") {
		t.Error("Has should report a registered provider")
	}
	if r.Has("nope") {
		t.Error("Has should not report an unregistered provider")
	}
	e, err := r.Build("local", "m", llm.APIConfig{})
	if err != nil || e == nil {
		t.Fatalf("Build: %v", err)
	}
	// An unknown provider must say it cannot embed, not fall back silently —
	// a silent fallback would produce vectors from the wrong model.
	if _, err := r.Build("nope", "m", llm.APIConfig{}); err == nil {
		t.Error("Build on an unregistered provider should error")
	}
	if got := r.Names(); len(got) != 1 || got[0] != "local" {
		t.Errorf("Names: got %v", got)
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"scaled is identical", []float32{2, 4}, []float32{1, 2}, 1},
		// Degenerate inputs score as "unrelated" rather than NaN or panic:
		// this runs inside a ranking loop where one bad row must not take
		// down the whole search.
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0},
		{"empty", nil, nil, 0},
		{"zero magnitude", []float32{0, 0}, []float32{1, 1}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := llm.CosineSimilarity(tc.a, tc.b)
			if math.Abs(got-tc.want) > 1e-6 {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if math.IsNaN(got) {
				t.Error("cosine returned NaN")
			}
		})
	}
}

// Ranking only needs the ORDER to be right, so this pins the property the
// search layer actually depends on.
func TestCosineOrdersByRelatedness(t *testing.T) {
	query := []float32{1, 1, 0}
	near := []float32{1, 0.9, 0}
	far := []float32{0, 0, 1}

	if llm.CosineSimilarity(query, near) <= llm.CosineSimilarity(query, far) {
		t.Error("a near vector must outrank a far one")
	}
}
