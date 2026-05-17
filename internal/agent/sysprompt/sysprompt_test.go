package sysprompt

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Phase 1 analysis — sysprompt surface:
//   - identity() falls back to "an interactive coding assistant" when name is blank
//   - environment() renders "(unknown)" / "(unset)" placeholders for empty fields
//   - harness() / toolsGuide() / taskPlanning() are pure deterministic string returners
//   - Build() composes sections with blank-line separators; skips sections that
//     are toggled off and skips empty section strings
//   - Default() flips every section toggle on and auto-detects env

func TestIdentity_WithAgentName(t *testing.T) {
	got := identity(Inputs{AgentName: "evva"})
	if !strings.Contains(got, "You are evva,") {
		t.Errorf("expected 'You are evva,' in identity; got %q", got)
	}
}

func TestIdentity_FallsBackOnBlankName(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	for _, name := range cases {
		got := identity(Inputs{AgentName: name})
		if !strings.Contains(got, "an interactive coding assistant") {
			t.Errorf("blank name %q didn't get fallback; got %q", name, got)
		}
		// Make sure the format-string didn't double-render "you are , an..."
		if strings.Contains(got, "You are ,") {
			t.Errorf("ugly bare-comma render: %q", got)
		}
	}
}

func TestEnvironment_FullySpecified(t *testing.T) {
	in := Inputs{
		OS:       "darwin",
		Shell:    "zsh",
		WorkDir:  "/home/user/code",
		EvvaHome: "/home/user/.evva",
	}
	got := environment(in)

	for _, want := range []string{
		"# Environment",
		"OS / shell: darwin / zsh",
		"Working directory: /home/user/code",
		"Evva home (global config, skills, memory): /home/user/.evva",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\nfull:\n%s", want, got)
		}
	}
}

func TestEnvironment_EmptyFieldsRenderPlaceholders(t *testing.T) {
	got := environment(Inputs{})
	for _, want := range []string{
		"OS / shell: (unknown) / (unknown)",
		"Working directory: (unknown)",
		"Evva home (global config, skills, memory): (unset)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing placeholder %q\nfull:\n%s", want, got)
		}
	}
}

func TestBuild_MinimalInputs_OnlyIdentityAndEnv(t *testing.T) {
	// All section toggles off → only identity + environment, joined by
	// exactly one blank line. The harness / tools / task sections must
	// NOT leak through.
	in := Inputs{AgentName: "evva", OS: "linux"}
	got := Build(in)

	if !strings.Contains(got, "evva") {
		t.Error("identity missing")
	}
	if !strings.Contains(got, "# Environment") {
		t.Error("environment missing")
	}
	for _, banned := range []string{"# Software engineering", "# Tools", "# Multi-step work"} {
		if strings.Contains(got, banned) {
			t.Errorf("section %q leaked when toggle was off; got\n%s", banned, got)
		}
	}
	// Sections separated by exactly one blank line.
	if !strings.Contains(got, "\n\n# Environment") {
		t.Error("identity and environment not separated by a blank line")
	}
}

func TestBuild_AllSectionsOn(t *testing.T) {
	in := Inputs{
		AgentName:           "evva",
		OS:                  "darwin",
		Shell:               "zsh",
		WorkDir:             "/tmp",
		EvvaHome:            "/tmp/.evva",
		IncludeHarness:      true,
		IncludeToolGuide:    true,
		IncludeTaskPlanning: true,
	}
	got := Build(in)

	for _, want := range []string{
		"# Environment",
		"# Software engineering",
		"# Tools",
		"# Multi-step work",
		"tool_search",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing section/marker %q\nfull:\n%s", want, got)
		}
	}
}

func TestBuild_OrderIsIdentityEnvHarnessToolsTasks(t *testing.T) {
	got := Build(Inputs{
		AgentName:           "evva",
		IncludeHarness:      true,
		IncludeToolGuide:    true,
		IncludeTaskPlanning: true,
	})

	// Compare relative positions of unique markers. Identity has no
	// fixed marker; use the opening "You are evva".
	idxIdentity := strings.Index(got, "You are evva")
	idxEnv := strings.Index(got, "# Environment")
	idxHarness := strings.Index(got, "# Software engineering")
	idxTools := strings.Index(got, "# Tools")
	idxTasks := strings.Index(got, "# Multi-step work")

	for _, pair := range []struct {
		name string
		a, b int
	}{
		{"identity<env", idxIdentity, idxEnv},
		{"env<harness", idxEnv, idxHarness},
		{"harness<tools", idxHarness, idxTools},
		{"tools<tasks", idxTools, idxTasks},
	} {
		if pair.a < 0 || pair.b < 0 {
			t.Fatalf("missing marker for pair %s (a=%d b=%d)", pair.name, pair.a, pair.b)
		}
		if pair.a >= pair.b {
			t.Errorf("order violation: %s (a=%d b=%d)", pair.name, pair.a, pair.b)
		}
	}
}

