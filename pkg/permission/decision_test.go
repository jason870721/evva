package permission

import (
	"path/filepath"
	"testing"
)

func mkCall(name, cmd string) ToolCall {
	if cmd == "" {
		return ToolCall{Name: name}
	}
	return ToolCall{Name: name, Input: []byte(`{"command":"` + cmd + `"}`)}
}

func TestDecide_BypassAllowsEverything(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Behavior: BehaviorDeny, Source: SourceProject},
	})
	d := Decide(mkCall("bash", "rm -rf /"), ModeBypass, store, Hint{}, "", "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("bypass should allow even with deny rule; got %v", d.Behavior)
	}
}

func TestDecide_PlanModeBlocksWrites(t *testing.T) {
	d := Decide(mkCall("edit", ""), ModePlan, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("plan mode should deny edit; got %v", d.Behavior)
	}
	d = Decide(mkCall("read", ""), ModePlan, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("plan mode should allow read; got %v", d.Behavior)
	}
}

func TestDecide_PlanModeAllowsReadOnlyBash(t *testing.T) {
	// Read-only bash commands (ls, cat, git status, etc.) auto-allow in plan
	// mode via the classifier hint, so the model can inspect the codebase.
	tests := []struct {
		cmd  string
		hint Hint
		want Behavior
		desc string
	}{
		{"ls", Hint{IsReadOnly: true, Matched: "ls"}, BehaviorAllow, "read-only allow"},
		{"cat foo.go", Hint{IsReadOnly: true, Matched: "cat"}, BehaviorAllow, "read-only + matched"},
		{"git status", Hint{IsReadOnly: true, Matched: "git_status"}, BehaviorAllow, "read-only + matched"},
		{"do-something", Hint{}, BehaviorDeny, "unclassified deny"},
		{"mkdir foo", Hint{IsCommonFS: true, Matched: "mkdir"}, BehaviorDeny, "common-fs deny"},
		{"rm -rf /", Hint{IsDangerous: true, Matched: "rm"}, BehaviorDeny, "dangerous deny"},
	}

	for _, tt := range tests {
		d := Decide(mkCall("bash", tt.cmd), ModePlan, NewStore(), tt.hint, "", "")
		if d.Behavior != tt.want {
			t.Errorf("plan mode + bash %q: %s — wanted %v, got %v (%s)",
				tt.cmd, tt.desc, tt.want, d.Behavior, d.Reason)
		}
	}
}

func TestDecide_PlanModeDenyRuleBlocksReadOnlyBash(t *testing.T) {
	// A deny rule should still beat the read-only bash carve-out in plan mode.
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Behavior: BehaviorDeny, Source: SourceProject},
	})
	d := Decide(mkCall("bash", "ls"), ModePlan, store,
		Hint{IsReadOnly: true, Matched: "ls"}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule should beat plan-mode read-only bash carve-out; got %v", d.Behavior)
	}
}

