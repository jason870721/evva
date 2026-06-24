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
		"# Priorities",
		"# Core Principles",
		"# System",
		"# Doing tasks",
		"# Executing actions with care",
		"# Tools",
		"# Tone and style",
		"# Communicating with the user",
		"# Environment",
		"# Session-specific guidance",
		"# Context Preservation",
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
		{"priorities", "# Priorities"},
		{"core-principles", "# Core Principles"},
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

func TestMainAgent_RendersMemoryIndexWhenPresent(t *testing.T) {
	ctx := mainCtx()
	ctx.EnableAutoMemory = true
	ctx.MemoryIndex = "- [role](user/role.md) — senior Go dev"
	got := buildMainPrompt(ctx)

	if !strings.Contains(got, "# Memory index (from /tmp/.evva/memory/MEMORY.md)") {
		t.Error("memory index heading missing when MemoryIndex set")
	}
	if !strings.Contains(got, "- [role](user/role.md) — senior Go dev") {
		t.Error("memory index body missing")
	}
	// The typed-memory guidance block renders alongside the index.
	if !strings.Contains(got, "# Memory") || !strings.Contains(got, "## Types of memory") {
		t.Error("typed-memory guidance block missing when auto-memory on")
	}
}

func TestMainAgent_OmitsMemorySectionsWhenAutoMemoryOff(t *testing.T) {
	ctx := mainCtx() // EnableAutoMemory defaults false
	ctx.MemoryIndex = "- [x](x.md) — hook"
	got := buildMainPrompt(ctx)
	if strings.Contains(got, "# Memory index") {
		t.Errorf("memory index should be absent when auto-memory is off")
	}
	if strings.Contains(got, "## Types of memory") {
		t.Errorf("typed-memory guidance should be absent when auto-memory is off")
	}
}

func TestMainAgent_ProjectMemoryBeforeMemoryIndex(t *testing.T) {
	ctx := mainCtx()
	ctx.EnableAutoMemory = true
	ctx.WorkdirMemory = "proj"
	ctx.MemoryIndex = "idx"
	got := buildMainPrompt(ctx)

	idxProj := strings.Index(got, "# Project memory")
	idxMem := strings.Index(got, "# Memory index")
	if idxProj < 0 || idxMem < 0 {
		t.Fatalf("both headings should be present; project=%d index=%d", idxProj, idxMem)
	}
	if idxProj >= idxMem {
		t.Errorf("EVVA.md project memory should appear before the memory index (proj=%d index=%d)", idxProj, idxMem)
	}
}

func TestMainAgent_RendersRepoMapWhenPresent(t *testing.T) {
	ctx := mainCtx()
	ctx.RepoMap = "# Repo map\n\nRepo map — 12 symbols across 3 packages.\n\n### pkg/foo\n  Struct Foo  foo.go:5\n"
	got := buildMainPrompt(ctx)

	if !strings.Contains(got, "# Repo map") {
		t.Error("repo map heading missing when RepoMap set")
	}
	if !strings.Contains(got, "Struct Foo") {
		t.Error("repo map body missing")
	}
}

// TestMainAgent_OmitsRepoMapWhenEmpty pins the opt-in-off invariant (A1): with
// no RepoMap set, the prompt is byte-identical to a session that never knew the
// feature existed.
func TestMainAgent_OmitsRepoMapWhenEmpty(t *testing.T) {
	withMap := mainCtx()
	withMap.RepoMap = "# Repo map\n\nsome content\n"

	withoutMap := buildMainPrompt(mainCtx())
	if strings.Contains(withoutMap, "# Repo map") {
		t.Errorf("repo map heading should be absent when RepoMap is empty:\n%s", withoutMap)
	}

	// The only delta between the two prompts is the injected map section.
	if buildMainPrompt(mainCtx()) != withoutMap {
		t.Error("buildMainPrompt is not deterministic for an empty RepoMap")
	}
	if !strings.Contains(buildMainPrompt(withMap), "# Repo map") {
		t.Error("repo map should render once RepoMap is set")
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

// TestSkillsSection_OmitAuthoring (RP-10-3): the slim mode (omitAuthoring=true, used
// by long-running swarm members) drops the "how to create a skill" guidance while
// keeping the list + the load instruction; the full mode (evva) keeps the guidance.
func TestSkillsSection_OmitAuthoring(t *testing.T) {
	refs := []SkillRef{{Name: "commit", Description: "make a commit"}}

	full := skillsSection(refs, false)
	if !strings.Contains(full, "How to create a skill") {
		t.Errorf("full skills section should include authoring guidance; got:\n%s", full)
	}

	slim := skillsSection(refs, true)
	if strings.Contains(slim, "How to create a skill") {
		t.Errorf("slim skills section (omitAuthoring) must drop authoring guidance; got:\n%s", slim)
	}

	// Both still list the skill and tell the model to load it with the skill tool.
	for label, s := range map[string]string{"full": full, "slim": slim} {
		if !strings.Contains(s, "- commit: make a commit") {
			t.Errorf("%s section missing the skill list item; got:\n%s", label, s)
		}
		if !strings.Contains(s, "Invoke a skill with the `skill` tool") {
			t.Errorf("%s section missing the load instruction; got:\n%s", label, s)
		}
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
