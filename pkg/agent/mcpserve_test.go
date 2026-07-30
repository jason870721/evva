package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/mcp"
)

// The spawner is only useful if it actually satisfies the interface pkg/mcp
// serves through. This assertion is the whole reason the seam exists (pkg/agent
// imports pkg/mcp, so the reverse edge would be an import cycle), so it is
// worth pinning explicitly rather than leaving it to a call site.
var _ mcp.PersonaSpawner = (*PersonaSpawner)(nil)

func newTestSpawner(t *testing.T) *PersonaSpawner {
	t.Helper()
	sp, warns := NewPersonaSpawner(Config{
		AppConfig: &config.Config{AppHome: t.TempDir()},
	})
	for _, w := range warns {
		t.Logf("registry warning: %v", w)
	}
	return sp
}

func TestPersonaSpawnerListsBuiltInPersonas(t *testing.T) {
	sp := newTestSpawner(t)
	got := sp.Personas()
	if len(got) == 0 {
		t.Fatal("no personas listed; the allowlist could never validate")
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	// "evva" is the built-in fallback persona; if it is not main-tier the
	// registry wiring changed underneath us.
	if !slices.Contains(names, "evva") {
		t.Errorf("main personas = %v, want the built-in \"evva\" among them", names)
	}
}

func TestPersonaSpawnerRejectsUnknownPersona(t *testing.T) {
	// Fails before any agent is constructed — an unknown name must not cost a
	// provider call, and must not silently degrade to the default persona the
	// way agent.New's own fallback does.
	_, err := newTestSpawner(t).RunPersona(context.Background(), "definitely-not-a-persona", "hi")
	if err == nil {
		t.Fatal("want an error for an unknown persona")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-persona") {
		t.Errorf("error should name the persona, got %v", err)
	}
}

func TestPersonaSpawnerReusesSuppliedRegistry(t *testing.T) {
	// A host that built its own registry (the evva→nono pattern) must not have
	// it silently replaced by a disk scan.
	reg, _ := BuildAgentRegistry(t.TempDir())
	reg.Register(AgentDefinition{
		Name:      "custom-one",
		WhenToUse: "A host-registered persona.",
		As:        []string{"main"},
	})
	sp, _ := NewPersonaSpawner(Config{
		AppConfig: &config.Config{AppHome: t.TempDir()},
		Personas:  reg,
	})

	var found *mcp.PersonaInfo
	for _, p := range sp.Personas() {
		if p.Name == "custom-one" {
			found = &p
			break
		}
	}
	if found == nil {
		t.Fatal("host-registered persona missing from the spawner's catalog")
	}
	if found.WhenToUse != "A host-registered persona." {
		t.Errorf("WhenToUse not carried through: %q", found.WhenToUse)
	}
}
