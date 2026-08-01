package agentdef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSandboxLayering(t *testing.T) {
	cases := []struct {
		name         string
		spaceDefault bool
		override     string
		want         bool
	}{
		{"inherit off", false, "", false},
		{"inherit on", true, "", true},
		{"member opts in", false, SandboxOn, true},
		{"member opts out of a sandboxed space", true, SandboxOff, false},
		{"whitespace is still an override", false, "  on  ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSandbox(tc.spaceDefault, tc.override); got != tc.want {
				t.Errorf("ResolveSandbox(%v, %q) = %v, want %v", tc.spaceDefault, tc.override, got, tc.want)
			}
		})
	}
}

// writeManifest lives in manifest_test.go — reused here.

func TestManifestParsesSandboxFields(t *testing.T) {
	p := writeManifest(t, `
name: demo
workdir: .
leader:
  agent: boss
workers:
  - agent: coder
    sandbox: "on"
  - agent: writer
    sandbox: "off"
  - agent: plain
settings:
  sandbox: true
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if !m.Settings.Sandbox {
		t.Error("settings.sandbox should be true")
	}
	want := map[string]string{"coder": SandboxOn, "writer": SandboxOff, "plain": ""}
	for _, w := range m.Workers {
		if got := w.Sandbox; got != want[w.Agent] {
			t.Errorf("%s: Sandbox = %q, want %q", w.Agent, got, want[w.Agent])
		}
	}
}

func TestManifestRejectsBadSandboxValue(t *testing.T) {
	p := writeManifest(t, `
name: demo
workdir: .
leader:
  agent: boss
workers:
  - agent: coder
    sandbox: maybe
`)
	_, err := LoadManifest(p)
	if err == nil {
		t.Fatal("want an error for an invalid sandbox value")
	}
	if !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("error should name the field, got: %v", err)
	}
}

// D8 parity: sandboxing implies a worktree, so allowing it on the leader would
// smuggle in the isolation the worktree guard already forbids.
func TestManifestRejectsLeaderSandbox(t *testing.T) {
	p := writeManifest(t, `
name: demo
workdir: .
leader:
  agent: boss
  sandbox: "on"
workers:
  - agent: coder
`)
	_, err := LoadManifest(p)
	if err == nil {
		t.Fatal("want an error for leader.sandbox: on")
	}
	if !strings.Contains(err.Error(), "leader.sandbox") {
		t.Errorf("error should name leader.sandbox, got: %v", err)
	}
}

// The leader may still explicitly opt OUT — only "on" is refused.
func TestManifestAllowsLeaderSandboxOff(t *testing.T) {
	p := writeManifest(t, `
name: demo
workdir: .
leader:
  agent: boss
  sandbox: "off"
workers:
  - agent: coder
settings:
  sandbox: true
`)
	if _, err := LoadManifest(p); err != nil {
		t.Fatalf("leader.sandbox: off should be accepted: %v", err)
	}
}

func TestManifestSandboxRoundTrips(t *testing.T) {
	p := writeManifest(t, `
name: demo
workdir: .
leader:
  agent: boss
workers:
  - agent: coder
    sandbox: "on"
settings:
  sandbox: true
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	out := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	back, err := LoadManifest(out)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if !back.Settings.Sandbox {
		t.Error("settings.sandbox lost in round trip")
	}
	if len(back.Workers) != 1 || back.Workers[0].Sandbox != SandboxOn {
		t.Errorf("member sandbox lost in round trip: %+v", back.Workers)
	}
}

// A manifest that never mentions sandboxing must round-trip without gaining
// the key — the off default stays invisible.
func TestManifestSandboxOmittedWhenOff(t *testing.T) {
	p := writeManifest(t, `
name: demo
workdir: .
leader:
  agent: boss
workers:
  - agent: coder
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	out := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sandbox") {
		t.Errorf("an unused knob should not be written:\n%s", raw)
	}
}
