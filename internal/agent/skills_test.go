package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/skill"
)

// tier1Bundled mirrors internal/skills/bundled.bundledNames. Duplicated here
// (not imported) so this test fails loudly if the live catalog drifts from
// what v1.4 promises to ship.
var tier1Bundled = []string{"commit", "review", "security-review", "simplify", "setup-hooks"}

// A fresh install (empty home dir, no workdir skills dir) must still boot
// with the bundled tier-1 catalog overlaid by loadDiskSkillRegistry.
func TestLoadDiskSkillRegistry_OverlaysBundled(t *testing.T) {
	cfg := &config.Config{
		AppHomeSkillsDir: t.TempDir(), // exists but empty
		WorkDirSkillsDir: filepath.Join(t.TempDir(), "does-not-exist"),
	}
	reg := loadDiskSkillRegistry(cfg)
	for _, name := range tier1Bundled {
		got, ok := reg.Get(name)
		if !ok {
			t.Errorf("bundled skill %q missing from fresh-install registry", name)
			continue
		}
		if got.Source != skill.SourceBundled {
			t.Errorf("%q source: got %v want bundled", name, got.Source)
		}
	}
	if len(reg.Warnings) != 0 {
		t.Errorf("fresh install should not warn: %v", reg.Warnings)
	}
}

// A user's on-disk skill of the same name silently overrides the bundled
// body — no shadowing warning (bundled is the lowest-precedence tier).
func TestLoadDiskSkillRegistry_DiskOverridesBundledSilently(t *testing.T) {
	wd := t.TempDir()
	dir := filepath.Join(wd, "commit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# commit user override\nUSER_BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{WorkDirSkillsDir: wd}

	reg := loadDiskSkillRegistry(cfg)
	got, ok := reg.Get("commit")
	if !ok {
		t.Fatal("commit missing")
	}
	if got.Source != skill.SourceWorkDir {
		t.Errorf("user disk skill should win: source=%v want workdir", got.Source)
	}
	if len(reg.Warnings) != 0 {
		t.Errorf("overriding a bundled skill must be silent; got warnings: %v", reg.Warnings)
	}
	body, _ := reg.LoadBody("commit")
	if !strings.Contains(body, "USER_BODY") {
		t.Errorf("LoadBody returned bundled body, expected user override: %q", body)
	}
	// The remaining tier-1 skills still resolve to bundled.
	if s, _ := reg.Get("review"); s.Source != skill.SourceBundled {
		t.Errorf("review should remain bundled; got %v", s.Source)
	}
}

// nil cfg yields a truly empty catalog — the bundled overlay only applies on
// the disk-load path, matching the documented "explicit empty" semantics.
func TestLoadDiskSkillRegistry_NilCfgEmpty(t *testing.T) {
	reg := loadDiskSkillRegistry(nil)
	if names := reg.Names(); len(names) != 0 {
		t.Errorf("nil cfg should yield an empty catalog; got %v", names)
	}
}

// End-to-end: the registry flattening feeds the Main prompt's # Skills
// section, which must advertise every tier-1 bundled skill by name.
func TestMainPrompt_AdvertisesBundledSkills(t *testing.T) {
	cfg := &config.Config{AppHomeSkillsDir: t.TempDir()}
	refs := refsFromRegistry(loadDiskSkillRegistry(cfg))
	ctx := sysprompt.PromptContext{AgentName: "evva", Today: time.Now(), Skills: refs}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)

	if !strings.Contains(prompt, "# Skills") {
		t.Fatalf("main prompt missing # Skills section")
	}
	for _, name := range tier1Bundled {
		if !strings.Contains(prompt, "- "+name+":") {
			t.Errorf("main prompt # Skills section missing %q as a list item", name)
		}
	}
}

// Subagents must never render a # Skills section even when Skills are
// threaded through PromptContext (AdvertiseSkills: false). Pins A4.
func TestSubagentPrompts_NoSkillsCatalog(t *testing.T) {
	ctxWithSkills := sysprompt.PromptContext{
		Skills: []sysprompt.SkillRef{{Name: "commit", Description: "x"}},
	}
	for _, def := range []sysprompt.AgentDefinition{
		sysprompt.ExploreAgent, sysprompt.GeneralAgent, sysprompt.PlanAgent,
	} {
		if def.AdvertiseSkills {
			t.Errorf("%s should not advertise skills", def.Name)
		}
		if got := def.BuildSystemPrompt(ctxWithSkills); strings.Contains(got, "# Skills") {
			t.Errorf("%s subagent prompt must not contain a # Skills section", def.Name)
		}
	}
}
