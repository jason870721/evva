package hooks

import (
	"encoding/json"
	"strings"
)

// parseDecision parses a hook command's stdout into a Decision. Empty or
// non-JSON stdout returns an empty Decision (pass-through). Unknown fields
// are tolerated — the parser only reads the keys it knows.
func parseDecision(stdout []byte) Decision {
	trimmed := bytes_trimSpace(stdout)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Decision{}
	}
	var raw map[string]any
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return Decision{}
	}
	d := Decision{}
	if v, ok := raw["continue"].(bool); ok {
		c := v
		d.Continue = &c
	}
	if v, ok := raw["decision"].(string); ok {
		d.Decision = v
	}
	if v, ok := raw["reason"].(string); ok {
		d.Reason = v
	}
	if v, ok := raw["systemMessage"].(string); ok {
		d.SystemMessage = v
	}
	if v, ok := raw["hookSpecificOutput"].(map[string]any); ok {
		d.HookSpecificOutput = v
	}
	return d
}

// applyPreToolUse folds a Decision into the running PreToolUseDecision.
// Called per hook in fire order so later hooks see earlier hooks'
// permissionDecision / updatedInput.
//
// Precedence rules:
//   - Continue=false OR Decision="block" → set Blocked, stop the chain
//   - Decision="approve" maps to permissionDecision="allow"
//   - hookSpecificOutput.permissionDecision wins over the legacy
//     top-level Decision field (mirrors ref)
//   - Later hooks' updatedInput overrides earlier hooks' (last-write-wins)
//   - AdditionalContext concatenates across hooks (newline-separated)
func applyPreToolUse(acc *PreToolUseDecision, d Decision) (stop bool) {
	if d.Continue != nil && !*d.Continue {
		acc.Blocked = true
		acc.BlockReason = firstNonEmpty(d.Reason, d.SystemMessage, "hook continue=false")
		return true
	}
	if strings.EqualFold(d.Decision, "block") {
		acc.Blocked = true
		acc.BlockReason = firstNonEmpty(d.Reason, d.SystemMessage, "hook decision=block")
		return true
	}
	if strings.EqualFold(d.Decision, "approve") {
		acc.PermissionDecision = "allow"
		acc.Reason = firstNonEmpty(acc.Reason, d.Reason)
	}
	if d.HookSpecificOutput != nil {
		if pd, ok := d.HookSpecificOutput["permissionDecision"].(string); ok && pd != "" {
			switch strings.ToLower(pd) {
			case "allow", "deny", "ask":
				acc.PermissionDecision = strings.ToLower(pd)
			}
		}
		if pdr, ok := d.HookSpecificOutput["permissionDecisionReason"].(string); ok && pdr != "" {
			acc.Reason = pdr
		}
		if ui, ok := d.HookSpecificOutput["updatedInput"]; ok && ui != nil {
			if b, err := json.Marshal(ui); err == nil {
				acc.UpdatedInput = b
			}
		}
		if ac, ok := d.HookSpecificOutput["additionalContext"].(string); ok && ac != "" {
			if acc.AdditionalContext == "" {
				acc.AdditionalContext = ac
			} else {
				acc.AdditionalContext = acc.AdditionalContext + "\n" + ac
			}
		}
	}
	return false
}

// extractAdditionalContext pulls hookSpecificOutput.additionalContext out
// of d. Empty string if not present. Used by PostToolUse, UserPromptSubmit,
// SessionStart.
func extractAdditionalContext(d Decision) string {
	if d.HookSpecificOutput == nil {
		return ""
	}
	if v, ok := d.HookSpecificOutput["additionalContext"].(string); ok {
		return v
	}
	return ""
}

// extractInitialUserMessage pulls hookSpecificOutput.initialUserMessage
// out of d. Used by SessionStart hooks to inject a system-authored user
// message before the real first prompt.
func extractInitialUserMessage(d Decision) string {
	if d.HookSpecificOutput == nil {
		return ""
	}
	if v, ok := d.HookSpecificOutput["initialUserMessage"].(string); ok {
		return v
	}
	return ""
}

// isBlock returns true if d says "block this step" — either via Continue=false
// or Decision=block.
func isBlock(d Decision) (bool, string) {
	if d.Continue != nil && !*d.Continue {
		return true, firstNonEmpty(d.Reason, d.SystemMessage, "hook continue=false")
	}
	if strings.EqualFold(d.Decision, "block") {
		return true, firstNonEmpty(d.Reason, d.SystemMessage, "hook decision=block")
	}
	return false, ""
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// bytes_trimSpace is a tiny inline trim so decision.go doesn't import
// "bytes" — keeping the package's import set small.
func bytes_trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) {
		switch b[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			goto done1
		}
	}
done1:
	end := len(b)
	for end > start {
		switch b[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
		default:
			goto done2
		}
	}
done2:
	return b[start:end]
}
