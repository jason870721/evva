package permission

import "strings"

// ParseRule parses ref-compatible rule strings of the form `ToolName` or
// `ToolName(content)` with `\(`, `\)`, and `\\` escapes inside the content.
// Behavior and Source are not parsed — they come from the surrounding
// (allow|deny|ask) array in the JSON. Returns ok=false for empty or
// missing-tool-name inputs.
//
// Degenerate forms `ToolName()` and `ToolName(*)` collapse to the tool-wide
// form (empty Content) — matching ref's behavior.
func ParseRule(s string) (toolName, content string, ok bool) {
	if s == "" {
		return "", "", false
	}

	open := findFirstUnescaped(s, '(')
	if open < 0 {
		return s, "", true
	}

	close := findLastUnescaped(s, ')')
	if close < 0 || close <= open || close != len(s)-1 {
		return s, "", true
	}

	toolName = s[:open]
	if toolName == "" {
		// Malformed `(foo)` — fall back to treating the whole input as a
		// tool name (matches ref's `permissionRuleValueFromString` fallback
		// in ref/src/utils/permissions/permissionRuleParser.ts).
		return s, "", true
	}

	raw := s[open+1 : close]
	if raw == "" || raw == "*" {
		return toolName, "", true
	}
	return toolName, unescapeRuleContent(raw), true
}

// FormatRule serializes the canonical identity of a rule (ToolName +
// optional escaped Content). Behavior and Source are stored separately.
func FormatRule(toolName, content string) string {
	if content == "" {
		return toolName
	}
	return toolName + "(" + escapeRuleContent(content) + ")"
}

// escapeRuleContent escapes backslashes and parens so the result survives
// a round-trip through ParseRule.
func escapeRuleContent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '(':
			b.WriteString(`\(`)
		case ')':
			b.WriteString(`\)`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unescapeRuleContent reverses escapeRuleContent. Order matters: parens are
// unescaped before backslashes so `\\(` collapses to `\(`, not `((`.
func unescapeRuleContent(s string) string {
	s = strings.ReplaceAll(s, `\(`, `(`)
	s = strings.ReplaceAll(s, `\)`, `)`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

// findFirstUnescaped returns the index of the first c not preceded by an
// odd number of backslashes, or -1 if no such occurrence exists.
func findFirstUnescaped(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c && countLeadingBackslashes(s, i)%2 == 0 {
			return i
		}
	}
	return -1
}

func findLastUnescaped(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c && countLeadingBackslashes(s, i)%2 == 0 {
			return i
		}
	}
	return -1
}

// countLeadingBackslashes returns the count of backslashes immediately
// preceding position i.
func countLeadingBackslashes(s string, i int) int {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n
}
