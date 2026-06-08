package bundled

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/skill"
)

func TestRegister_AddsAll(t *testing.T) {
	reg := skill.NewRegistry()
	warns := Register(reg)
	if len(warns) != 0 {
		t.Fatalf("Register produced warnings: %v", warns)
	}
	for _, name := range bundledNames {
		got, ok := reg.Get(name)
		if !ok {
			t.Errorf("bundled skill %q not registered", name)
			continue
		}
		if got.Source != skill.SourceBundled {
			t.Errorf("%q source: got %v want bundled", name, got.Source)
		}
		if strings.TrimSpace(got.Description) == "" {
			t.Errorf("%q has empty description", name)
		}
	}
}

func TestRegister_NilSafe(t *testing.T) {
	if warns := Register(nil); warns != nil {
		t.Errorf("Register(nil): got %v want nil", warns)
	}
}

func TestRegister_DiskOverridesBundled(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "commit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# commit user override\nUSER_BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, _ := skill.LoadRegistry(home, "")
	warnsBefore := len(reg.Warnings)
	warns := Register(reg)
	if len(warns) != 0 {
		t.Fatalf("Register warnings on override: %v", warns)
	}
	if len(reg.Warnings) != warnsBefore {
		t.Errorf("disk override of bundled must not warn; warnings grew: %v", reg.Warnings)
	}
	got, _ := reg.Get("commit")
	if got.Source != skill.SourceHome {
		t.Errorf("user disk skill should win: source=%v want home", got.Source)
	}
	body, _ := reg.LoadBody("commit")
	if !strings.Contains(body, "USER_BODY") {
		t.Errorf("LoadBody returned bundled body, expected user override: %q", body)
	}
}

func TestRegister_AllBodiesLoadable(t *testing.T) {
	reg := skill.NewRegistry()
	Register(reg)
	for _, name := range bundledNames {
		body, err := reg.LoadBody(name)
		if err != nil {
			t.Errorf("LoadBody(%q): %v", name, err)
			continue
		}
		if !strings.HasPrefix(body, "# ") {
			t.Errorf("%q body should start with a `# ` title line; got %.20q", name, body)
		}
	}
}

func TestRegister_TitleMatchesBundledName(t *testing.T) {
	for _, name := range bundledNames {
		raw, err := readBundled(name)
		if err != nil {
			t.Errorf("readBundled(%q): %v", name, err)
			continue
		}
		titleName, _, err := skill.ParseTitleLine(firstNonBlankLine(raw))
		if err != nil {
			t.Errorf("%q title parse: %v", name, err)
			continue
		}
		if titleName != name {
			t.Errorf("%q SKILL.md title names %q; must match the bundled name", name, titleName)
		}
	}
}

func TestEachSkillHasMatchingFolder(t *testing.T) {
	for _, name := range bundledNames {
		if _, err := readBundled(name); err != nil {
			t.Errorf("bundled name %q has no embedded content/%s/SKILL.md: %v", name, name, err)
		}
	}
}

// TestBuildAgentSkill_Content pins the hygiene rules from the build-agent PRD
// (A4/A6/A7/A9) so a future edit can't silently regress them.
func TestBuildAgentSkill_Content(t *testing.T) {
	body, err := readBundled("build-agent")
	if err != nil {
		t.Fatal(err)
	}
	// A4: no Claude Code tool names / paths leaked in.
	for _, bad := range []string{"~/.claude", "`Task`", "`Bash`", "`Read`", "`Grep`"} {
		if strings.Contains(body, bad) {
			t.Errorf("build-agent body contains forbidden token %q", bad)
		}
	}
	// A6/A7/A9: the load-bearing instructions are present.
	for _, must := range []string{
		"agent.New(",           // constructor decision tree
		"agent.NewWithProfile", // the à-la-carte alternative
		"WithHeadlessBypass",   // the headless warning (A6)
		"internal/",            // the guardrail mentions it (A7)
		"go doc",               // drift-resistance (A9)
		"examples/full-host/main.go",
		"examples/minimal-host/main.go",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("build-agent body missing required reference %q", must)
		}
	}
}

// TestSetupSwarmSkill_Content pins the key references in the setup-swarm skill
// so a future edit can't silently drop load-bearing guidance.
func TestSetupSwarmSkill_Content(t *testing.T) {
	body, err := readBundled("setup-swarm")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"evva-swarm.yml",
		"evva swarm .",
		"evva service start",
		"agents/main/",
		"agents/sub/",
		"system_prompt.md",
		"profile.yml",
		"tools/active.yml",
		"tools/deferr.yml",
		"permission_mode",
		"task_create",
		"send_message",
		"list_members",
		"skill",
		"advertise_skills",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("setup-swarm body missing required reference %q", must)
		}
	}
}
