package agentdef

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadManifestHappy(t *testing.T) {
	m, err := LoadManifest(filepath.Join("testdata", "evva-swarm.yml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Name != "test-eng-team" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Leader.Agent != "leader" {
		t.Errorf("Leader = %q", m.Leader.Agent)
	}
	want := []Member{{Agent: "backend-dev"}, {Agent: "frontend-dev"}}
	if !reflect.DeepEqual(m.Workers, want) {
		t.Errorf("Workers = %+v, want %+v", m.Workers, want)
	}
	if m.Settings.PermissionMode != "default" || m.Settings.MaxIterations != 50 {
		t.Errorf("Settings = %+v", m.Settings)
	}
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadManifestMissingFile(t *testing.T) {
	if _, err := LoadManifest(filepath.Join("testdata", "nope.yml")); err == nil {
		t.Fatal("want error for missing manifest")
	}
}

func TestLoadManifestDuplicateWorker(t *testing.T) {
	p := writeManifest(t, `
name: dup
leader:
  agent: leader
workers:
  - agent: eng
  - agent: eng
`)
	_, err := LoadManifest(p)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want a duplicate-name error", err)
	}
}

func TestLoadManifestWorkerCollidesWithLeader(t *testing.T) {
	p := writeManifest(t, `
name: dup
leader:
  agent: leader
workers:
  - agent: leader
`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("want error when a worker reuses the leader's name")
	}
}

func TestLoadManifestMissingLeader(t *testing.T) {
	p := writeManifest(t, `
name: noleader
workers:
  - agent: eng
`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("want error when leader.agent is missing")
	}
}

func TestLoadManifestEmptyWorkerName(t *testing.T) {
	p := writeManifest(t, `
name: empty
leader:
  agent: leader
workers:
  - agent: ""
`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("want error for an empty worker name")
	}
}

// A missing name is now ACCEPTED (Docker-style): the service assigns a handle
// (--name > manifest name > generated), so the manifest no longer requires one.
func TestLoadManifestMissingNameIsAllowed(t *testing.T) {
	p := writeManifest(t, `
leader:
  agent: leader
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("a nameless manifest should load, got %v", err)
	}
	if m.Name != "" {
		t.Fatalf("Name = %q, want empty (service assigns the handle)", m.Name)
	}
}
