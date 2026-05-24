package hooks

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCommand_Exit0(t *testing.T) {
	script := filepath.Join(t.TempDir(), "hook.sh")
	writeScript(t, script, `#!/bin/sh
echo '{"continue":true}'
exit 0
`)

	ctx := context.Background()
	res := runCommand(ctx, slog.Default(), Command{Command: script, Timeout: 5}, []byte(`{}`), os.Environ(), 30*time.Second)
	if res.exitCode != 0 {
		t.Errorf("expected exit 0, got %d", res.exitCode)
	}
	d := parseDecision(res.stdout)
	if d.Continue == nil || !*d.Continue {
		t.Errorf("expected continue=true, got %+v", d)
	}
}

func TestRunCommand_Exit1(t *testing.T) {
	script := filepath.Join(t.TempDir(), "hook.sh")
	writeScript(t, script, `#!/bin/sh
echo "oops" >&2
exit 1
`)

	ctx := context.Background()
	res := runCommand(ctx, slog.Default(), Command{Command: script, Timeout: 5}, []byte(`{}`), os.Environ(), 30*time.Second)
	if res.exitCode != 1 {
		t.Errorf("expected exit 1, got %d", res.exitCode)
	}
}

func TestRunCommand_Exit2(t *testing.T) {
	script := filepath.Join(t.TempDir(), "hook.sh")
	writeScript(t, script, `#!/bin/sh
echo "block reason" >&2
exit 2
`)

	ctx := context.Background()
	res := runCommand(ctx, slog.Default(), Command{Command: script, Timeout: 5}, []byte(`{}`), os.Environ(), 30*time.Second)
	if res.exitCode != 2 {
		t.Errorf("expected exit 2, got %d", res.exitCode)
	}
	reason := extractReason(res, "fallback")
	if reason != "block reason" {
		t.Errorf("expected block reason 'block reason', got %q", reason)
	}
}

func TestRunCommand_Timeout(t *testing.T) {
	script := filepath.Join(t.TempDir(), "hook.sh")
	writeScript(t, script, `#!/bin/sh
sleep 10
`)

	ctx := context.Background()
	res := runCommand(ctx, slog.Default(), Command{Command: script, Timeout: 1}, []byte(`{}`), os.Environ(), 30*time.Second)
	if !res.timedOut {
		t.Error("expected timeout")
	}
}

func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
