package hooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// newTestDispatcher builds a Dispatcher backed by a hand-built registry.
// baseFn returns a static BasePayload so tests don't depend on real agent state.
func newTestDispatcher(t *testing.T, byEvent map[Event][]Config) *Dispatcher {
	t.Helper()
	reg := NewRegistry()
	reg.ReplaceAll(byEvent)
	return NewDispatcher(reg, slog.Default(), func() BasePayload {
		return BasePayload{SessionID: "test-session", Cwd: "/tmp", AgentID: "test-agent"}
	}, t.TempDir())
}

// echoScript writes a shell script that echoes its args and returns it as the
// Command string. scriptDir must already exist.
func echoScript(t *testing.T, dir, name, jsonOutput string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+jsonOutput+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDispatcher_FirePreToolUse_Block(t *testing.T) {
	script := echoScript(t, t.TempDir(), "block.sh", `{"decision":"block","reason":"nope"}`)

	d := newTestDispatcher(t, map[Event][]Config{
		EventPreToolUse: {{Matcher: "bash", Hooks: []Command{{Type: TypeCommand, Command: script, Timeout: 5}}}},
	})

	dec, err := d.FirePreToolUse(context.Background(), "bash", []byte(`{}`), "id-1")
	if err != nil {
		t.Fatal(err)
	}
	if dec == nil || !dec.Blocked {
		t.Fatal("expected blocked")
	}
	if dec.BlockReason != "nope" {
		t.Errorf("expected reason 'nope', got %q", dec.BlockReason)
	}
}

func TestDispatcher_FirePreToolUse_UpdatedInput(t *testing.T) {
	script := echoScript(t, t.TempDir(), "mutate.sh",
		`{"hookSpecificOutput":{"updatedInput":{"command":"echo hello"}}}`)

	d := newTestDispatcher(t, map[Event][]Config{
		EventPreToolUse: {{Matcher: "bash", Hooks: []Command{{Type: TypeCommand, Command: script, Timeout: 5}}}},
	})

	dec, err := d.FirePreToolUse(context.Background(), "bash", []byte(`{"command":"ls"}`), "id-2")
	if err != nil {
		t.Fatal(err)
	}
	if dec == nil {
		t.Fatal("expected decision")
	}
	if dec.Blocked {
		t.Error("should not be blocked")
	}
	var out map[string]string
	if err := json.Unmarshal(dec.UpdatedInput, &out); err != nil {
		t.Fatalf("failed to unmarshal updatedInput: %v", err)
	}
	if out["command"] != "echo hello" {
		t.Errorf("expected command=echo hello, got %q", out["command"])
	}
}

func TestDispatcher_FirePreToolUse_PermissionDecision(t *testing.T) {
	script := echoScript(t, t.TempDir(), "allow.sh",
		`{"hookSpecificOutput":{"permissionDecision":"allow"}}`)

	d := newTestDispatcher(t, map[Event][]Config{
		EventPreToolUse: {{Matcher: "bash", Hooks: []Command{{Type: TypeCommand, Command: script, Timeout: 5}}}},
	})

	dec, err := d.FirePreToolUse(context.Background(), "bash", []byte(`{}`), "id-3")
	if err != nil {
		t.Fatal(err)
	}
	if dec == nil {
		t.Fatal("expected decision")
	}
	if dec.PermissionDecision != "allow" {
		t.Errorf("expected allow, got %q", dec.PermissionDecision)
	}
}

func TestDispatcher_FirePostToolUse(t *testing.T) {
	script := echoScript(t, t.TempDir(), "post.sh",
		`{"hookSpecificOutput":{"additionalContext":"[extra context]"}}`)

	d := newTestDispatcher(t, map[Event][]Config{
		EventPostToolUse: {{Matcher: "", Hooks: []Command{{Type: TypeCommand, Command: script, Timeout: 5}}}},
	})

	addCtx, err := d.FirePostToolUse(context.Background(), "bash", []byte(`{}`), "tool result", "id-4", false)
	if err != nil {
		t.Fatal(err)
	}
	if addCtx != "[extra context]" {
		t.Errorf("expected '[extra context]', got %q", addCtx)
	}
}

func TestDispatcher_FireUserPromptSubmit_Block(t *testing.T) {
	script := echoScript(t, t.TempDir(), "block-prompt.sh",
		`{"continue":false,"reason":"prompt blocked"}`)

	d := newTestDispatcher(t, map[Event][]Config{
		EventUserPromptSubmit: {{Hooks: []Command{{Type: TypeCommand, Command: script, Timeout: 5}}}},
	})

	addCtx, blocked, reason, err := d.FireUserPromptSubmit(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Error("expected blocked")
	}
	if reason != "prompt blocked" {
		t.Errorf("expected 'prompt blocked', got %q", reason)
	}
	if addCtx != "" {
		t.Errorf("expected empty addCtx when blocked, got %q", addCtx)
	}
}

func TestDispatcher_FireStop_ReentryGuard(t *testing.T) {
	script := echoScript(t, t.TempDir(), "stop.sh",
		`{"decision":"block","reason":"not done yet"}`)

	d := newTestDispatcher(t, map[Event][]Config{
		EventStop: {{Hooks: []Command{{Type: TypeCommand, Command: script, Timeout: 5}}}},
	})

	// First call: should block
	blocked, reason, err := d.FireStop(context.Background(), "last msg", false)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked || reason != "not done yet" {
		t.Errorf("expected blocked on first call, got blocked=%v reason=%q", blocked, reason)
	}

	// Second call with stopHookActive=true: should NOT block
	blocked, _, err = d.FireStop(context.Background(), "last msg", true)
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Error("expected NOT blocked when stopHookActive=true")
	}
}

func TestDispatcher_NoHooks_ReturnsNil(t *testing.T) {
	d := newTestDispatcher(t, nil)

	dec, err := d.FirePreToolUse(context.Background(), "bash", []byte(`{}`), "id")
	if err != nil || dec != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", dec, err)
	}

	addCtx, err := d.FirePostToolUse(context.Background(), "bash", nil, "", "id", false)
	if err != nil || addCtx != "" {
		t.Errorf("expected ('', nil), got (%q, %v)", addCtx, err)
	}
}

func TestDispatcher_NilDispatcher(t *testing.T) {
	var d *Dispatcher

	if d.Has(EventPreToolUse) {
		t.Error("nil dispatcher should return false for Has")
	}

	dec, err := d.FirePreToolUse(context.Background(), "bash", nil, "id")
	if err != nil || dec != nil {
		t.Errorf("nil dispatcher should return (nil, nil), got (%v, %v)", dec, err)
	}

	d.FireNotification(context.Background(), "msg", "title", "test")
	// Should not panic
}
