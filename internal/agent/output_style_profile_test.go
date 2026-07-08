package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/tools"
)

// Tests for applyOutputStyle at the profile-build seam: cfg-driven styles
// reach the built-in main prompt, LongRunning (swarm-resident) personas are
// exempt, a persona-declared meta.yml style wins over the user config, and
// an unknown configured name falls back to default instead of failing.

// styleCfg builds a raw Config pointing at a temp AppHome that carries one
// custom "pirate" style. No SaveFile is involved — fields are set directly
// so the test never touches the process-global config singleton.
func styleCfg(t *testing.T, active string) *config.Config {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "output-styles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: pirate\ndescription: yarr\nkeep-coding-instructions: true\n---\nSpeak like a pirate. PIRATE-MARKER."
	if err := os.WriteFile(filepath.Join(dir, "pirate.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		AppName:     "evva",
		AppHome:     home,
		WorkDir:     t.TempDir(),
		AppEnv:      "prod",
		OutputStyle: active,
	}
}

func TestMainProfile_OutputStyleApplied(t *testing.T) {
	cfg := styleCfg(t, "pirate")
	prof := mainProfileForDef(sysprompt.MainAgent, cfg, constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO,
		[]sysprompt.SkillRef{}, memdir.Snapshot{}, nil, nil, "")
	if !strings.Contains(prof.SystemPrompt, "# Output Style: pirate") ||
		!strings.Contains(prof.SystemPrompt, "PIRATE-MARKER") {
		t.Error("configured disk style did not reach the built-in main prompt")
	}
	if !strings.Contains(prof.SystemPrompt, "# Doing tasks") {
		t.Error("keep-coding-instructions: true style must keep the doing-tasks doctrine")
	}
}

func TestMainProfile_OutputStyleUnknownFallsBack(t *testing.T) {
	cfg := styleCfg(t, "ghost")
	prof := mainProfileForDef(sysprompt.MainAgent, cfg, constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO,
		[]sysprompt.SkillRef{}, memdir.Snapshot{}, nil, nil, "")
	if strings.Contains(prof.SystemPrompt, "# Output Style:") {
		t.Error("unknown style name must fall back to the default (no overlay)")
	}
	if !strings.Contains(prof.SystemPrompt, "# Doing tasks") {
		t.Error("fallback prompt lost the doing-tasks doctrine")
	}
}

func TestMainProfile_OutputStyleSkippedForLongRunning(t *testing.T) {
	cfg := styleCfg(t, "pirate")
	def := sysprompt.MainAgent
	def.LongRunning = true
	prof := mainProfileForDef(def, cfg, constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO,
		[]sysprompt.SkillRef{}, memdir.Snapshot{}, nil, nil, "")
	if strings.Contains(prof.SystemPrompt, "# Output Style:") {
		t.Error("swarm-resident (LongRunning) personas must never get an output style")
	}
}

func TestDiskPersona_MetaOutputStyleWinsOverConfig(t *testing.T) {
	cfg := styleCfg(t, "pirate")
	def := sysprompt.AgentDefinition{
		Name:        "tutor",
		As:          []string{"main"},
		ActiveTools: []tools.ToolName{tools.READ_FILE},
		OutputStyle: "Explanatory",
		BuildSystemPrompt: func(sysprompt.PromptContext) string {
			return "You are tutor. PERSONA-BODY."
		},
	}
	prof := mainProfileFromDiskAgent(def, cfg, constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO,
		[]sysprompt.SkillRef{}, memdir.Snapshot{}, nil, nil, "")
	if !strings.Contains(prof.SystemPrompt, "# Output Style: Explanatory") {
		t.Error("persona-declared output_style must win over the user's config")
	}
	if strings.Contains(prof.SystemPrompt, "# Output Style: pirate") {
		t.Error("user-configured style leaked past a persona-declared one")
	}
}
