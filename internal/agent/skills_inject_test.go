package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
)

// fakeSkillLLM is a no-network client so agent.New can build + render without a
// real provider, mirroring the swarm test's fakeLLM.
type fakeSkillLLM struct{ model string }

func (f *fakeSkillLLM) Name() string             { return skillStubProvider }
func (f *fakeSkillLLM) Model() string            { return f.model }
func (*fakeSkillLLM) SupportsDeferLoading() bool { return false }
func (*fakeSkillLLM) Complete(context.Context, []llm.Message, []tools.Tool) (llm.Response, error) {
	return llm.Response{Content: "ok"}, nil
}
func (f *fakeSkillLLM) Stream(ctx context.Context, m []llm.Message, ts []tools.Tool, _ llm.ChunkSink) (llm.Response, error) {
	return f.Complete(ctx, m, ts)
}
func (*fakeSkillLLM) Apply(...llm.Option) {}

const (
	skillStubProvider = "agent_skill_stub"
	skillStubModel    = "stub-m"
)

func init() {
	if !llm.DefaultRegistry().Has(skillStubProvider) {
		_ = llm.DefaultRegistry().Register(skillStubProvider, func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &fakeSkillLLM{model: model}, nil
		})
	}
}

func skillStubConfig(t *testing.T, homeSkillsDir string) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{AppName: "skilltest", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.AppHomeSkillsDir = homeSkillsDir
	cfg.WorkDirSkillsDir = filepath.Join(t.TempDir(), "none")
	cfg.LLMProviderConfig[skillStubProvider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x", Models: []constant.Model{skillStubModel}}
	cfg.DefaultProvider = constant.LLMProvider{Name: skillStubProvider, Models: []constant.Model{skillStubModel}}
	cfg.DefaultModel = constant.Model(skillStubModel)
	return cfg
}

func writeSkillFile(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func memberDef() sysprompt.AgentDefinition {
	// The swarm-member shape: advertise its skills + long-running (date-free +
	// no skill-authoring guidance).
	return sysprompt.AgentDefinition{
		Name:              "member",
		As:                []string{"main"},
		AdvertiseSkills:   true,
		LongRunning:       true,
		BuildSystemPrompt: func(sysprompt.PromptContext) string { return "You are member.\n" },
	}
}

// TestAgentNew_InjectedSkillRegistryDrivesInitialPrompt proves RP-10-2 + RP-10-3
// end to end: a member constructed with WithSkillRegistry advertises ITS OWN skills
// in the INITIAL system prompt (not the cfg-global catalog the pre-injection resolve
// fell back to), and — being long-running — omits the skill-authoring guidance.
func TestAgentNew_InjectedSkillRegistryDrivesInitialPrompt(t *testing.T) {
	globalDir := t.TempDir()
	writeSkillFile(t, globalDir, "globalskill", "# globalskill the global catalog one\nbody\n")
	cfg := skillStubConfig(t, globalDir)

	reg := NewAgentRegistry()
	reg.Register(memberDef())

	custom := skill.NewRegistry()
	if err := custom.Add(skill.SkillMeta{Name: "memberskill", Description: "the member's own", BodyFunc: func() (string, error) { return "x", nil }}); err != nil {
		t.Fatal(err)
	}

	// pkg/agent.New resolves the profile BEFORE WithSkillRegistry is applied, so the
	// pre-injection prompt falls back to the global catalog — the bug RP-10-2 fixes.
	prof, err := ResolveMainProfile(cfg, reg, "member", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(prof.SystemPrompt, "globalskill") {
		t.Fatalf("precondition: pre-injection prompt should fall back to global; got:\n%s", prof.SystemPrompt)
	}

	a, err := New(nil, prof, WithConfig(cfg), WithPersona("member"), WithAgentRegistry(reg), WithSkillRegistry(custom))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sp := a.profile.SystemPrompt
	if !strings.Contains(sp, "memberskill") {
		t.Errorf("initial prompt must list the injected member skill; got:\n%s", sp)
	}
	if strings.Contains(sp, "globalskill") {
		t.Errorf("initial prompt must NOT inherit the cfg-global catalog; got:\n%s", sp)
	}
	if strings.Contains(sp, "How to create a skill") {
		t.Errorf("long-running member must omit skill-authoring guidance (RP-10-3); got:\n%s", sp)
	}
}

// TestAgentNew_EmptyInjectedRegistryAdvertisesNoneNotGlobal proves the empty-registry
// coercion: a member with an injected but EMPTY catalog advertises no skills at all,
// rather than falling back to the cfg-global catalog (RP-10-2).
func TestAgentNew_EmptyInjectedRegistryAdvertisesNoneNotGlobal(t *testing.T) {
	globalDir := t.TempDir()
	writeSkillFile(t, globalDir, "globalskill", "# globalskill the global catalog one\nbody\n")
	cfg := skillStubConfig(t, globalDir)

	reg := NewAgentRegistry()
	reg.Register(memberDef())

	prof, err := ResolveMainProfile(cfg, reg, "member", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	a, err := New(nil, prof, WithConfig(cfg), WithPersona("member"), WithAgentRegistry(reg), WithSkillRegistry(skill.NewRegistry()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sp := a.profile.SystemPrompt
	if strings.Contains(sp, "# Skills") {
		t.Errorf("empty injected registry must not render a # Skills section; got:\n%s", sp)
	}
	if strings.Contains(sp, "globalskill") {
		t.Errorf("empty injected registry must not inherit the global catalog; got:\n%s", sp)
	}
	if a.skillRefs == nil {
		t.Error("empty injected registry must coerce skillRefs to a non-nil empty slice (suppresses global fallback)")
	}
}
