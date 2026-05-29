package mcp

import "strings"

// ToolNamePrefix returns "mcp__<server>__" for the normalized server.
func ToolNamePrefix(server string) string {
	return "mcp__" + NormalizeName(server) + "__"
}

// BuildToolName returns "mcp__<server>__<tool>" with both names
// normalized. Inverse of ParseToolName.
func BuildToolName(server, tool string) string {
	return ToolNamePrefix(server) + NormalizeName(tool)
}

// ToolNameInfo is the parsed shape: server + tool.
type ToolNameInfo struct {
	Server string
	Tool   string
}

// ParseToolName extracts server + tool from a mcp__<server>__<tool>
// string. Returns nil if name lacks the prefix or has no tool segment.
// Known limitation: if a server name contains "__", parsing reports
// the first segment as the server. Server names with double-underscore
// are rare in practice (ref has the same limitation).
func ParseToolName(name string) *ToolNameInfo {
	parts := strings.Split(name, "__")
	if len(parts) < 3 || parts[0] != "mcp" || parts[1] == "" {
		return nil
	}
	return &ToolNameInfo{
		Server: parts[1],
		Tool:   strings.Join(parts[2:], "__"),
	}
}