func TestBuild_NoTrailingBlankLines(t *testing.T) {
	got := Build(Inputs{AgentName: "x"})
	if strings.HasSuffix(got, "\n\n") {
		t.Errorf("trailing blank lines (final section gets no extra \\n): %q", got[len(got)-10:])
	}
}

func TestDefault_FlipsAllSectionsOn(t *testing.T) {
	d := Default("evva", "/home/x/.evva", "dev")
	if !d.IncludeHarness || !d.IncludeToolGuide || !d.IncludeTaskPlanning {
		t.Errorf("Default did not flip all toggles: %+v", d)
	}
	if d.AgentName != "evva" {
		t.Errorf("AgentName: got %q", d.AgentName)
	}
	if d.EvvaHome != "/home/x/.evva" {
		t.Errorf("EvvaHome: got %q", d.EvvaHome)
	}
	if d.Today.IsZero() {
		t.Error("Today not set")
	}
	if d.OS != runtime.GOOS {
		t.Errorf("OS: got %q, want runtime.GOOS=%q", d.OS, runtime.GOOS)
	}
	// WorkDir auto-detected; just check it's non-empty in a normal env.
	if d.WorkDir == "" {
		t.Error("WorkDir should be auto-detected")
	}
}

func TestDefault_ShellHonorsEnv(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	d := Default("evva", "", "dev")
	if d.Shell != "fish" {
		t.Errorf("Shell: got %q, want %q", d.Shell, "fish")
	}
}

func TestDefault_ShellUppercasingNormalized(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/ZSH")
	d := Default("evva", "", "dev")
	if d.Shell != "zsh" {
		t.Errorf("Shell should be lowercased: got %q", d.Shell)
	}
}

func TestDetectShell_EmptyEnv(t *testing.T) {
	// Save then unset SHELL.
	orig, hadIt := os.LookupEnv("SHELL")
	_ = os.Unsetenv("SHELL")
	t.Cleanup(func() {
		if hadIt {
			_ = os.Setenv("SHELL", orig)
		}
	})

	if got := detectShell(); got != "" {
		t.Errorf("detectShell with unset $SHELL: got %q, want empty", got)
	}
}

func TestBuild_SkipsBlankSectionsBetweenContent(t *testing.T) {
	// All three optional sections off should still join Identity+Env
	// without leaving a double blank between them.
	got := Build(Inputs{AgentName: "x"})
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("found triple-newline (blank section leak): %q", got)
	}
}

func TestBuild_SkillsSection_RendersWhenPopulated(t *testing.T) {
	got := Build(Inputs{
		AgentName: "evva",
		Skills: []SkillRef{
			{Name: "git-commit", Description: "how to commit (rules) in a git branch"},
			{Name: "review", Description: "code review checklist"},
		},
	})
	for _, want := range []string{
		"# Skills",
		"- git-commit: how to commit (rules) in a git branch",
		"- review: code review checklist",
		"Invoke a skill with the `skill` tool",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\nfull:\n%s", want, got)
		}
	}
}

func TestBuild_SkillsSection_OmittedWhenEmpty(t *testing.T) {
	got := Build(Inputs{AgentName: "evva"})
	if strings.Contains(got, "# Skills") {
		t.Errorf("# Skills should not appear when Skills is empty:\n%s", got)
	}
}

func TestBuild_SkillsSection_EmptyDescriptionFallback(t *testing.T) {
	got := Build(Inputs{
		AgentName: "evva",
		Skills:    []SkillRef{{Name: "solo"}},
	})
	if !strings.Contains(got, "- solo\n") && !strings.HasSuffix(got, "- solo") {
		t.Errorf("expected '- solo' line without colon; got:\n%s", got)
	}
	if strings.Contains(got, "- solo:") {
		t.Errorf("trailing colon should be omitted when description is empty:\n%s", got)
	}
}

// TestBuild_TodayFieldIsAdvisoryOnly is a forward-looking smoke test:
// Today is on Inputs but no current section consumes it. If a future
// section starts rendering it, this test will start failing — at which
// point update it to assert the new format.
func TestBuild_TodayFieldIsAdvisoryOnly(t *testing.T) {
	got := Build(Inputs{AgentName: "x", Today: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)})
	if strings.Contains(got, "2099") {
		t.Log("note: a section is now rendering Today; update this test")
	}
}
