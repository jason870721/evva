// Package sysprompt assembles the agent's system prompt from composable
// sections — identity, environment, software-engineering harness, tool-use
// guidance (including the deferred-tool / TOOL_SEARCH protocol), and the
// multi-step task-planning protocol.
//
// The module is dependency-light on purpose: it takes a plain Inputs struct
// and returns a string. Callers (today: cmd/evva/main.go) fill the identity
// and Evva-home fields from configs.AppConfig; everything else (OS, shell,
// workdir, today) is auto-detected by Default().
//
// Sections are joined with a blank line; disabled sections drop out cleanly.
// The order is fixed because the model reads top-to-bottom — identity first,
// then environment context, then the rules that govern conduct, then the
// tool-and-planning protocols.
package sysprompt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SkillRef is the prompt-side view of a user-installed skill — just the
// name and description we want to advertise to the model. The sysprompt
// package deliberately does not depend on internal/tools/skill so the
// dependency graph stays one-way; the caller flattens the skill registry
// into this struct.
type SkillRef struct {
	Name        string
	Description string
}

// Inputs is the composable input to Build. Zero values are tolerated: any
// environment field that is empty is rendered as "(unknown)"; toggles
// default to false so callers must opt in to each section. Use Default() to
// get an Inputs preset with every section enabled and the environment
// fields auto-detected.
type Inputs struct {
	// Identity
	AgentName string    // e.g. "evva" — the name the agent introduces itself as.
	Today     time.Time // anchors the model in absolute time; zero = omit the date line.

	// Environment
	OS       string // runtime.GOOS — "darwin", "linux", "windows".
	Shell    string // basename of $SHELL — "zsh", "bash", ...
	WorkDir  string // absolute path of the current working directory.
	EvvaHome string // ~/.evva — where skills, memory, and config live.

	// Section toggles. All four default to false so a caller that wants a
	// minimal prompt (e.g. a tightly-scoped subagent) can leave them off.
	// Default() flips them all on for the main agent.
	IncludeHarness      bool // software-engineering conduct rules.
	IncludeToolGuide    bool // dedicated-tool preference + TOOL_SEARCH protocol.
	IncludeTaskPlanning bool // when and how to use the task_* tool family.

	// Skills is the advertised skill catalog. Build renders a `# Skills`
	// section iff len(Skills) > 0 — there is no separate toggle, since
	// "no skills installed" and "don't advertise skills" are the same
	// observable state.
	Skills []SkillRef

	Env string // dev or prod
}

// Default returns an Inputs preset suitable for the main agent: every
// section enabled, today set to time.Now(), OS / shell / workdir detected
// from the current process. Caller supplies agent name and Evva home
// because those live in configs.AppConfig and the sysprompt module
// deliberately does not import configs (keeps the dependency graph one-way
// and the module trivially testable).
func Default(agentName, evvaHome, env string) Inputs {
	workdir, _ := os.Getwd()
	return Inputs{
		AgentName:           agentName,
		Today:               time.Now(),
		OS:                  runtime.GOOS,
		Shell:               detectShell(),
		WorkDir:             workdir,
		EvvaHome:            evvaHome,
		IncludeHarness:      true,
		IncludeToolGuide:    true,
		IncludeTaskPlanning: true,
		Env:                 env,
	}
}

// Build composes the system prompt from in. Section order is fixed; a
// disabled or empty section is skipped without leaving a stray blank line.
func Build(in Inputs) string {
	parts := []string{
		identity(in),
		environment(in),
	}
	if in.IncludeHarness {
		parts = append(parts, harness())
	}
	if in.IncludeToolGuide {
		parts = append(parts, toolsGuide())
	}
	if in.IncludeTaskPlanning {
		parts = append(parts, taskPlanning())
	}
	if len(in.Skills) > 0 {
		parts = append(parts, skillsSection(in.Skills))
	}
	if in.Env == "dev" {
		parts = append(parts, devSection())
	}

	out := parts[:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

// detectShell returns the basename of $SHELL, lowercased — "zsh", "bash",
// "fish", etc. Falls back to "" when SHELL is unset (Windows, restricted
// containers); Build renders that as "(unknown)" so the line stays readable.
func detectShell() string {
	s := os.Getenv("SHELL")
	if s == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(s))
}
