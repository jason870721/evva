package permission

import (
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// shellRuleKind is the parsed shape of a shell rule's Content.
type shellRuleKind int

const (
	shellExact shellRuleKind = iota
	shellPrefix
	shellWildcard
)

// matchToolCall reports whether r applies to call. Content matching is
// tool-aware: shell commands for `bash`, path globs for `read`/`write`/
// `edit`/`notebook_edit`, exact string fallback for anything else.
//
// A tool-wide rule (Content == "") always matches a call to the same tool.
func matchToolCall(r Rule, call ToolCall) bool {
	if r.ToolName != call.Name {
		return false
	}
	if r.Content == "" {
		return true
	}
	switch call.Name {
	case "bash":
		return matchShell(r.Content, extractBashCommand(call.Input))
	case "read", "write", "edit", "notebook_edit":
		return matchPath(r.Content, extractFilePath(call.Input))
	default:
		return r.Content == string(call.Input)
	}
}

// matchShell matches a Bash command against a shell-style rule pattern.
// Three pattern shapes — see ref/src/utils/permissions/shellRuleMatching.ts.
//
//   - `npm:*` (legacy prefix) → match if command == "npm" or starts with "npm "
//   - `git *` (single trailing wildcard) → match if command == "git" or starts with "git "
//   - `git log *` (mid/multi wildcard) → regex-style: `*` is any-chars
//   - `npm install` (exact) → string equality
func matchShell(pattern, command string) bool {
	pattern = strings.TrimSpace(pattern)
	command = strings.TrimSpace(command)
	kind, body := parseShellPattern(pattern)
	switch kind {
	case shellExact:
		return body == command
	case shellPrefix:
		return command == body || strings.HasPrefix(command, body+" ")
	case shellWildcard:
		return matchWildcard(body, command)
	}
	return false
}

func parseShellPattern(p string) (shellRuleKind, string) {
	if strings.HasSuffix(p, ":*") {
		return shellPrefix, strings.TrimSuffix(p, ":*")
	}
	if hasUnescapedStar(p) {
		return shellWildcard, p
	}
	return shellExact, p
}

func hasUnescapedStar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '*' && countLeadingBackslashes(s, i)%2 == 0 {
			return true
		}
	}
	return false
}

// matchWildcard compiles the pattern to a regex and matches against s.
// Supports `*` (any chars), `\*` (literal asterisk), `\\` (literal backslash).
// A pattern ending in single ` *` makes the trailing space+args optional so
// `git *` matches bare `git` too — ref's alignment with `:*` prefix semantics.
func matchWildcard(pattern, s string) bool {
	if onlyTrailingStar(pattern) {
		prefix := pattern[:len(pattern)-2]
		return s == prefix || strings.HasPrefix(s, prefix+" ")
	}

	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			n := pattern[i+1]
			if n == '*' {
				b.WriteString(regexp.QuoteMeta("*"))
				i++
				continue
			}
			if n == '\\' {
				b.WriteString(regexp.QuoteMeta(`\`))
				i++
				continue
			}
		}
		if c == '*' {
			b.WriteString("(?s:.*)")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(c)))
	}
	b.WriteByte('$')

	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// onlyTrailingStar reports whether the pattern is "<prefix> *" with exactly
// one unescaped star (the trailing one).
func onlyTrailingStar(pattern string) bool {
	if !strings.HasSuffix(pattern, " *") {
		return false
	}
	count := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '*' && countLeadingBackslashes(pattern, i)%2 == 0 {
			count++
		}
	}
	return count == 1
}

// matchPath matches a file path against a doublestar glob. Empty paths
// never match (defensive — a Read/Write call without a path would already
// have failed schema validation before reaching the gate).
func matchPath(pattern, path string) bool {
	if path == "" {
		return false
	}
	ok, err := doublestar.Match(pattern, path)
	if err != nil {
		return false
	}
	return ok
}

// extractBashCommand pulls the "command" field out of a Bash tool call's
// JSON input. On any parse failure it returns "" — the matcher then can't
// match anything, which is the safe default (call falls through to the
// next rule / ask).
func extractBashCommand(raw []byte) string {
	return extractStringField(raw, "command")
}

func extractFilePath(raw []byte) string {
	return extractStringField(raw, "file_path")
}

// extractStringField is a minimal, allocation-free-ish way to pull a
// top-level string field out of a known-good tool input JSON. We don't use
// encoding/json because the matcher is on the hot path and the tool input
// has already been validated by the time the gate sees it.
func extractStringField(raw []byte, field string) string {
	s := string(raw)
	key := `"` + field + `"`
	idx := strings.Index(s, key)
	if idx < 0 {
		return ""
	}
	// Find the colon after the key.
	rest := s[idx+len(key):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	rest = rest[colon+1:]
	// Skip whitespace.
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || rest[0] == '\r') {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	// Walk to the closing quote, honoring backslash escapes.
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '\\' && i+1 < len(rest) {
			next := rest[i+1]
			switch next {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(next)
			}
			i++
			continue
		}
		if c == '"' {
			return b.String()
		}
		b.WriteByte(c)
	}
	return ""
}
