package hooks

import "sync"

// Registry holds the configured hooks for the current session. Project +
// user scopes are merged at load time and stored under one event-keyed
// map. Project hooks come first in the slice so the dispatcher fires
// them ahead of user hooks (project hooks may short-circuit user hooks
// via continue:false).
//
// Safe for concurrent reads; writes (Reload) take the write lock.
type Registry struct {
	mu      sync.RWMutex
	byEvent map[Event][]Config
}

// NewRegistry returns an empty Registry. The loader populates it; tests
// can hand-build one for unit coverage of the dispatcher.
func NewRegistry() *Registry {
	return &Registry{byEvent: map[Event][]Config{}}
}

// ReplaceAll atomically swaps the registry contents. Used by Load so a
// settings-file reload doesn't expose a partially-written intermediate
// state.
func (r *Registry) ReplaceAll(byEvent map[Event][]Config) {
	if byEvent == nil {
		byEvent = map[Event][]Config{}
	}
	r.mu.Lock()
	r.byEvent = byEvent
	r.mu.Unlock()
}

// For returns the matcher configs for the given event in fire order:
// project first, then user. Returns nil if no hooks are configured.
//
// The slice is owned by the registry; callers must NOT mutate it.
func (r *Registry) For(e Event) []Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byEvent[e]
}

// HasAny reports whether any hooks are configured for e. Useful for
// fast-path checks so the agent loop can skip building a payload when
// no hook would fire anyway.
func (r *Registry) HasAny(e Event) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byEvent[e]) > 0
}
