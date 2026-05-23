package permission

import (
	"encoding/json"
)

// Decide runs the permission pipeline for a single tool call and returns
// the resolved Behavior + Reason. An Ask outcome is escalated to the
// broker by the caller (state_machine.go).
//
// workdir is the project's working directory — used only for the plan-mode
// plan-file carve-out (step 4a). Callers without a workdir (tests, headless
// runs) may pass "" to skip the carve-out; the rest of the pipeline is
// workdir-independent.
//
// Pipeline:
//
//  1. ModeBypass → allow (no rule lookup; bypass means bypass).
//  2. Deny rules → deny (always win when not bypassed).
//  3. Ask rules → ask (a user-forced prompt overrides mode auto-allow).
//  4. ModePlan-only:
//     - Write/Edit targeting <workdir>/.evva/plans/ → allow (plan-file carve-out).
//     - Tool ∈ ReadOnlyOrSelfTools → allow.
//     - Bash + classifier says read-only → allow.
//     - Else → deny "plan mode forbids writes".
//  5. ReadOnlyOrSelfTools → allow (the baseline safe set).
//  6. Bash + classifier says read-only → allow.
//  7. ModeAcceptEdits-only:
//     - tool ∈ AcceptEditsAutoAllow (edit/write/notebook_edit) → allow.
//     - Bash + classifier says common-fs (mkdir/mv/cp/touch/...) → allow.
//  8. Allow rules → allow.
//  9. Else → ask.
//
// The order ensures deny rules always win (step 2), user-forced asks
// always show (step 3), and plan mode's hard ceiling is enforced before
// the auto-allow path can let a write through (step 4 before step 7).
func Decide(call ToolCall, mode Mode, store *Store, hint Hint, workdir string) Decision {
	if mode == ModeBypass {
		return Decision{Behavior: BehaviorAllow, Reason: "bypass mode"}
	}

	if store != nil {
		if r, ok := store.firstMatch(call, BehaviorDeny); ok {
			return Decision{
				Behavior: BehaviorDeny,
				Reason:   "denied by rule: " + FormatRule(r.ToolName, r.Content),
			}
		}
		if r, ok := store.firstMatch(call, BehaviorAsk); ok {
			return Decision{
				Behavior: BehaviorAsk,
				Reason:   "ask rule: " + FormatRule(r.ToolName, r.Content),
			}
		}
	}

	// mode = default (allow read)
	inSafelist := ReadOnlyOrSelfTools[call.Name]
	if inSafelist {
		return Decision{Behavior: BehaviorAllow, Reason: "read-only or self-coordination tool"}
	}
	if call.Name == "bash" && hint.IsReadOnly {
		reason := "bash: read-only command"
		if hint.Matched != "" {
			reason += " (" + hint.Matched + ")"
		}
		return Decision{Behavior: BehaviorAllow, Reason: reason}
	}

	if mode == ModePlan {
		if isPlanFileWrite(call, workdir) {
			return Decision{Behavior: BehaviorAllow, Reason: "plan mode: plan-file write"}
		}
		return Decision{
			Behavior: BehaviorDeny,
			Reason:   "plan mode forbids writes — Shift+Tab to exit plan mode",
		}
	}

	if mode == ModeAcceptEdits {
		if AcceptEditsAutoAllow[call.Name] {
			return Decision{Behavior: BehaviorAllow, Reason: "accept_edits: file-edit tool"}
		}
		if call.Name == "bash" && hint.IsCommonFS {
			reason := "accept_edits: common fs command"
			if hint.Matched != "" {
				reason += " (" + hint.Matched + ")"
			}
			return Decision{Behavior: BehaviorAllow, Reason: reason}
		}
	}

	if store != nil {
		if r, ok := store.firstMatch(call, BehaviorAllow); ok {
			return Decision{
				Behavior: BehaviorAllow,
				Reason:   "allowed by rule: " + FormatRule(r.ToolName, r.Content),
			}
		}
	}

	reason := "no matching rule"
	if hint.IsDangerous {
		reason = "dangerous command"
		if hint.Matched != "" {
			reason += " (" + hint.Matched + ")"
		}
	}
	return Decision{Behavior: BehaviorAsk, Reason: reason}
}

// isPlanFileWrite reports whether call is a Write or Edit targeting a path
// under <workdir>/.evva/plans/. The carve-out lets the model compose its
// plan while ModePlan otherwise hard-denies every write.
//
// Returns false when workdir is empty, the tool isn't write/edit, the input
// doesn't carry a file_path string, or the path resolves outside the plan
// dir. Errors during JSON parsing are non-fatal — they just disable the
// carve-out for that call (the gate falls through to the deny branch).
func isPlanFileWrite(call ToolCall, workdir string) bool {
	if workdir == "" {
		return false
	}
	if call.Name != "write" && call.Name != "edit" {
		return false
	}
	if len(call.Input) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(call.Input, &m); err != nil {
		return false
	}
	p, _ := m["file_path"].(string)
	return IsPlanFilePath(workdir, p)
}
