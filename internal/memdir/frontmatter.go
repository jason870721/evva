package memdir

import "strings"

// ParseFrontmatter splits a YAML-ish frontmatter block from a markdown body.
//
// A memory file may open with a block delimited by a `---` line on each side:
//
//	---
//	name: user-role
//	description: senior Go engineer, new to this repo's frontend
//	type: user
//	---
//	<body…>
//
// The parser is deliberately minimal — flat `key: value` pairs, string values
// only — so the base memdir package stays stdlib-only (no YAML dependency; see
// the package doc). It is legacy-tolerant by design: a file with no opening
// `---`, or an unterminated block, yields an empty map and the *entire* input
// as the body, never an error. A malformed memory file therefore stays readable
// (the scanner simply skips it) instead of breaking a session.
//
// Keys are lowercased + trimmed; values are trimmed and stripped of one pair of
// surrounding quotes. Indentation is tolerated, so a nested `metadata:` block —
// some ref templates write `type:` under it — is flattened into the same map.
// The flat form is canonical (MEMORY_FRONTMATTER_EXAMPLE), so a top-level key
// always wins over an indented duplicate of the same name.
func ParseFrontmatter(content string) (map[string]string, string) {
	fm := map[string]string{}
	if !strings.HasPrefix(content, "---") {
		return fm, content
	}
	lines := strings.Split(content, "\n")
	// The very first line must be exactly "---" (tolerate trailing CR/space).
	if strings.TrimSpace(lines[0]) != "---" {
		return fm, content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		// Unterminated block — treat the whole file as body (legacy-tolerant).
		return fm, content
	}
	for _, ln := range lines[1:end] {
		key, val, ok := splitFrontmatterLine(ln)
		if !ok {
			continue
		}
		if _, exists := fm[key]; exists {
			continue // top-level key already set; don't let an indented dup clobber it
		}
		fm[key] = val
	}
	body := strings.TrimPrefix(strings.Join(lines[end+1:], "\n"), "\n")
	return fm, body
}

// splitFrontmatterLine parses one "key: value" line. Leading indentation is
// tolerated (so a `metadata:` sub-key flattens in). Comment lines (`#`), blank
// lines, lines with no colon, and container lines with an empty value (a bare
// `metadata:`) return ok=false and are skipped. The split is on the FIRST colon
// so a value may itself contain ':'.
func splitFrontmatterLine(line string) (key, val string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	rawKey, rawVal, found := strings.Cut(trimmed, ":")
	if !found {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(rawKey))
	val = unquoteScalar(strings.TrimSpace(rawVal))
	if key == "" || val == "" {
		return "", "", false
	}
	return key, val, true
}

// unquoteScalar strips a single pair of matching surrounding quotes.
func unquoteScalar(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
