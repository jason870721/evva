package sysprompt

import (
	"strings"
	"testing"
	"time"
)

// Tests for buildMainPrompt — composition order, memory rendering, dev
// gating, presence of every advertised section.

func mainCtx() PromptContext {
	return PromptContext{
		AgentName: "evva",
		Today:     time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC),
		OS:        "darwin",
		Shell:     "zsh",
		WorkDir:   "/tmp",
		EvvaHome:  "/tmp/.evva",
		Env:       "prod",
	}
}

func TestMainAgent_ContainsAllStaticSections(t *testing.T) {
	got := buildMainPrompt(mainCtx())

	for _, want := range []string{
		"You are evva, an interactive coding agent for the terminal.",
		"# Core Rules",
		"# System",
		"# Doing tasks",
		"# Executing actions with care",
		"# Tools",
		"# Tone and style",
		"# Communicating with the user",
		"# Environment",
		"# Session-specific guidance",
		"# Multi-step work",
		"## Deferred tools and `tool_search`",
		"When working with tool results, write down any important information",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing section/marker %q\nfull:\n%s", want, got)
		}
	}
}

func TestMainAgent_SectionOrder(t *testing.T) {
	got := buildMainPrompt(mainCtx())

	// Order mirrors ref Claude Code's getSystemPrompt: static general rules
	// first, then context-specific blocks (environment, memory, session
	// guidance), then catalogs at the bottom.
	markers := []struct {
		name string
		key  string
	}{
		{"identity", "You are evva,"},
		{"core-rules", "# Core Rules"},
		{"system", "# System"},
		{"doing-tasks", "# Doing tasks"},
		{"actions", "# Executing actions with care"},
		{"tools", "# Tools"},
		{"tone", "# Tone and style"},
		{"communicating", "# Communicating with the user"},
		{"environment", "# Environment"},
		{"session-guidance", "# Session-specific guidance"},
		{"summarize", "When working with tool results"},
		{"multi-step", "# Multi-step work"},
	}

	prev := -1
	prevName := ""
	for _, m := range markers {
		idx := strings.Index(got, m.key)
		if idx < 0 {
			t.Fatalf("marker %q not found", m.key)
		}
		if idx <= prev {
			t.Errorf("order violation: %q (at %d) should come after %q (at %d)", m.name, idx, prevName, prev)
		}
		prev, prevName = idx, m.name
	}
}

func TestMainAgent_IdentityFallbackOnBlankName(t *testing.T) {
	ctx := mainCtx()
	ctx.AgentName = ""
	got := buildMainPrompt(ctx)
	if !strings.Contains(got, "You are evva,") {
		t.Errorf("expected fallback name 'evva' on blank AgentName; got prompt without it")
	}
}

func TestMainAgent_NoStaticPlanModeSection(t *testing.T) {
	// Plan-mode guidance moved to per-turn attachments (Phase 11). The
	// static prompt may reference enter_plan_mode in the tools guide, but
	// the dedicated "# Plan mode" workflow section should be gone.
	got := buildMainPrompt(mainCtx())
	if strings.Contains(got, "# Plan mode\n") {
		t.Errorf("# Plan mode section should no longer appear in the static prompt — guidance moved to per-turn attachments")
	}
}

func TestMainAgent_EnvironmentRendersFields(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	for _, want := range []string{
		"OS / shell: darwin / zsh",
		"Working directory: /tmp",
		"AAP_HOME (global: config, skills, memory): /tmp/.evva",
		"Monday May 18 2026",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing env field %q", want)
		}
	}
}

func TestMainAgent_EnvironmentPlaceholdersForEmptyFields(t *testing.T) {
	ctx := PromptContext{AgentName: "evva", Today: time.Now()}
	got := buildMainPrompt(ctx)
	for _, want := range []string{
		"OS / shell: (unknown) / (unknown)",
		"Working directory: (unknown)",
		"AAP_HOME (global: config, skills, memory): (unset)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing placeholder %q", want)
		}
	}
}

func TestMainAgent_RendersProjectMemoryWhenPresent(t *testing.T) {
	ctx := mainCtx()
	ctx.WorkdirMemory = "Conventions: use gofmt. Prefer table-driven tests."
	got := buildMainPrompt(ctx)

	if !strings.Contains(got, "# Project memory (from EVVA.md)") {
		t.Error("project memory heading missing when ProjectMemory set")
	}
	if !strings.Contains(got, "Conventions: use gofmt.") {
		t.Error("project memory body missing")
	}
}

