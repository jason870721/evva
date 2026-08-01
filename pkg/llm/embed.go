package llm

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Embedder turns text into vectors. It is a SEPARATE capability from Client,
// not a method on it, for two reasons:
//
//   - Most providers expose embeddings on a different endpoint with a
//     different model list, and several expose no embedding models at all.
//     Widening Client would force every implementation — including downstream
//     ones — to carry a method most of them cannot honor.
//   - Embedding is optional everywhere it is used. A nil Embedder is a
//     supported configuration, not a degraded one: callers fall back to
//     behavior that never needed vectors.
//
// Implementations must be safe for concurrent use.
type Embedder interface {
	// Embed returns one vector per input, in input order. All returned
	// vectors share a dimensionality. An empty input slice returns an empty
	// result and no error.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// EmbedModel is the model id vectors are being produced under. Callers
	// persist it alongside cached vectors so a model change can be detected
	// and the cache invalidated — comparing vectors from two different models
	// is meaningless, and silently doing so produces confident nonsense.
	EmbedModel() string
}

// EmbedderFactory builds one Embedder for the given credentials and model id.
// Mirrors ClientFactory. A provider with no embedding support simply never
// registers one.
type EmbedderFactory func(api APIConfig, model string) (Embedder, error)

// EmbedderRegistry maps provider names to EmbedderFactories. Separate from
// Registry rather than an extra field on it: the set of providers that can
// chat and the set that can embed are genuinely different, and conflating
// them would make "provider X is registered" ambiguous.
//
// Safe for concurrent use; Register rejects duplicates for the same reason
// Registry does.
type EmbedderRegistry struct {
	mu        sync.RWMutex
	factories map[string]EmbedderFactory
}

// NewEmbedderRegistry returns an empty registry.
func NewEmbedderRegistry() *EmbedderRegistry {
	return &EmbedderRegistry{factories: map[string]EmbedderFactory{}}
}

// Register associates a factory with a provider name.
func (r *EmbedderRegistry) Register(name string, factory EmbedderFactory) error {
	if name == "" {
		return fmt.Errorf("llm: cannot register empty embedder provider name")
	}
	if factory == nil {
		return fmt.Errorf("llm: nil embedder factory for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		return fmt.Errorf("llm: duplicate embedder registration for %q", name)
	}
	r.factories[name] = factory
	return nil
}

// MustRegister wraps Register and panics on error. init-time use only.
func (r *EmbedderRegistry) MustRegister(name string, factory EmbedderFactory) {
	if err := r.Register(name, factory); err != nil {
		panic(err)
	}
}

// Build resolves the named provider's embedder factory and invokes it.
func (r *EmbedderRegistry) Build(name, model string, api APIConfig) (Embedder, error) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("llm: provider %q has no embedding support", name)
	}
	return factory(api, model)
}

// Has reports whether name can embed.
func (r *EmbedderRegistry) Has(name string) bool {
	r.mu.RLock()
	_, ok := r.factories[name]
	r.mu.RUnlock()
	return ok
}

// Names returns every provider that can embed, sorted.
func (r *EmbedderRegistry) Names() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.factories))
	for n := range r.factories {
		out = append(out, n)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

var (
	defaultEmbedderRegistryOnce sync.Once
	defaultEmbedderRegistry     *EmbedderRegistry
)

// DefaultEmbedderRegistry returns the process-wide embedder registry.
// Populated by importing pkg/llm/builtins for its side effect, same as
// DefaultRegistry.
func DefaultEmbedderRegistry() *EmbedderRegistry {
	defaultEmbedderRegistryOnce.Do(func() {
		defaultEmbedderRegistry = NewEmbedderRegistry()
	})
	return defaultEmbedderRegistry
}

// CosineSimilarity returns the cosine of the angle between a and b, in
// [-1, 1]. Mismatched lengths or a zero-magnitude vector return 0 — an
// undefined comparison scores as "unrelated" rather than panicking or
// producing NaN, because this runs inside a ranking loop where one bad row
// must not take down a search.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
