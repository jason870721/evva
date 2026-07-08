package agent

import (
	"strings"
	"testing"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/internal/memdir"
)

// TestSwitchOutputStyle_RebuildsPromptAndPersists switches the root agent
// to the built-in Explanatory style and verifies the live rebuild: the new
// system prompt carries the style section, the session resets (the prompt
// prefix changed), and the choice persists to config. Then switches back
// to default and verifies the overlay is gone.
func TestSwitchOutputStyle_RebuildsPromptAndPersists(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()

	prevStyle := cfg.OutputStyle
	prevDefault := cfg.DefaultProfile
	t.Cleanup(func() {
		_ = cfg.SetOutputStyle(prevStyle)
		_ = cfg.SetDefaultProfile(prevDefault)
	})

	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof, WithName("test"), WithAgentRegistry(reg), WithPersona("evva"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	if got := a.OutputStyleName(); got != "default" && got != prevStyle {
		t.Errorf("initial OutputStyleName = %q", got)
	}

	if err := a.SwitchOutputStyle("Explanatory"); err != nil {
		t.Fatalf("SwitchOutputStyle: %v", err)
	}
	if !strings.Contains(a.profile.SystemPrompt, "# Output Style: Explanatory") {
		t.Error("rebuilt prompt missing the Explanatory style section")
	}
	if got := len(a.session.GetMessages()); got != 0 {
		t.Errorf("post-switch session: want 0 messages, got %d", got)
	}
	if got := cfg.GetOutputStyle(); got != "Explanatory" {
		t.Errorf("style not persisted: got %q", got)
	}
	if got := a.OutputStyleName(); got != "Explanatory" {
		t.Errorf("OutputStyleName after switch = %q", got)
	}

	if err := a.SwitchOutputStyle("default"); err != nil {
		t.Fatalf("SwitchOutputStyle(default): %v", err)
	}
	if strings.Contains(a.profile.SystemPrompt, "# Output Style:") {
		t.Error("default switch should remove the style section")
	}
}

// TestSwitchOutputStyle_RejectsUnknown pins the validation path: a typo
// must not persist or rebuild anything.
func TestSwitchOutputStyle_RejectsUnknown(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()
	prevStyle := cfg.OutputStyle
	t.Cleanup(func() { _ = cfg.SetOutputStyle(prevStyle) })

	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof, WithName("test"), WithAgentRegistry(reg), WithPersona("evva"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	if err := a.SwitchOutputStyle("ghost-style"); err == nil {
		t.Fatal("expected error for unknown style")
	}
	if got := cfg.OutputStyle; got != prevStyle {
		t.Errorf("rejected switch must not persist: got %q, want %q", got, prevStyle)
	}
}
