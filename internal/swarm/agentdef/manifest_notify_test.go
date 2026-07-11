package agentdef

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestManifestNotifyKnob covers the NTF config surface: absent = off, happy
// parse with defaults, and the fail-fast rejections.
func TestManifestNotifyKnob(t *testing.T) {
	t.Run("absent_is_off", func(t *testing.T) {
		m, err := LoadManifest(writeManifest(t, `
leader: {agent: lead}
`))
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		if m.Settings.Notify != nil {
			t.Fatalf("Notify = %+v, want nil (feature off)", m.Settings.Notify)
		}
	})

	t.Run("happy_defaults", func(t *testing.T) {
		m, err := LoadManifest(writeManifest(t, `
leader: {agent: lead}
settings:
  notify:
    url: "https://hooks.slack.com/services/T/B/x"
`))
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		n := m.Settings.Notify
		if n == nil || n.URL != "https://hooks.slack.com/services/T/B/x" {
			t.Fatalf("Notify = %+v, want the url", n)
		}
		if n.Format != NotifyFormatJSON || n.RateLimit != DefaultNotifyRateLimit || len(n.Events) != 0 {
			t.Fatalf("defaults = %+v, want json/12/all-groups", n)
		}
	})

	t.Run("full_block", func(t *testing.T) {
		m, err := LoadManifest(writeManifest(t, `
leader: {agent: lead}
settings:
  notify:
    url: "https://x.example/hook"
    format: slack
    secret: s3cret
    events: [gates, alerts, gates]
    command: "notify-send evva"
    rate_limit: 5
`))
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		n := m.Settings.Notify
		if n.Format != NotifyFormatSlack || n.Secret != "s3cret" || n.Command != "notify-send evva" || n.RateLimit != 5 {
			t.Fatalf("full block = %+v", n)
		}
		if !reflect.DeepEqual(n.Events, []string{"gates", "alerts"}) {
			t.Fatalf("events = %v, want deduped [gates alerts]", n.Events)
		}
	})

	t.Run("rejections", func(t *testing.T) {
		for name, yml := range map[string]string{
			"no_target": `
leader: {agent: lead}
settings:
  notify: {format: json}
`,
			"bad_format": `
leader: {agent: lead}
settings:
  notify: {url: "https://x", format: pigeon}
`,
			"bad_group": `
leader: {agent: lead}
settings:
  notify: {url: "https://x", events: [gates, vibes]}
`,
			"negative_rate": `
leader: {agent: lead}
settings:
  notify: {url: "https://x", rate_limit: -1}
`,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := LoadManifest(writeManifest(t, yml))
				if err == nil || !strings.Contains(err.Error(), "notify") {
					t.Fatalf("err = %v, want a settings.notify rejection", err)
				}
			})
		}
	})
}

// TestManifestNotifyRoundTrip: WriteManifest emits the block whole; defaults
// (json format, default rate) are omitted and reload as themselves.
func TestManifestNotifyRoundTrip(t *testing.T) {
	for name, spec := range map[string]*NotifySpec{
		"webhook_defaults": {URL: "https://x.example/hook", Format: NotifyFormatJSON, RateLimit: DefaultNotifyRateLimit},
		"slack_custom":     {URL: "https://hooks.slack.example/T", Format: NotifyFormatSlack, Secret: "s", Events: []string{"alerts"}, RateLimit: 3},
		"command_only":     {Command: "notify-send evva", Format: NotifyFormatJSON, RateLimit: DefaultNotifyRateLimit},
		"off":              nil,
	} {
		t.Run(name, func(t *testing.T) {
			m := Manifest{Leader: Member{Agent: "lead"}}
			m.Settings.Notify = spec
			p := filepath.Join(t.TempDir(), "evva-swarm.yml")
			if err := WriteManifest(p, m); err != nil {
				t.Fatalf("WriteManifest: %v", err)
			}
			got, err := LoadManifest(p)
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			n := got.Settings.Notify
			switch {
			case spec == nil:
				if n != nil {
					t.Fatalf("round-trip grew a notify block: %+v", n)
				}
			case !reflect.DeepEqual(n, spec):
				t.Fatalf("round-trip = %+v, want %+v", n, spec)
			}
		})
	}
}
