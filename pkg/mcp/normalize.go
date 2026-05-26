package mcp

import "regexp"

var invalidNameChar = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// NormalizeName maps an arbitrary server or tool name into the API-safe
// pattern ^[a-zA-Z0-9_-]{1,64}$. Replaces every invalid character with
// an underscore. Direct port of ref/src/services/mcp/normalization.ts:
// normalizeNameForMCP, minus the claude.ai-specific underscore-collapse
// branch (we don't ship claude.ai-proxy servers).
func NormalizeName(name string) string {
	return invalidNameChar.ReplaceAllString(name, "_")
}
