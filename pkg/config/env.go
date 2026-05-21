package config

import (
	"os"
	"path/filepath"
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

// resolveLogDir picks the directory the logger writes per-agent log
// files into. Three-way semantics around the LOG_DIR env var:
//
//	unset       → "<evvaHome>/logs" (post-`make install` default — users
//	              never need to configure anything to find their logs)
//	LOG_DIR=""  → nil (explicit opt-out: stdout-only, dev mode)
//	LOG_DIR=p   → &p (explicit override to a custom path)
//
// The empty-string opt-out is preserved so dev runs with `LOG_DIR=` in
// front of `go run ./cmd/evva` still print noisy logs to the terminal
// instead of writing them to disk.
func resolveLogDir(evvaHome string) *string {
	val, ok := os.LookupEnv("LOG_DIR")
	if !ok {
		def := filepath.Join(evvaHome, "logs")
		return &def
	}
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
