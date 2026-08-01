package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

// Registry maps a session's host workdir onto the container serving it.
//
// # Why a registry rather than a field
//
// The bash tool learns its workdir at construction (NewBashWithHost captures
// ToolState.Workdir()) and knows nothing else about the session that owns it.
// Meanwhile a container is provisioned much later and by a different layer —
// the subagent spawner, or the swarm's member constructor — and several
// sandboxed sessions coexist in one evva process. Keying on the workdir is
// what lets those two layers meet without threading a handle through the
// config clone, the toolset and the daemon host.
//
// Lookup is longest-prefix, not exact: a session's workdir may sit anywhere
// under the mounted root (the swarm mounts a member's whole worktree but sets
// the member's workdir to the position of its space inside that tree), and
// nested sandboxes must resolve to the innermost one.
type Registry struct {
	mu sync.RWMutex
	m  map[string]*Container
}

// Default is the process-wide registry the production wiring uses. Tests
// construct their own Registry instead of touching this.
var Default = &Registry{}

// Register binds every workdir at or below root to c. Returns a release
// function; calling it is how a session guarantees it stops claiming paths
// even if teardown of the container itself fails.
func (r *Registry) Register(root string, c *Container) func() {
	key := normalize(root)
	if key == "" || c == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.m == nil {
		r.m = map[string]*Container{}
	}
	r.m[key] = c
	r.mu.Unlock()
	return func() { r.Unregister(root) }
}

// Unregister drops the binding for root.
func (r *Registry) Unregister(root string) {
	key := normalize(root)
	r.mu.Lock()
	delete(r.m, key)
	r.mu.Unlock()
}

// Lookup returns the innermost container whose root contains workdir, or nil
// when the path is not sandboxed. nil is the overwhelmingly common answer —
// sandboxing is opt-in — so this stays allocation-free on the hot path.
func (r *Registry) Lookup(workdir string) *Container {
	key := normalize(workdir)
	if key == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.m) == 0 {
		return nil
	}
	var best string
	var found *Container
	for root, c := range r.m {
		if !under(key, root) {
			continue
		}
		if len(root) > len(best) {
			best, found = root, c
		}
	}
	return found
}

// StopAll tears down every registered container and empties the registry. Used
// on process shutdown so an interrupted run does not leave containers behind;
// --rm covers the crash case, this covers the orderly one.
func (r *Registry) StopAll(ctx context.Context) {
	r.mu.Lock()
	live := r.m
	r.m = nil
	r.mu.Unlock()
	for _, c := range live {
		_ = c.Stop(ctx)
	}
}

// Len reports how many sandboxed sessions are live. For observability.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.m)
}

// Provision starts a container for a session rooted at hostRoot and binds it
// in the Default registry, returning the release function that tears both
// down. It is the one entry point production callers use, so the "start it,
// then register it, and never leave one without the other" pairing lives in a
// single place rather than being re-derived by the subagent spawner and the
// swarm member constructor independently.
//
// Returns (nil, no-op, nil) when runtime is empty — sandboxing is off, which
// is the default and not an error.
func Provision(ctx context.Context, runtime, image, network, hostRoot, label string) (*Container, func(), error) {
	if runtime == "" {
		return nil, func() {}, nil
	}
	c, err := Start(ctx, Options{
		Runtime:  runtime,
		Image:    image,
		Network:  network,
		HostRoot: hostRoot,
		Label:    label,
	})
	if err != nil {
		return nil, func() {}, err
	}
	unregister := Default.Register(hostRoot, c)
	return c, func() {
		unregister()
		_ = c.Stop(ctx)
	}, nil
}

func normalize(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	// Symlink resolution matters on macOS, where /tmp is a symlink to
	// /private/tmp: a worktree registered under one spelling would otherwise
	// never match a workdir carrying the other.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func under(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
