package config

import "sync"

// Get returns a process-wide Config initialized lazily via LoadDefault.
// Subsequent calls return the same pointer.
//
// Get is a host-side convenience for the bundled cmd/evva binary and for
// the reference TUI (which reads runtime settings without an injected
// pointer). Library code inside the agent loop and tools must NOT call
// Get — they receive *Config through dependency injection (agent.WithConfig,
// toolset.ToolState.SetConfig, function parameters).
//
// Downstream hosts that want a non-default AppHome should call Load with
// explicit LoadOptions and pass the result into agent.WithConfig.
func Get() *Config {
	getOnce.Do(func() {
		getInstance = LoadDefault()
	})
	return getInstance
}

var (
	getOnce     sync.Once
	getInstance *Config
)
