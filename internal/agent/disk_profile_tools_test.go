package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
)

// RP-19 wiring through the profile layer: a disk persona that declares
// deferred tools gets tool_search auto-mounted into its active set (a
// deferred catalog without tool_search is dead data), its prompt advertises
// the catalog in an <available-deferred-tools> block, and the
// per-tool-gated mechanics guide renders. A persona without deferred tools
// gets none of that bolted on.
func TestResolveMainProfile_DeferredAutoMountsToolSearch(t *testing.T) {
	cfg := config.Get()
	reg := NewAgentRegistry()
	reg.Register(sysprompt.AgentDefinition{
		Name:              "deferpersona",
		As:                []string{"main"},
		ActiveTools:       []tools.ToolName{tools.READ_FILE},
		DeferredTools:     []tools.ToolName{tools.WEB_SEARCH},
		BuildSystemPrompt: func(sysprompt.PromptContext) string { return "You are deferpersona.\n" },
	})

	prof, err := ResolveMainProfile(cfg, reg, "deferpersona", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	if !slices.Contains(prof.ActiveTools, tools.TOOL_SEARCH) {
		t.Errorf("tool_search should be auto-mounted when deferred tools exist; active = %v", prof.ActiveTools)
	}
	if !strings.Contains(prof.SystemPrompt, "<available-deferred-tools>") {
		t.Errorf("prompt missing the deferred catalog:\n%s", prof.SystemPrompt)
	}
	if !strings.Contains(prof.SystemPrompt, "web_search") {
		t.Errorf("deferred catalog should name web_search:\n%s", prof.SystemPrompt)
	}
	if !strings.Contains(prof.SystemPrompt, "# Tools") {
		t.Errorf("prompt missing the tools mechanics guide:\n%s", prof.SystemPrompt)
	}

	// The registry's definition must stay as authored — the auto-mount is a
	// profile-level effect, not a mutation of the persona catalog.
	def, _ := reg.Get("deferpersona")
	if slices.Contains(def.ActiveTools, tools.TOOL_SEARCH) {
		t.Errorf("registry definition was mutated; active = %v", def.ActiveTools)
	}
}

func TestResolveMainProfile_NoDeferredNoToolSearchMount(t *testing.T) {
	cfg := config.Get()
	reg := NewAgentRegistry()
	reg.Register(sysprompt.AgentDefinition{
		Name:              "plainpersona",
		As:                []string{"main"},
		ActiveTools:       []tools.ToolName{tools.READ_FILE},
		BuildSystemPrompt: func(sysprompt.PromptContext) string { return "You are plainpersona.\n" },
	})

	prof, err := ResolveMainProfile(cfg, reg, "plainpersona", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	if slices.Contains(prof.ActiveTools, tools.TOOL_SEARCH) {
		t.Errorf("tool_search must not be mounted without deferred tools; active = %v", prof.ActiveTools)
	}
	if strings.Contains(prof.SystemPrompt, "<available-deferred-tools>") {
		t.Errorf("prompt should have no deferred catalog:\n%s", prof.SystemPrompt)
	}
	if strings.Contains(prof.SystemPrompt, "## Deferred tools") {
		t.Errorf("prompt should have no deferred protocol:\n%s", prof.SystemPrompt)
	}
}
