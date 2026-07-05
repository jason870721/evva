package agentdef

import (
	"os"
	"path/filepath"
	"testing"
)

// TestShippedExamplesLoad walks every example swarm in examples/evva-swarm/
// through the exact register-time path (LoadManifest + BuildAll) so a shipped
// example can never rot silently — a renamed knob, a missing agent dir, or a
// bad profile fails CI here instead of a user's first `evva swarm .`.
func TestShippedExamplesLoad(t *testing.T) {
	root := filepath.Join("..", "..", "..", "examples", "evva-swarm")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		manifest := filepath.Join(dir, "evva-swarm.yml")
		if _, err := os.Stat(manifest); err != nil {
			continue // not a swarm example (docs, assets)
		}
		found++
		t.Run(e.Name(), func(t *testing.T) {
			m, err := LoadManifest(manifest)
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			loaded, warns, err := NewLoader().BuildAll(dir, m)
			if err != nil {
				t.Fatalf("BuildAll: %v", err)
			}
			for _, w := range warns {
				t.Logf("warning: %v", w)
			}
			if len(loaded) != 1+len(m.Workers) {
				t.Fatalf("loaded %d members, manifest declares %d", len(loaded), 1+len(m.Workers))
			}
		})
	}
	if found < 6 {
		t.Fatalf("only %d example manifests found under %s — path drift?", found, root)
	}
}
