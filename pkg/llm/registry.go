package llm

import (
	"fmt"
	"sort"
	"sync"
)

// ClientFactory builds one llm.Client instance for the given provider
// credentials, model id, and option list. Each registered provider
// supplies a ClientFactory that wraps its own New() constructor.
//
// Factories may return an error when the provided APIConfig is invalid
// (e.g. missing API key for a cloud provider). Returning nil for both
// Client and err is a programmer error.
type ClientFactory func(api APIConfig, model string, opts ...Option) (Client, error)

// Registry maps provider names to ClientFactories. The agent loop
// resolves a (providerName, model) pair through Registry.Build at agent
// construction; downstream apps register additional providers before
// the first call to Build by writing to DefaultRegistry().
//
// Registry is safe for concurrent use. Register fails on duplicate names —
// silently overwriting would let a typo route a model id to the wrong
// implementation. Use MustRegister at init time when a duplicate is a
// programming bug.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]ClientFactory
}

// NewRegistry returns an empty registry. Most callers want DefaultRegistry
// instead, which is pre-populated with the built-in providers.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]ClientFactory{}}
}

// Register associates a factory with a provider name. Returns an error if
// name is empty, factory is nil, or name is already registered.
func (r *Registry) Register(name string, factory ClientFactory) error {
	if name == "" {
		return fmt.Errorf("llm: cannot register empty provider name")
	}
	if factory == nil {
		return fmt.Errorf("llm: nil factory for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		return fmt.Errorf("llm: duplicate registration for %q", name)
	}
	r.factories[name] = factory
	return nil
}

// MustRegister wraps Register and panics on error. Use only at init time
// where a duplicate or nil factory is a programmer bug.
func (r *Registry) MustRegister(name string, factory ClientFactory) {
	if err := r.Register(name, factory); err != nil {
		panic(err)
	}
}

// Build resolves the named provider's factory and invokes it. Returns an
// error for unregistered names — there is no silent fallback.
func (r *Registry) Build(name, model string, api APIConfig, opts []Option) (Client, error) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("llm: unknown provider %q", name)
	}
	return factory(api, model, opts...)
}

// Has reports whether name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	_, ok := r.factories[name]
	r.mu.RUnlock()
	return ok
}

// Names returns every registered provider name, sorted lexicographically.
// Useful for diagnostics, tests, and /model picker enumeration.
func (r *Registry) Names() []string {
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
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
)

// DefaultRegistry returns the process-wide registry. Pre-population is
// the caller's responsibility — import pkg/llm/builtins for its side
// effect to register the bundled providers (anthropic, deepseek, ollama):
//
//	import _ "github.com/johnny1110/evva/pkg/llm/builtins"
//
// Or register a single provider explicitly:
//
//	llm.DefaultRegistry().MustRegister(claude.ProviderName, claude.Factory)
//
// This keeps DefaultRegistry's content explicit and lets downstream apps
// opt into exactly the providers they want.
func DefaultRegistry() *Registry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewRegistry()
	})
	return defaultRegistry
}
