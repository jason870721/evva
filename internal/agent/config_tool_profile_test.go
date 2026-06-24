package agent

import (
	"slices"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
)

// TestMainProfile_IncludesConfigTool pins A1: the config tool is active on
// Main so the model can surface it without a tool_search round-trip.
func TestMainProfile_IncludesConfigTool(t *testing.T) {
	cfg := config.Get()
	prof := mainProfile(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil, memdir.Snapshot{}, nil, nil, "")
	if !slices.Contains(prof.ActiveTools, tools.CONFIG) {
		t.Errorf("Main ActiveTools missing config: %v", prof.ActiveTools)
	}
}

// TestSubagentProfiles_ExcludeConfigTool pins the other half of A1: cold,
// narrow-task subagents have no business mutating user config, so they get
// it neither active nor deferred.
func TestSubagentProfiles_ExcludeConfigTool(t *testing.T) {
	cfg := config.Get()
	subagents := map[string]Profile{
		"explore": Explore(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil),
		"plan":    Plan(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil),
		"general": General(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil),
	}
	for name, prof := range subagents {
		if slices.Contains(prof.ActiveTools, tools.CONFIG) {
			t.Errorf("subagent %q must not have config active; ActiveTools=%v", name, prof.ActiveTools)
		}
		if slices.Contains(prof.DeferredTools, tools.CONFIG) {
			t.Errorf("subagent %q must not defer config either; DeferredTools=%v", name, prof.DeferredTools)
		}
	}
}