func TestDecide_PlanModeAllowsPlanFileWrite(t *testing.T) {
	wd := t.TempDir()
	planPath := filepath.Join(wd, ".evva", "plans", "current.md")

	in := []byte(`{"file_path":"` + planPath + `","content":"# Plan"}`)
	call := ToolCall{Name: "write", Input: in}
	d := Decide(call, ModePlan, NewStore(), Hint{}, wd, "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("plan-mode write to plan file should allow; got %v (%s)", d.Behavior, d.Reason)
	}

	// Non-plan path still denies.
	otherPath := filepath.Join(wd, "main.go")
	in2 := []byte(`{"file_path":"` + otherPath + `","content":"package main"}`)
	d = Decide(ToolCall{Name: "write", Input: in2}, ModePlan, NewStore(), Hint{}, wd, "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("plan-mode write outside plan dir should deny; got %v", d.Behavior)
	}

	// Edit also honored.
	in3 := []byte(`{"file_path":"` + planPath + `","old_string":"a","new_string":"b"}`)
	d = Decide(ToolCall{Name: "edit", Input: in3}, ModePlan, NewStore(), Hint{}, wd, "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("plan-mode edit on plan file should allow; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_PlanModeCarveOutNeedsWorkdir(t *testing.T) {
	planPath := "/tmp/anywhere/.evva/plans/current.md"
	in := []byte(`{"file_path":"` + planPath + `"}`)
	// Empty workdir disables the carve-out — the call still denies.
	d := Decide(ToolCall{Name: "write", Input: in}, ModePlan, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("plan-mode + empty workdir should deny write; got %v", d.Behavior)
	}
}

func TestIsPlanFilePath_Cases(t *testing.T) {
	wd := t.TempDir()
	in := filepath.Join(wd, ".evva", "plans", "current.md")
	if !IsPlanFilePath(wd, in) {
		t.Errorf("inside path should match: %q under %q", in, wd)
	}

	out := filepath.Join(wd, "main.go")
	if IsPlanFilePath(wd, out) {
		t.Errorf("outside path should not match: %q under %q", out, wd)
	}

	// Plan dir itself (no file) is not a plan-file write target.
	if IsPlanFilePath(wd, filepath.Join(wd, ".evva", "plans")) {
		t.Errorf("plan-dir root should not match")
	}

	// Traversal attempt.
	traversal := filepath.Join(wd, ".evva", "plans", "..", "..", "etc", "passwd")
	if IsPlanFilePath(wd, traversal) {
		t.Errorf("traversal escape should not match: %q", traversal)
	}

	// Empty inputs.
	if IsPlanFilePath("", in) {
		t.Errorf("empty workdir should not match")
	}
	if IsPlanFilePath(wd, "") {
		t.Errorf("empty path should not match")
	}
}

func TestDecide_DenyWinsOverAllow(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "npm:*", Behavior: BehaviorAllow, Source: SourceProject},
		{ToolName: "bash", Behavior: BehaviorDeny, Source: SourceUser},
	})
	d := Decide(mkCall("bash", "npm install"), ModeDefault, store, Hint{}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule should beat allow rule; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_AskRule(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "rm:*", Behavior: BehaviorAsk, Source: SourceProject},
	})
	d := Decide(mkCall("bash", "rm -rf foo"), ModeDefault, store, Hint{}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("expected ask; got %v", d.Behavior)
	}
}

func TestDecide_AcceptEdits(t *testing.T) {
	d := Decide(mkCall("edit", ""), ModeAcceptEdits, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("accept_edits should allow edit; got %v", d.Behavior)
	}
	// Common-fs bash command auto-allows in accept_edits.
	d = Decide(mkCall("bash", "mkdir foo"), ModeAcceptEdits, NewStore(), Hint{IsCommonFS: true, Matched: "mkdir"}, "", "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("accept_edits should allow common-fs bash; got %v (%s)", d.Behavior, d.Reason)
	}
	// Dangerous / unclassified bash still asks even in accept_edits.
	d = Decide(mkCall("bash", "do-something"), ModeAcceptEdits, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("accept_edits should ask for unclassified bash; got %v", d.Behavior)
	}
}

func TestDecide_DefaultAutoAllowsSafeTools(t *testing.T) {
	// Read-only/self-coordination tools auto-allow in default mode.
	for _, name := range []string{"read", "tree", "agent", "todo_write", "tool_search", "enter_plan_mode", "exit_plan_mode", "daemon_list", "daemon_output"} {
		d := Decide(mkCall(name, ""), ModeDefault, NewStore(), Hint{}, "", "")
		if d.Behavior != BehaviorAllow {
			t.Errorf("default should auto-allow %q; got %v (%s)", name, d.Behavior, d.Reason)
		}
	}
}

func TestDecide_DefaultAllowsReadOnlyBash(t *testing.T) {
	// Bash with a read-only command (ls, cat, ...) auto-allows in default
	// mode via the classifier hint, so the user isn't prompted for every
	// directory listing.
	d := Decide(mkCall("bash", "ls"), ModeDefault, NewStore(), Hint{IsReadOnly: true, Matched: "ls"}, "", "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("default should allow read-only bash; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_DefaultAsksCommonFSBash(t *testing.T) {
	// Even common fs commands ask in default mode — they only auto-allow
	// in accept_edits.
	d := Decide(mkCall("bash", "mkdir foo"), ModeDefault, NewStore(), Hint{IsCommonFS: true, Matched: "mkdir"}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("default should ask for common-fs bash; got %v", d.Behavior)
	}
}

func TestDecide_AllowRule(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "git:*", Behavior: BehaviorAllow, Source: SourceProject},
	})
	d := Decide(mkCall("bash", "git status"), ModeDefault, store, Hint{}, "", "")
	if d.Behavior != BehaviorAllow {
		t.Errorf("git:* allow rule should match 'git status'; got %v", d.Behavior)
	}
}

