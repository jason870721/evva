package config

import (
	"os"
	"strings"
)

// getEnvDefault returns the env var value, or fallback if unset/empty.
// Uses LookupEnv to distinguish "unset" from "set to empty string";
// both are treated as "use default" here — empty string is not a valid value
// for config fields like LOG_LEVEL.
func getEnvDefault(key, fallback string) string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return fallback
	}
	return strings.TrimSpace(val)
}

func getEnvDefaultLowerCase(key, fallback string) string {
	return strings.ToLower(getEnvDefault(key, fallback))
}

// getEnvNullable returns nil if the var is unset or empty,
// or a pointer to the trimmed value if present.
// This preserves the semantic distinction:
//
//	nil   → "not configured, use default behavior"
//	&""   → never returned (empty treated as nil)
//	&"/var/log" → explicitly configured
func getEnvNullable(key string) *string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(val)
	return &trimmed
}
