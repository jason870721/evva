package permission

import "testing"

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
	d := Decide(mkCall("bash", "rm -rf /"), ModeBypass, store, Hint{})
	if d.Behavior != BehaviorAllow {
		t.Errorf("bypass should allow even with deny rule; got %v", d.Behavior)
	}
}

func TestDecide_PlanModeBlocksWrites(t *testing.T) {
	d := Decide(mkCall("edit", ""), ModePlan, NewStore(), Hint{})
	if d.Behavior != BehaviorDeny {
		t.Errorf("plan mode should deny edit; got %v", d.Behavior)
	}
	d = Decide(mkCall("read", ""), ModePlan, NewStore(), Hint{})
	if d.Behavior != BehaviorAllow {
		t.Errorf("plan mode should allow read; got %v", d.Behavior)
	}
}

func TestDecide_DenyWinsOverAllow(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "npm:*", Behavior: BehaviorAllow, Source: SourceProject},
		{ToolName: "bash", Behavior: BehaviorDeny, Source: SourceUser},
	})
	d := Decide(mkCall("bash", "npm install"), ModeDefault, store, Hint{})
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule should beat allow rule; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_AskRule(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "rm:*", Behavior: BehaviorAsk, Source: SourceProject},
	})
	d := Decide(mkCall("bash", "rm -rf foo"), ModeDefault, store, Hint{})
	if d.Behavior != BehaviorAsk {
		t.Errorf("expected ask; got %v", d.Behavior)
	}
}

func TestDecide_AcceptEdits(t *testing.T) {
	d := Decide(mkCall("edit", ""), ModeAcceptEdits, NewStore(), Hint{})
	if d.Behavior != BehaviorAllow {
		t.Errorf("accept_edits should allow edit; got %v", d.Behavior)
	}
	// Common-fs bash command auto-allows in accept_edits.
	d = Decide(mkCall("bash", "mkdir foo"), ModeAcceptEdits, NewStore(), Hint{IsCommonFS: true, Matched: "mkdir"})
	if d.Behavior != BehaviorAllow {
		t.Errorf("accept_edits should allow common-fs bash; got %v (%s)", d.Behavior, d.Reason)
	}
	// Dangerous / unclassified bash still asks even in accept_edits.
	d = Decide(mkCall("bash", "do-something"), ModeAcceptEdits, NewStore(), Hint{})
	if d.Behavior != BehaviorAsk {
		t.Errorf("accept_edits should ask for unclassified bash; got %v", d.Behavior)
	}
}

func TestDecide_DefaultAutoAllowsSafeTools(t *testing.T) {
	// Read-only/self-coordination tools auto-allow in default mode.
	for _, name := range []string{"read", "tree", "agent", "task_create", "todo_write", "tool_search"} {
		d := Decide(mkCall(name, ""), ModeDefault, NewStore(), Hint{})
		if d.Behavior != BehaviorAllow {
			t.Errorf("default should auto-allow %q; got %v (%s)", name, d.Behavior, d.Reason)
		}
	}
}

func TestDecide_DefaultAllowsReadOnlyBash(t *testing.T) {
	// Bash with a read-only command (ls, cat, ...) auto-allows in default
	// mode via the classifier hint, so the user isn't prompted for every
	// directory listing.
	d := Decide(mkCall("bash", "ls"), ModeDefault, NewStore(), Hint{IsReadOnly: true, Matched: "ls"})
	if d.Behavior != BehaviorAllow {
		t.Errorf("default should allow read-only bash; got %v (%s)", d.Behavior, d.Reason)
	}
}

func TestDecide_DefaultAsksCommonFSBash(t *testing.T) {
	// Even common fs commands ask in default mode — they only auto-allow
	// in accept_edits.
	d := Decide(mkCall("bash", "mkdir foo"), ModeDefault, NewStore(), Hint{IsCommonFS: true, Matched: "mkdir"})
	if d.Behavior != BehaviorAsk {
		t.Errorf("default should ask for common-fs bash; got %v", d.Behavior)
	}
}

func TestDecide_AllowRule(t *testing.T) {
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "git:*", Behavior: BehaviorAllow, Source: SourceProject},
	})
	d := Decide(mkCall("bash", "git status"), ModeDefault, store, Hint{})
	if d.Behavior != BehaviorAllow {
		t.Errorf("git:* allow rule should match 'git status'; got %v", d.Behavior)
	}
}

func TestDecide_DangerousBashSurfacesHint(t *testing.T) {
	// Dangerous bash still asks (any non-bypass mode), but the Reason
	// includes the matched entry so the approval UI can show it.
	d := Decide(mkCall("bash", "eval foo"), ModeDefault, NewStore(), Hint{IsDangerous: true, Matched: "eval"})
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
	d := Decide(mkCall("bash", "do-something"), ModeDefault, NewStore(), Hint{})
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
	d := Decide(mkCall("bash", "ls"), ModeDefault, store, Hint{})
	if d.Behavior != BehaviorDeny {
		t.Errorf("deny rule must win over session allow; got %v (%s)", d.Behavior, d.Reason)
	}
}