func TestDecide_DangerousBashSurfacesHint(t *testing.T) {
	// Dangerous bash still asks (any non-bypass mode), but the Reason
	// includes the matched entry so the approval UI can show it.
	d := Decide(mkCall("bash", "eval foo"), ModeDefault, NewStore(), Hint{IsDangerous: true, Matched: "eval"}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("dangerous bash should ask; got %v", d.Behavior)
	}
	if d.Reason == "no matching rule" || d.Reason == "" {
		t.Errorf("expected dangerous hint surfaced in reason; got %q", d.Reason)
	}
}

func TestDecide_DefaultAsksUnclassifiedBash(t *testing.T) {
	// Bash with no classifier hint (model command we don't recognize)
	// asks under default — the safe-by-default stance.
	d := Decide(mkCall("bash", "do-something"), ModeDefault, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("default + unclassified bash should ask; got %v", d.Behavior)
	}
}

func TestDecide_SessionRuleBeatsUserRule(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Behavior: BehaviorDeny, Source: SourceUser},
	})
	store.AddSessionRule(Rule{ToolName: "bash", Content: "ls", Behavior: BehaviorAllow})

	// User deny rule is tool-wide; session allow is specific.
	// Pipeline: deny first → still hits the user-scope deny.
	// This test pins that behavior so we don't accidentally invert it
	// (deny precedence is the safety guarantee).
	d := Decide(mkCall("bash", "ls"), ModeDefault, store, Hint{}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule must win over session allow; got %v (%s)", d.Behavior, d.Reason)
	}
}

// --- config tool: read auto-allows, write asks (value-aware), plan denies writes ---

func cfgCall(input string) ToolCall {
	return ToolCall{Name: "config", Input: []byte(input)}
}

func TestDecide_ConfigGetAllows(t *testing.T) {
	get := cfgCall(`{"setting":"display_thinking"}`)
	for _, mode := range []Mode{ModeDefault, ModeAcceptEdits, ModePlan} {
		d := Decide(get, mode, NewStore(), Hint{}, "", "")
		if d.Behavior != BehaviorAllow {
			t.Errorf("config GET in %s: want allow, got %v (%s)", mode, d.Behavior, d.Reason)
		}
	}
}

func TestDecide_ConfigSetAsksWithMessage(t *testing.T) {
	set := cfgCall(`{"setting":"display_thinking","value":false}`)
	for _, mode := range []Mode{ModeDefault, ModeAcceptEdits} {
		d := Decide(set, mode, NewStore(), Hint{}, "", "")
		if d.Behavior != BehaviorAsk {
			t.Errorf("config SET in %s: want ask, got %v (%s)", mode, d.Behavior, d.Reason)
		}
		if d.Reason != "Set display_thinking to false" {
			t.Errorf("config SET reason = %q, want %q", d.Reason, "Set display_thinking to false")
		}
	}
}

func TestDecide_ConfigSetStringMessageUnquoted(t *testing.T) {
	d := Decide(cfgCall(`{"setting":"default_effort","value":"high"}`), ModeDefault, NewStore(), Hint{}, "", "")
	if d.Reason != "Set default_effort to high" {
		t.Errorf("string-valued SET reason = %q, want %q", d.Reason, "Set default_effort to high")
	}
}

