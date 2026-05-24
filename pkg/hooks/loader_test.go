package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeEvvaSettings(t *testing.T, workdir, content string) {
	t.Helper()
	writeFile(t, filepath.Join(workdir, ".evva"), "settings.json", content)
}

func writeHomeSettings(t *testing.T, home, content string) {
	t.Helper()
	writeFile(t, home, "settings.json", content)
}

func TestLoad_MissingFile(t *testing.T) {
	reg, warns := Load(t.TempDir(), t.TempDir())
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
	if reg.HasAny(EventPreToolUse) {
		t.Error("expected empty registry")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `not json`)

	reg, warns := Load(workdir, "")
	if len(warns) == 0 || warns[0].Err == nil {
		t.Errorf("expected warning for invalid JSON, got %v", warns)
	}
	if reg.HasAny(EventPreToolUse) {
		t.Error("expected empty registry on parse failure")
	}
}

func TestLoad_UnknownEvent(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{"hooks":{"UnknownEvent":[{"hooks":[{"type":"command","command":"echo"}]}]}}`)

	_, warns := Load(workdir, "")
	if len(warns) == 0 {
		t.Fatal("expected warning for unknown event")
	}
	hasUnknown := false
	for _, w := range warns {
		if w.Path != "" {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Errorf("expected a warning about unknown event, got %v", warns)
	}
}

func TestLoad_BadMatcherGlob(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{"hooks":{"PreToolUse":[{"matcher":"[","hooks":[{"type":"command","command":"echo"}]}]}}`)

	_, warns := Load(workdir, "")
	if len(warns) == 0 {
		t.Fatal("expected warning for bad glob")
	}
}

func TestLoad_TypeCommandWithoutCommand(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command"}]}]}}`)

	_, warns := Load(workdir, "")
	found := false
	for _, w := range warns {
		if w.Err != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for type=command without command, got %v", warns)
	}
}

func TestLoad_TypeHTTPWithoutURL(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"http"}]}]}}`)

	_, warns := Load(workdir, "")
	found := false
	for _, w := range warns {
		if w.Err != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for type=http without url, got %v", warns)
	}
}

func TestLoad_TimeoutOutOfRange(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo","timeout":1000}]}]}}`)

	_, warns := Load(workdir, "")
	found := false
	for _, w := range warns {
		if w.Err != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for timeout out of range, got %v", warns)
	}
}

func TestLoad_HTTPAsyncDefaultsTrue(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"http","url":"http://example.com"}]}]}}`)

	reg, warns := Load(workdir, "")
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	cfgs := reg.For(EventPreToolUse)
	if len(cfgs) == 0 || len(cfgs[0].Hooks) == 0 {
		t.Fatal("expected hooks to be loaded")
	}
	if !cfgs[0].Hooks[0].Async {
		t.Error("expected async=true by default for HTTP hooks")
	}
}

func TestLoad_ProjectBeforeUserMergeOrder(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()

	// Project hooks fire first
	writeEvvaSettings(t, workdir, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo project"}]}]}}`)
	// User hooks fire second
	writeHomeSettings(t, home, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo user"}]}]}}`)

	reg, warns := Load(workdir, home)
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	cfgs := reg.For(EventPreToolUse)
	if len(cfgs) != 2 {
		t.Fatalf("expected 2 configs (project + user), got %d", len(cfgs))
	}
	if cfgs[0].Hooks[0].Command != "echo project" {
		t.Errorf("expected project hook first, got %q", cfgs[0].Hooks[0].Command)
	}
	if cfgs[1].Hooks[0].Command != "echo user" {
		t.Errorf("expected user hook second, got %q", cfgs[1].Hooks[0].Command)
	}
}

func TestLoad_ValidFile(t *testing.T) {
	workdir := t.TempDir()
	writeEvvaSettings(t, workdir, `{
		"hooks": {
			"PreToolUse": [{
				"matcher": "bash",
				"hooks": [{
					"type": "command",
					"command": "cat > /dev/null"
				}]
			}],
			"PostToolUse": [{
				"hooks": [{
					"type": "command",
					"command": "echo done"
				}]
			}]
		}
	}`)

	reg, warns := Load(workdir, "")
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if !reg.HasAny(EventPreToolUse) {
		t.Error("expected PreToolUse hooks")
	}
	if !reg.HasAny(EventPostToolUse) {
		t.Error("expected PostToolUse hooks")
	}
	if reg.HasAny(EventStop) {
		t.Error("expected no Stop hooks")
	}
}