func TestMainAgent_OmitsProjectMemoryWhenEmpty(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	if strings.Contains(got, "Project memory") {
		t.Errorf("project memory heading should be absent when empty:\n%s", got)
	}
}

func TestMainAgent_RendersUserProfileWhenPresent(t *testing.T) {
	ctx := mainCtx()
	ctx.UserProfile = "Preferences: terse output, no summaries."
	got := buildMainPrompt(ctx)

	if !strings.Contains(got, "# User profile (from USER_PROFILE.md)") {
		t.Error("user profile heading missing when UserProfile set")
	}
	if !strings.Contains(got, "Preferences: terse output") {
		t.Error("user profile body missing")
	}
}

func TestMainAgent_OmitsUserProfileWhenEmpty(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	if strings.Contains(got, "User profile") {
		t.Errorf("user profile heading should be absent when empty")
	}
}

func TestMainAgent_BothMemorySectionsWhenBothPresent(t *testing.T) {
	ctx := mainCtx()
	ctx.WorkdirMemory = "proj"
	ctx.UserProfile = "user"
	got := buildMainPrompt(ctx)

	idxProj := strings.Index(got, "# Project memory")
	idxUser := strings.Index(got, "# User profile")
	if idxProj < 0 || idxUser < 0 {
		t.Fatalf("both memory headings should be present; project=%d user=%d", idxProj, idxUser)
	}
	if idxProj >= idxUser {
		t.Errorf("project memory should appear before user profile (proj=%d user=%d)", idxProj, idxUser)
	}
}

func TestMainAgent_DevSectionGatedOnEnv(t *testing.T) {
	// Prod: feedback section MUST NOT appear.
	prod := buildMainPrompt(mainCtx())
	if strings.Contains(prod, "Dev-mode feedback") {
		t.Errorf("dev section leaked into prod prompt")
	}

	// Dev: feedback section MUST appear.
	ctx := mainCtx()
	ctx.Env = "dev"
	dev := buildMainPrompt(ctx)
	if !strings.Contains(dev, "Dev-mode feedback") {
		t.Errorf("dev section missing from dev prompt")
	}
	if !strings.Contains(dev, "`feedback`") {
		t.Errorf("dev section should reference the feedback tool by name")
	}
}

func TestMainAgent_SkillsSection_RendersWhenPopulated(t *testing.T) {
	ctx := mainCtx()
	ctx.Skills = []SkillRef{
		{Name: "git-commit", Description: "how to commit (rules) in a git branch"},
		{Name: "review", Description: "code review checklist"},
	}
	got := buildMainPrompt(ctx)
	for _, want := range []string{
		"# Skills",
		"- git-commit: how to commit (rules) in a git branch",
		"- review: code review checklist",
		"Invoke a skill with the `skill` tool",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestMainAgent_SkillsSection_OmittedWhenEmpty(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	if strings.Contains(got, "# Skills") {
		t.Errorf("# Skills should not appear when Skills is empty")
	}
}

func TestMainAgent_SkillsSection_EmptyDescriptionFallback(t *testing.T) {
	ctx := mainCtx()
	ctx.Skills = []SkillRef{{Name: "solo"}}
	got := buildMainPrompt(ctx)
	if !strings.Contains(got, "- solo\n") && !strings.HasSuffix(got, "- solo") {
		t.Errorf("expected '- solo' line without colon")
	}
	if strings.Contains(got, "- solo:") {
		t.Errorf("trailing colon should be omitted when description is empty")
	}
}

func TestMainAgent_NoTrailingBlankLines(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	if strings.HasSuffix(got, "\n\n") {
		t.Errorf("trailing blank lines: %q", got[max(0, len(got)-10):])
	}
}

func TestMainAgent_NoTripleNewlines(t *testing.T) {
	// Skipped sections must not leave a double-blank scar.
	got := buildMainPrompt(mainCtx())
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("triple-newline detected — a section is leaking blank content")
	}
}

func TestMainAgent_ToolNamesAreLiteralStrings(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	for _, want := range []string{"`read`", "`edit`", "`bash`", "`tool_search`", "`agent`"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected literal tool reference %q in prompt", want)
		}
	}
}

func TestMainAgent_ReferencesSubagentExplore(t *testing.T) {
	got := buildMainPrompt(mainCtx())
	if !strings.Contains(got, `subagent_type: "explore"`) {
		t.Errorf("main agent should advertise subagent_type: \"explore\"")
	}
}
