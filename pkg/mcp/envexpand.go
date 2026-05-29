package mcp

import (
	"os"
	"regexp"
	"strings"
)

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ExpandEnv expands ${VAR} and ${VAR:-default} references in s using the
// process environment. Returns the expanded string and a slice of any
// referenced variables that were unset and had no default. Direct port
// of ref/src/services/mcp/envExpansion.ts:expandEnvVarsInString.
func ExpandEnv(s string) (expanded string, missing []string) {
	expanded = envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		inner := strings.TrimPrefix(strings.TrimSuffix(match, "}"), "${")
		name, def, hasDef := strings.Cut(inner, ":-")
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if hasDef {
			return def
		}
		missing = append(missing, name)
		return match
	})
	return expanded, missing
}
