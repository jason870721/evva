package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/alarm"
	"github.com/johnny1110/evva/pkg/tools/cron"
)

func suffixTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{AppName: "t", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestMainProfileForDef_SwarmResident(t *testing.T) {
	cfg := suffixTestConfig(t)
	def := sysprompt.MainAgent
	def.LongRunning = true
	def.PromptSuffix = "## SWARM PROTOCOL MARKER"

	p := mainProfileForDef(def, cfg, cfg.DefaultProvider, cfg.DefaultModel, []sysprompt.SkillRef{}, memdir.Snapshot{}, nil, nil, "")

	if !strings.HasSuffix(p.SystemPrompt, "## SWARM PROTOCOL MARKER") {
		t.Fatalf("suffix must terminate the prompt; tail: %q", p.SystemPrompt[len(p.SystemPrompt)-80:])
	}
	if strings.Contains(p.SystemPrompt, "- Today:") {
		t.Fatalf("LongRunning must omit the date line")
	}
	if strings.Contains(p.SystemPrompt, "How to create a skill") {
		t.Fatalf("LongRunning must omit skill-authoring guidance")
	}
	for _, n := range slices.Concat(alarm.Names(), cron.Names()) {
		if slices.Contains(p.DeferredTools, n) || slices.Contains(p.ActiveTools, n) {
			t.Fatalf("swarm-resident profile must not carry solo scheduling tool %q", n)
		}
	}
	if slices.Contains(p.ActiveTools, tools.SCHEDULE_WAKEUP) {
		t.Fatalf("swarm-resident profile must not carry schedule_wakeup")
	}
}

func TestMainProfile_SoloUnchanged(t *testing.T) {
	cfg := suffixTestConfig(t)
	p := Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, []sysprompt.SkillRef{}, memdir.Snapshot{}, nil)
	if strings.Contains(p.SystemPrompt, "SWARM PROTOCOL") {
		t.Fatalf("solo prompt must carry no swarm suffix")
	}
	if !strings.Contains(p.SystemPrompt, "- Today:") {
		t.Fatalf("solo prompt must keep the date line")
	}
	if !slices.Contains(p.DeferredTools, tools.ALARM_CREATE) {
		t.Fatalf("solo profile must keep solo alarm tools")
	}
	if !slices.Contains(p.ActiveTools, tools.SCHEDULE_WAKEUP) {
		t.Fatalf("solo profile must keep schedule_wakeup")
	}
}

func TestResolveMainProfile_EvvaSuffixFromRegistry(t *testing.T) {
	cfg := suffixTestConfig(t)
	reg, _ := BuildAgentRegistry(t.TempDir())
	def, ok := reg.Get("evva")
	if !ok {
		t.Fatal("built-in evva missing from registry")
	}
	def.LongRunning = true
	def.PromptSuffix = "## SWARM PROTOCOL MARKER"
	reg.Register(def)

	prof, err := resolveMainProfileWithExtra(cfg, reg, "evva", []sysprompt.SkillRef{}, memdir.Snapshot{}, nil, cfg.DefaultProvider, cfg.DefaultModel, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(prof.SystemPrompt, "## SWARM PROTOCOL MARKER") {
		t.Fatalf("registry-composed evva def must surface its suffix")
	}
	if strings.Count(prof.SystemPrompt, "## SWARM PROTOCOL MARKER") != 1 {
		t.Fatalf("suffix must appear exactly once")
	}
	// Re-resolve (the ReloadSkills / MCP re-render path) — suffix must survive.
	again, err := resolveMainProfileWithExtra(cfg, reg, "evva", []sysprompt.SkillRef{}, memdir.Snapshot{}, nil, cfg.DefaultProvider, cfg.DefaultModel, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(again.SystemPrompt, "## SWARM PROTOCOL MARKER") != 1 {
		t.Fatalf("re-render must keep exactly one suffix")
	}
}

func TestDiskMainProfile_SuffixAndStrip(t *testing.T) {
	cfg := suffixTestConfig(t)
	def := sysprompt.AgentDefinition{
		Name: "nono", As: []string{"main"},
		BuildSystemPrompt: func(_ sysprompt.PromptContext) string { return "I am nono." },
		ActiveTools:       []tools.ToolName{tools.READ_FILE, tools.SCHEDULE_WAKEUP},
		DeferredTools:     append([]tools.ToolName{}, alarm.Names()...),
		LongRunning:       true,
		PromptSuffix:      "## SWARM PROTOCOL MARKER",
	}
	p := mainProfileFromDiskAgent(def, cfg, cfg.DefaultProvider, cfg.DefaultModel, nil, memdir.Snapshot{}, nil, nil, "")
	if !strings.HasSuffix(p.SystemPrompt, "## SWARM PROTOCOL MARKER") {
		t.Fatalf("disk persona suffix missing")
	}
	if slices.Contains(p.ActiveTools, tools.SCHEDULE_WAKEUP) {
		t.Fatalf("strip must remove schedule_wakeup from a long-running disk persona")
	}
	for _, n := range alarm.Names() {
		if slices.Contains(p.DeferredTools, n) {
			t.Fatalf("strip must remove solo alarm tool %q", n)
		}
	}
	if !slices.Contains(p.ActiveTools, tools.READ_FILE) {
		t.Fatalf("unrelated tools must survive the strip")
	}
}