func TestDecide_ConfigSetDeniedInPlanMode(t *testing.T) {
	d := Decide(cfgCall(`{"setting":"display_thinking","value":false}`), ModePlan, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("config SET in plan mode: want deny, got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_ConfigDenyRuleBeatsGetAllow(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{{ToolName: "config", Behavior: BehaviorDeny, Source: SourceProject}})
	d := Decide(cfgCall(`{"setting":"display_thinking"}`), ModeDefault, store, Hint{}, "", "")
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule must beat config GET allow; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_ConfigNullValueIsWrite(t *testing.T) {
	// {"value":null} is a write (matches the tool's own get/set split), so it
	// must ask, not auto-allow.
	d := Decide(cfgCall(`{"setting":"display_thinking","value":null}`), ModeDefault, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("config {value:null} should ask (write), got %v (%s)", d.Behavior, d.Reason)
	}
}

// --- auto-memory write carve-out (A8) ---

func memWrite(name, path string) ToolCall {
	return ToolCall{Name: name, Input: []byte(`{"file_path":"` + path + `","content":"x"}`)}
}

func TestDecide_AutoMemWriteAllowsInDefaultAndAcceptEdits(t *testing.T) {
	memDir := t.TempDir()
	inside := filepath.Join(memDir, "feedback", "no-db-mocks.md")
	for _, mode := range []Mode{ModeDefault, ModeAcceptEdits} {
		for _, name := range []string{"write", "edit"} {
			d := Decide(memWrite(name, inside), mode, NewStore(), Hint{}, "", memDir)
			if d.Behavior != BehaviorAllow {
				t.Errorf("%s to memory dir in %s: want allow, got %v (%s)", name, mode, d.Behavior, d.Reason)
			}
		}
	}
}

func TestDecide_AutoMemWriteEscapesGetNoCarveOut(t *testing.T) {
	memDir := t.TempDir()
	// Sibling, traversal-escape, and absolute-elsewhere all fall through to the
	// normal default-mode gate (ask), never the carve-out allow.
	escapes := []string{
		filepath.Join(filepath.Dir(memDir), "sibling.md"),
		filepath.Join(memDir, "..", "escape.md"),
		"/etc/passwd",
		memDir, // the dir root itself is not a file write inside it
	}
	for _, p := range escapes {
		d := Decide(memWrite("write", p), ModeDefault, NewStore(), Hint{}, "", memDir)
		if d.Behavior == BehaviorAllow {
			t.Errorf("write to %q should NOT get the carve-out; got allow (%s)", p, d.Reason)
		}
	}
}

func TestDecide_AutoMemWritePlanModeStillDenies(t *testing.T) {
	memDir := t.TempDir()
	inside := filepath.Join(memDir, "user", "role.md")
	// The carve-out sits after the plan-mode ceiling, so plan mode still denies.
	d := Decide(memWrite("write", inside), ModePlan, NewStore(), Hint{}, "", memDir)
	if d.Behavior != BehaviorDeny {
		t.Errorf("plan mode must still deny memory-dir write; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_AutoMemCarveOutInertWhenDisabled(t *testing.T) {
	memDir := t.TempDir()
	inside := filepath.Join(memDir, "user", "role.md")
	// Empty memDir (auto-memory off) → no special treatment, default gate asks.
	d := Decide(memWrite("write", inside), ModeDefault, NewStore(), Hint{}, "", "")
	if d.Behavior != BehaviorAsk {
		t.Errorf("carve-out must be inert when memDir is empty; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_AutoMemDenyRuleBeatsCarveOut(t *testing.T) {
	memDir := t.TempDir()
	inside := filepath.Join(memDir, "user", "role.md")
	store := NewStore()
	store.ReplaceAll([]Rule{{ToolName: "write", Behavior: BehaviorDeny, Source: SourceUser}})
	// Deny rules are evaluated before the carve-out (step 2 before step 5).
	d := Decide(memWrite("write", inside), ModeDefault, store, Hint{}, "", memDir)
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule must beat the auto-memory carve-out; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestIsAutoMemPath_Cases(t *testing.T) {
	memDir := t.TempDir()
	if !IsAutoMemPath(memDir, filepath.Join(memDir, "a", "b.md")) {
		t.Errorf("nested path inside memory dir should match")
	}
	if IsAutoMemPath(memDir, memDir) {
		t.Errorf("dir root itself should not match")
	}
	if IsAutoMemPath(memDir, filepath.Join(memDir, "..", "x.md")) {
		t.Errorf("traversal escape should not match")
	}
	if IsAutoMemPath("", filepath.Join(memDir, "a.md")) {
		t.Errorf("empty memDir should not match (carve-out inert)")
	}
	if IsAutoMemPath(memDir, "") {
		t.Errorf("empty path should not match")
	}
}
