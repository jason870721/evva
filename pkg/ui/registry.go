package ui

import (
	"sort"
	"sync"
)

// Factory builds a UI given the user's config home (EVVA_HOME). One is
// registered per UI implementation so the host can pick one at startup by
// name (e.g. `evva -tui bubbletea`). The signature is deliberately uniform
// — every UI takes the config dir and nothing else, so the selection flag
// can construct any of them the same way; a UI that needs more reads it
// from its own config under evvaHome.
type Factory func(evvaHome string) UI

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds (or replaces) a named UI factory. Intended to be called
// from a UI package's init() so a blank import wires it in:
//
//	import _ "github.com/johnny1110/evva/pkg/ui/bubbletea" // registers "bubbletea"
//
// Safe for concurrent use.
func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = f
}

// Lookup returns the factory registered under name and whether it existed.
func Lookup(name string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// Names returns the registered UI names in sorted order — for the -tui
// help text and "unknown UI" error messages.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
