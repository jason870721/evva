package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
)

// TestRepoMap_DeferredOnMainOnly pins A8: repo_map is on the Main profile (as a
// deferred tool) and absent from the read-only subagent profiles, whose prompts
// never carry a repo map.
func TestRepoMap_DeferredOnMainOnly(t *testing.T) {
	cfg := config.Get()

	main := Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil, memdir.Snapshot{}, nil)
	if !slices.Contains(main.DeferredTools, tools.REPO_MAP) {
		t.Error("repo_map should be a deferred tool on the Main profile")
	}
	if slices.Contains(main.ActiveTools, tools.REPO_MAP) {
		t.Error("repo_map should be deferred, not active, on Main")
	}

	for name, prof := range map[string]Profile{
		"Explore": Explore(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil),
		"Plan":    Plan(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil),
	} {
		if slices.Contains(prof.ActiveTools, tools.REPO_MAP) || slices.Contains(prof.DeferredTools, tools.REPO_MAP) {
			t.Errorf("%s subagent must not carry repo_map", name)
		}
		if strings.Contains(prof.SystemPrompt, "# Repo map") {
			t.Errorf("%s subagent prompt must not carry a repo map", name)
		}
	}
}
