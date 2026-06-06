package agent

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	config "github.com/johnny1110/evva/pkg/config"
)

// TestResolveMainProfile_LongRunningOmitsDate proves the RP-5 wiring end to end:
// AgentDefinition.LongRunning threads to PromptContext.OmitDate in
// mainProfileFromDiskAgent, so a long-running persona's composed system prompt
// carries the environment section WITHOUT the drifting "- Today:" date (keeping
// the prompt-cache prefix bit-stable), while an ordinary persona keeps the date.
func TestResolveMainProfile_LongRunningOmitsDate(t *testing.T) {
	cfg := config.Get()
	reg := NewAgentRegistry()
	mkDef := func(name string, longRunning bool) sysprompt.AgentDefinition {
		return sysprompt.AgentDefinition{
			Name:              name,
			As:                []string{"main"},
			LongRunning:       longRunning,
			BuildSystemPrompt: func(sysprompt.PromptContext) string { return "You are " + name + ".\n" },
		}
	}
	reg.Register(mkDef("longrunner", true))
	reg.Register(mkDef("shortrunner", false))

	long, err := ResolveMainProfile(cfg, reg, "longrunner", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile(longrunner): %v", err)
	}
	if !strings.Contains(long.SystemPrompt, "# Environment") {
		t.Fatalf("long-running prompt missing environment section:\n%s", long.SystemPrompt)
	}
	if strings.Contains(long.SystemPrompt, "Today") {
		t.Errorf("long-running persona must omit the date; got:\n%s", long.SystemPrompt)
	}

	short, err := ResolveMainProfile(cfg, reg, "shortrunner", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile(shortrunner): %v", err)
	}
	if !strings.Contains(short.SystemPrompt, "- Today:") {
		t.Errorf("ordinary persona should keep the date; got:\n%s", short.SystemPrompt)
	}
}
