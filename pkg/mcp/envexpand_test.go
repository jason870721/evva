package mcp

import (
	"slices"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("EVVA_MCP_TEST_VAR", "expanded")

	t.Run("set var", func(t *testing.T) {
		got, missing := ExpandEnv("x=${EVVA_MCP_TEST_VAR}")
		if got != "x=expanded" || len(missing) != 0 {
			t.Fatalf("got %q missing %v", got, missing)
		}
	})

	t.Run("default used when unset", func(t *testing.T) {
		got, missing := ExpandEnv("${EVVA_MCP_NOT_SET:-fallback}")
		if got != "fallback" || len(missing) != 0 {
			t.Fatalf("got %q missing %v", got, missing)
		}
	})

	t.Run("unset with no default is reported", func(t *testing.T) {
		got, missing := ExpandEnv("${EVVA_MCP_NOT_SET}")
		if got != "${EVVA_MCP_NOT_SET}" {
			t.Fatalf("literal preserved: got %q", got)
		}
		if !slices.Contains(missing, "EVVA_MCP_NOT_SET") {
			t.Fatalf("missing should contain EVVA_MCP_NOT_SET, got %v", missing)
		}
	})

	t.Run("set var beats default", func(t *testing.T) {
		got, _ := ExpandEnv("${EVVA_MCP_TEST_VAR:-fallback}")
		if got != "expanded" {
			t.Fatalf("got %q, want expanded", got)
		}
	})

	t.Run("no expansion", func(t *testing.T) {
		got, missing := ExpandEnv("plain string")
		if got != "plain string" || len(missing) != 0 {
			t.Fatalf("got %q missing %v", got, missing)
		}
	})
}
