package agent

import (
	"slices"
	"strings"
	"testing"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/tools"
)

// TestMainProfile_AdvertisesExtraDeferred pins A2 at the profile layer
// without a live MCP connection: folding extra (MCP) deferred names into
// the Main builder must surface them in the prompt's
// <available-deferred-tools> block AND in Profile.DeferredTools.
func TestMainProfile_AdvertisesExtraDeferred(t *testing.T) {
	cfg := config.Get()
	extra := []tools.ToolName{"mcp__filesystem__read_file", "mcp__filesystem__write_file"}

	prof := mainProfile(cfg, cfg.DefaultProvider, cfg.DefaultModel, nil, memdir.Snapshot{}, nil, extra, "")

	// The static resource meta tools are always deferred on Main.
	if !slices.Contains(prof.DeferredTools, tools.LIST_MCP_RESOURCES) ||
		!slices.Contains(prof.DeferredTools, tools.READ_MCP_RESOURCE) {
		t.Fatalf("Main DeferredTools missing the static MCP resource tools: %v", prof.DeferredTools)
	}
	// The extra per-server names ride in too.
	for _, n := range extra {
		if !slices.Contains(prof.DeferredTools, n) {
			t.Fatalf("Main DeferredTools missing %q", n)
		}
		if !strings.Contains(prof.SystemPrompt, string(n)) {
			t.Fatalf("system prompt does not advertise %q", n)
		}
	}

	// The advertised names must sit inside the deferred block, not elsewhere.
	block := between(prof.SystemPrompt, "<available-deferred-tools>", "</available-deferred-tools>")
	if !strings.Contains(block, "mcp__filesystem__read_file") {
		t.Fatalf("mcp name not inside <available-deferred-tools> block; block=%q", block)
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], end)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j]
}
