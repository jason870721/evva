package permission

import (
	"encoding/json"
	"fmt"
	"strings"
)

// configToolName is the wire name of the `config` tool (internal/tools/config).
// The config tool is the one tool whose risk depends on its input: reading a
// setting (no "value") is safe and auto-allows; writing one asks. Decide
// special-cases it the same way it special-cases bash — by name, since the
// permission package classifies tools by name rather than via a method on the
// tool itself.
const configToolName = "config"

// Decide runs the permission pipeline for a single tool call and returns
// the resolved Behavior + Reason. An Ask outcome is escalated to the
// broker by the caller (state_machine.go).
//
// workdir is the project's working directory — used only for the plan-mode
// plan-file carve-out (step 4a). Callers without a workdir (tests, headless
// runs) may pass "" to skip the carve-out; the rest of the pipeline is
// workdir-independent.
//
// memDir is the resolved auto-memory directory (<appHome>/memory) when
// auto-memory is on, or "" when it's off — callers pass memdir.MemoryDir's
// value (which is already gated on the auto-memory toggle). A write/edit
// confined to memDir auto-allows in default + accept-edits so the model can
// maintain its typed memory files without a prompt; an empty memDir makes the
// carve-out inert.
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
//  5. Write/Edit confined to memDir → allow (auto-memory carve-out; after the
//     plan-mode ceiling so plan mode still denies, inert when memDir == "").
//  6. ReadOnlyOrSelfTools → allow (the baseline safe set).
//  7. Bash + classifier says read-only → allow.
//  8. ModeAcceptEdits-only:
//     - tool ∈ AcceptEditsAutoAllow (edit/write/notebook_edit) → allow.
//     - Bash + classifier says common-fs (mkdir/mv/cp/touch/...) → allow.
//  9. Allow rules → allow.
//  10. Else → ask.
//
// The order ensures deny rules always win (step 2), user-forced asks
// always show (step 3), and plan mode's hard ceiling is enforced before
// the auto-allow paths can let a write through (step 4 before steps 5 / 8).
func Decide(call ToolCall, mode Mode, store *Store, hint Hint, workdir, memDir string) Decision {
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
	// config reads (no "value") are side-effect-free — allow them in every
	// mode, including plan. config writes fall through: in plan mode they hit
	// the write-deny below; otherwise they reach the config-set ask near the
	// end of the pipeline.
	if call.Name == configToolName && configIsRead(call.Input) {
		return Decision{Behavior: BehaviorAllow, Reason: "config: read-only get"}
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

	// Auto-memory write carve-out. Placed AFTER the plan-mode ceiling so plan
	// mode still hard-denies writes (A8), and before the accept-edits branch so
	// it also fires in default mode. Inert when memDir == "" (auto-memory off,
	// A9). Mirrors isPlanFileWrite.
	if isAutoMemWrite(call, memDir) {
		return Decision{Behavior: BehaviorAllow, Reason: "auto-memory dir write"}
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

	// config writes reach here in non-plan modes (plan mode denied them
	// above). Ask with a setting-specific message so the prompt reads
	// "Set <key> to <value>" instead of a generic "no matching rule".
	if call.Name == configToolName {
		return Decision{Behavior: BehaviorAsk, Reason: configSetMessage(call.Input)}
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

// configIsRead reports whether a config tool call is a GET (no "value").
// It mirrors the tool's own get/set split (internal/tools/config): the value
// is absent or empty-bytes for a read. A present-but-null value
// ({"value":null}) counts as a write, matching the tool. A parse failure is
// treated as a write (the safe default — fall through to ask).
func configIsRead(raw []byte) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	v, ok := m["value"]
	return !ok || len(v) == 0
}

// configSetMessage renders the approval prompt for a config write as
// "Set <key> to <value>". A JSON string value is shown unquoted for
// readability ("Set default_effort to high"). Secret values are shown raw —
// transcript/prompt redaction of secrets is a separate, not-yet-built concern
// (see v1.5 design notes §5.6).
func configSetMessage(raw []byte) string {
	var m struct {
		Setting string          `json:"setting"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Setting == "" {
		return "modify configuration"
	}
	val := strings.TrimSpace(string(m.Value))
	var s string
	if json.Unmarshal(m.Value, &s) == nil {
		val = s
	}
	return fmt.Sprintf("Set %s to %s", m.Setting, val)
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

// isAutoMemWrite reports whether call is a Write or Edit whose file_path is
// confined to the auto-memory dir memDir. Mirrors isPlanFileWrite: only
// write/edit, parse file_path, defer containment to IsAutoMemPath. Returns
// false when memDir is empty (auto-memory off) so the carve-out is inert. A
// JSON parse failure disables the carve-out for that call (falls through to the
// normal gate).
func isAutoMemWrite(call ToolCall, memDir string) bool {
	if memDir == "" {
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
	return IsAutoMemPath(memDir, p)
}
