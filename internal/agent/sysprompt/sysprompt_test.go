package sysprompt

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// Tests for the package-level surface — PromptContext, DetectContext,
// detectShell. Per-agent builder tests live in main_agent_test.go,
// explore_agent_test.go, general_agent_test.go.

func TestDetectContext_PopulatesIdentityAndEnv(t *testing.T) {
	ctx := DetectContext("evva", "/home/x/.evva", "dev")

	if ctx.AgentName != "evva" {
		t.Errorf("AgentName: got %q, want %q", ctx.AgentName, "evva")
	}
	if ctx.EvvaHome != "/home/x/.evva" {
		t.Errorf("EvvaHome: got %q, want %q", ctx.EvvaHome, "/home/x/.evva")
	}
	if ctx.Env != "dev" {
		t.Errorf("Env: got %q, want %q", ctx.Env, "dev")
	}
	if ctx.Today.IsZero() {
		t.Error("Today not auto-populated")
	}
	if ctx.OS != runtime.GOOS {
		t.Errorf("OS: got %q, want runtime.GOOS=%q", ctx.OS, runtime.GOOS)
	}
	if ctx.WorkDir == "" {
		t.Error("WorkDir should be auto-detected in a normal test env")
	}
}

func TestDetectContext_LeavesUserSlotsZero(t *testing.T) {
	// Skills, WorkdirMemory, MemoryIndex are caller-supplied — DetectContext
	// must not populate them.
	ctx := DetectContext("evva", "/h", "prod")
	if ctx.Skills != nil {
		t.Errorf("Skills should be nil; got %v", ctx.Skills)
	}
	if ctx.WorkdirMemory != "" {
		t.Errorf("WorkdirMemory should be empty; got %q", ctx.WorkdirMemory)
	}
	if ctx.MemoryIndex != "" {
		t.Errorf("MemoryIndex should be empty; got %q", ctx.MemoryIndex)
	}
}

func TestDetectShell_HonorsEnv(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	if got := detectShell(); got != "fish" {
		t.Errorf("detectShell: got %q, want %q", got, "fish")
	}
}

func TestDetectShell_Lowercased(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/ZSH")
	if got := detectShell(); got != "zsh" {
		t.Errorf("detectShell should lowercase: got %q", got)
	}
}

func TestDetectShell_EmptyEnv(t *testing.T) {
	orig, hadIt := os.LookupEnv("SHELL")
	_ = os.Unsetenv("SHELL")
	t.Cleanup(func() {
		if hadIt {
			_ = os.Setenv("SHELL", orig)
		}
	})

	if got := detectShell(); got != "" {
		t.Errorf("detectShell with unset $SHELL: got %q, want empty", got)
	}
}

func TestDetectContext_ShellPicksUpFromEnv(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	ctx := DetectContext("evva", "", "dev")
	if !strings.EqualFold(ctx.Shell, "bash") {
		t.Errorf("Shell: got %q, want %q", ctx.Shell, "bash")
	}
}
