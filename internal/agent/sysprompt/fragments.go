package sysprompt

import (
	"fmt"
	"strings"
	"time"
)

// Fragments are the reusable building blocks composed by the main agent
// builder. Each returns either a non-empty section string ready to join, or
// an empty string when the section should be skipped — the caller filters
// blanks before joining, so a missing section leaves no stray blank line.
//
// Subagents (Explore, General) deliberately do not use these fragments.
// Their prompts are single hand-written strings; mirrors the ref TS pattern
// at ref/src/tools/AgentTool/built-in/{exploreAgent,generalPurposeAgent}.ts.

// identitySection opens the main agent's prompt with a neutral, software-
// engineering-focused introduction. Mirrors the tone of Claude Code's
// getSimpleIntroSection so the model sees prompts close to what it was
// trained on. AgentName falls back to "evva" when blank.
func identitySection(ctx PromptContext) string {
	name := strings.TrimSpace(ctx.AgentName)
	if name == "" {
		name = "evva"
	}
	return fmt.Sprintf(
		"You are %s, a coding agent for the terminal. "+
			"You help users with software engineering tasks: reading, writing, "+
			"refactoring code; running shell commands; designing and planning "+
			"implementation work.", name)
}

// environmentSection gives the model concrete facts about where it is
// running so it picks shell-correct commands, absolute paths, and the
// right place to look for skills / memory.
func environmentSection(ctx PromptContext) string {
	osLabel := defaultIfBlank(ctx.OS, "(unknown)")
	shellLabel := defaultIfBlank(ctx.Shell, "(unknown)")
	workdir := defaultIfBlank(ctx.WorkDir, "(unknown)")
	evvaHome := defaultIfBlank(ctx.EvvaHome, "(unset)")

	today := ctx.Today
	if today.IsZero() {
		today = time.Now()
	}

	return fmt.Sprintf(`# Environment
- OS / shell: %s / %s
- Today: %s
- Working directory: %s
- Evva home (global: config, skills, memory): %s`,
		osLabel, shellLabel,
		today.Format("Monday January 2 2006"),
		workdir, evvaHome)
}

// memorySection wraps a memory file body under a labeled heading so the
// boundary between user-authored content and the agent harness is
// unambiguous. Empty body returns "" — caller filters and skips the section.
func memorySection(heading, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return fmt.Sprintf("# %s\n\n%s", heading, body)
}

// skillsSection advertises the user-installed skill catalog. Each entry is
// rendered as `- <name>: <description>` (description omitted when empty);
// the model is told to invoke the SKILL tool to load full instructions.
//
// Skills are listed in the order the caller provides — the sysprompt
// package does not re-sort, since the registry already returns a stable
// order. An empty slice produces an empty string and the caller skips it.
func skillsSection(skills []SkillRef) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Skills\n")
	fmt.Fprintf(&b, "Invoke a skill with the `%s` tool to load its full instructions. Available skills:\n", nameSkill)
	for _, s := range skills {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			fmt.Fprintf(&b, "- %s\n", name)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", name, desc)
		}
	}
	b.WriteString("You can load skills if you think you may need them to help you process current work better.\n")
	b.WriteString("How to create a skill: locate EvvaHome dir (global skills) or workdir/.evva (workdir skills), create skills/{skill-name}/SKILL.md, the first line in SKILL.md is description (e.g # commit ...), other line is body.")
	return strings.TrimRight(b.String(), "\n")
}

// devFeedbackSection tells the model about the feedback tool available in
// dev mode. Gated on ctx.Env == "dev" by the caller so production agents
// never see it. Mirrors the section currently in sections.go:devSection.
func devFeedbackSection() string {
	return "# Dev-mode feedback (This system prompt only show in EVVA dev mode !)\n" +
		"You have right to access the `" + nameFeedback + "` tool. Use it proactively when you notice something worth reporting to the evva developers to improve EVVA:\n\n" +
		"- `category: \"bug\"` — a tool or behavior is broken (wrong output, crashes, hangs, schema mismatch).\n" +
		"- `category: \"improvement\"` — something works but could be better (tool missing parameter, clumsy workflow, better default, strange prompt).\n" +
		"- `category: \"unnecessary-result\"` — a tool result was confusing, redundant, or wasted tokens.\n" +
		"- `category: \"new-tool\"` — you wish a tool existed for a task you had to work around.\n\n" +
		"The `" + nameFeedback + "` field is freeform markdown. Include enough detail that a developer can act on it without guessing.\n" +
		"Please be more proactive to feedback than passive. (currently this is your main job in dev mode, even more than helping the user)"
}

// joinSections concatenates the non-empty sections with one blank line
// between them. Empty strings are dropped so a skipped section leaves no
// double-blank scar.
func joinSections(sections ...string) string {
	out := sections[:0]
	for _, s := range sections {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n\n")
}

func defaultIfBlank(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
