package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	config "github.com/johnny1110/evva/pkg/config"
)

// TestDetectContext_PrefersConfigWorkdir pins the fix for the swarm workdir
// bug: an agent whose cfg.WorkDir diverges from the process cwd (a swarm space
// under a shared service daemon, a worktree-isolated subagent) must see its
// OWN workdir in the system prompt's environment block — otherwise the model
// emits absolute paths into whatever directory the daemon happened to be
// started from, even though the tools resolve relative paths correctly.
func TestDetectContext_PrefersConfigWorkdir(t *testing.T) {
	cfg := config.Get().Clone()
	cfg.WorkDir = "/tmp/some-space-workdir"

	ctx := detectContext(cfg)
	if ctx.WorkDir != "/tmp/some-space-workdir" {
		t.Errorf("detectContext must pin WorkDir to cfg.WorkDir; got %q", ctx.WorkDir)
	}

	prof := Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil, memdir.Snapshot{}, nil)
	if !strings.Contains(prof.SystemPrompt, "/tmp/some-space-workdir") {
		t.Errorf("Main profile system prompt must carry cfg.WorkDir, not the process cwd")
	}

	// Empty WorkDir keeps the historical fallback (process cwd).
	cfg2 := config.Get().Clone()
	cfg2.WorkDir = ""
	wd, _ := os.Getwd()
	if got := detectContext(cfg2).WorkDir; got != wd {
		t.Errorf("empty cfg.WorkDir must fall back to process cwd %q; got %q", wd, got)
	}
}
