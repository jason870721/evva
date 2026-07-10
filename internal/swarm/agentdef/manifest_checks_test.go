package agentdef

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestManifestVerifyChecksKnob covers the CHK config surface: absent = off,
// happy parse, the default timeout, and the fail-fast rejections.
func TestManifestVerifyChecksKnob(t *testing.T) {
	t.Run("absent_is_off", func(t *testing.T) {
		m, err := LoadManifest(writeManifest(t, `
leader: {agent: lead}
`))
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		if m.Settings.VerifyChecks != nil {
			t.Fatalf("VerifyChecks = %+v, want nil (feature off)", m.Settings.VerifyChecks)
		}
	})

	t.Run("happy", func(t *testing.T) {
		m, err := LoadManifest(writeManifest(t, `
leader: {agent: lead}
settings:
  verify_checks:
    command: "go build ./... && go test ./..."
    timeout: 5m
`))
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		c := m.Settings.VerifyChecks
		if c == nil || c.Command != "go build ./... && go test ./..." || c.Timeout != 5*time.Minute {
			t.Fatalf("VerifyChecks = %+v, want the configured command at 5m", c)
		}
	})

	t.Run("default_timeout", func(t *testing.T) {
		m, err := LoadManifest(writeManifest(t, `
leader: {agent: lead}
settings:
  verify_checks: {command: "make check"}
`))
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		if got := m.Settings.VerifyChecks.Timeout; got != DefaultCheckTimeout {
			t.Fatalf("timeout = %s, want default %s", got, DefaultCheckTimeout)
		}
	})

	t.Run("rejections", func(t *testing.T) {
		for name, yml := range map[string]string{
			"empty_command": `
leader: {agent: lead}
settings:
  verify_checks: {timeout: 1m}
`,
			"zero_timeout": `
leader: {agent: lead}
settings:
  verify_checks: {command: "make check", timeout: "0"}
`,
			"negative_timeout": `
leader: {agent: lead}
settings:
  verify_checks: {command: "make check", timeout: -1m}
`,
			"over_max_timeout": `
leader: {agent: lead}
settings:
  verify_checks: {command: "make check", timeout: 11m}
`,
			"garbage_timeout": `
leader: {agent: lead}
settings:
  verify_checks: {command: "make check", timeout: soon}
`,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := LoadManifest(writeManifest(t, yml))
				if err == nil || !strings.Contains(err.Error(), "verify_checks") {
					t.Fatalf("err = %v, want a settings.verify_checks rejection", err)
				}
			})
		}
	})
}

// TestManifestVerifyChecksRoundTrip: WriteManifest emits the block whole; the
// default timeout is omitted and reloads as the default.
func TestManifestVerifyChecksRoundTrip(t *testing.T) {
	for name, spec := range map[string]*CheckSpec{
		"custom_timeout":  {Command: "go test ./...", Timeout: 5 * time.Minute},
		"default_timeout": {Command: "go test ./...", Timeout: DefaultCheckTimeout},
		"off":             nil,
	} {
		t.Run(name, func(t *testing.T) {
			m := Manifest{Leader: Member{Agent: "lead"}}
			m.Settings.VerifyChecks = spec
			p := filepath.Join(t.TempDir(), "evva-swarm.yml")
			if err := WriteManifest(p, m); err != nil {
				t.Fatalf("WriteManifest: %v", err)
			}
			got, err := LoadManifest(p)
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			c := got.Settings.VerifyChecks
			switch {
			case spec == nil:
				if c != nil {
					t.Fatalf("round-trip grew a checks block: %+v", c)
				}
			case c == nil || c.Command != spec.Command || c.Timeout != spec.Timeout:
				t.Fatalf("round-trip = %+v, want %+v", c, spec)
			}
		})
	}
}
