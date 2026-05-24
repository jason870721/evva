package hooks

import (
	"encoding/json"
	"testing"
)

func TestParseDecision(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   Decision
	}{
		{"empty string", "", Decision{}},
		{"non-json", "hello", Decision{}},
		{"empty json", "{}", Decision{}},
		{"continue false", `{"continue":false}`, Decision{Continue: boolPtr(false)}},
		{"continue true", `{"continue":true}`, Decision{Continue: boolPtr(true)}},
		{"decision block", `{"decision":"block"}`, Decision{Decision: "block"}},
		{"decision approve", `{"decision":"approve"}`, Decision{Decision: "approve"}},
		{"reason", `{"reason":"test reason"}`, Decision{Reason: "test reason"}},
		{"systemMessage", `{"systemMessage":"msg"}`, Decision{SystemMessage: "msg"}},
		{"hookSpecificOutput", `{"hookSpecificOutput":{"additionalContext":"ctx"}}`, Decision{HookSpecificOutput: map[string]any{"additionalContext": "ctx"}}},
		{"unknown keys tolerated", `{"foo":"bar"}`, Decision{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDecision([]byte(tt.stdout))
			if !decisionsEqual(got, tt.want) {
				t.Errorf("parseDecision(%q) = %+v, want %+v", tt.stdout, got, tt.want)
			}
		})
	}
}

func TestApplyPreToolUse(t *testing.T) {
	t.Run("block via continue false", func(t *testing.T) {
		acc := &PreToolUseDecision{}
		stop := applyPreToolUse(acc, Decision{Continue: boolPtr(false), Reason: "nope"})
		if !stop || !acc.Blocked || acc.BlockReason != "nope" {
			t.Errorf("expected blocked, got acc=%+v stop=%v", acc, stop)
		}
	})

	t.Run("block via decision=block", func(t *testing.T) {
		acc := &PreToolUseDecision{}
		stop := applyPreToolUse(acc, Decision{Decision: "block", Reason: "blocked"})
		if !stop || !acc.Blocked || acc.BlockReason != "blocked" {
			t.Errorf("expected blocked, got acc=%+v stop=%v", acc, stop)
		}
	})

	t.Run("decision approve maps to allow", func(t *testing.T) {
		acc := &PreToolUseDecision{}
		stop := applyPreToolUse(acc, Decision{Decision: "approve"})
		if stop || acc.PermissionDecision != "allow" {
			t.Errorf("expected allow, got acc=%+v stop=%v", acc, stop)
		}
	})

	t.Run("hookSpecificOutput.permissionDecision beats top-level", func(t *testing.T) {
		acc := &PreToolUseDecision{}
		applyPreToolUse(acc, Decision{Decision: "approve", HookSpecificOutput: map[string]any{"permissionDecision": "deny"}})
		if acc.PermissionDecision != "deny" {
			t.Errorf("expected deny from hookSpecificOutput, got %q", acc.PermissionDecision)
		}
	})

	t.Run("updatedInput last-write-wins", func(t *testing.T) {
		acc := &PreToolUseDecision{}
		applyPreToolUse(acc, Decision{HookSpecificOutput: map[string]any{"updatedInput": map[string]any{"x": 1}}})
		applyPreToolUse(acc, Decision{HookSpecificOutput: map[string]any{"updatedInput": map[string]any{"x": 2}}})
		var out map[string]json.Number
		if err := json.Unmarshal(acc.UpdatedInput, &out); err != nil || out["x"] != "2" {
			t.Errorf("expected updatedInput x=2, got %s (err=%v)", acc.UpdatedInput, err)
		}
	})

	t.Run("additionalContext concatenates", func(t *testing.T) {
		acc := &PreToolUseDecision{}
		applyPreToolUse(acc, Decision{HookSpecificOutput: map[string]any{"additionalContext": "first"}})
		applyPreToolUse(acc, Decision{HookSpecificOutput: map[string]any{"additionalContext": "second"}})
		if acc.AdditionalContext != "first\nsecond" {
			t.Errorf("expected concatenated context, got %q", acc.AdditionalContext)
		}
	})
}

func TestExtractAdditionalContext(t *testing.T) {
	if got := extractAdditionalContext(Decision{}); got != "" {
		t.Errorf("empty → %q", got)
	}
	d := Decision{HookSpecificOutput: map[string]any{"additionalContext": "hello"}}
	if got := extractAdditionalContext(d); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestExtractInitialUserMessage(t *testing.T) {
	if got := extractInitialUserMessage(Decision{}); got != "" {
		t.Errorf("empty → %q", got)
	}
	d := Decision{HookSpecificOutput: map[string]any{"initialUserMessage": "welcome"}}
	if got := extractInitialUserMessage(d); got != "welcome" {
		t.Errorf("got %q", got)
	}
}

func TestIsBlock(t *testing.T) {
	b, r := isBlock(Decision{})
	if b || r != "" {
		t.Errorf("empty should not block")
	}
	b, r = isBlock(Decision{Continue: boolPtr(false), Reason: "no"})
	if !b || r != "no" {
		t.Errorf("continue=false should block")
	}
	b, r = isBlock(Decision{Decision: "block", Reason: "nope"})
	if !b || r != "nope" {
		t.Errorf("decision=block should block")
	}
}

func boolPtr(b bool) *bool { return &b }

func decisionsEqual(a, b Decision) bool {
	// Compare Continue pointers
	if (a.Continue == nil) != (b.Continue == nil) {
		return false
	}
	if a.Continue != nil && *a.Continue != *b.Continue {
		return false
	}
	if a.Decision != b.Decision || a.Reason != b.Reason || a.SystemMessage != b.SystemMessage {
		return false
	}
	if len(a.HookSpecificOutput) != len(b.HookSpecificOutput) {
		return false
	}
	for k, av := range a.HookSpecificOutput {
		bv, ok := b.HookSpecificOutput[k]
		if !ok {
			return false
		}
		aj, _ := json.Marshal(av)
		bj, _ := json.Marshal(bv)
		if string(aj) != string(bj) {
			return false
		}
	}
	return true
}
