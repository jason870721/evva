// Package sysprompt builds the system prompt for each kind of agent evva
// runs. The package exposes one AgentDefinition per built-in agent (see
// agent_def.go) whose BuildSystemPrompt function turns a PromptContext into
// the full prompt string. The Main agent's prompt is composed from shared
// fragments (identity, environment, memory, skills, dev) plus per-agent
// harness/tools/planning blocks; subagents (Explore, General) own a single
// hand-written string mirroring ref/src/tools/AgentTool/built-in/*Agent.ts.
//
// The package is dependency-light on purpose: it imports only stdlib. The
// caller (cmd/evva/main.go via agent.Main) reads memory through the memdir
// package and threads it into PromptContext.WorkdirMemory / .MemoryIndex. The
// skill registry is similarly flattened by the caller into []SkillRef so
// sysprompt does not depend on pkg/skill.
//
// Tool names interpolated into prompts live in toolnames.go and are
// drift-checked against internal/tools/name.go by toolnames_link_test.go.
package sysprompt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DeferredToolSpec is the prompt-side view of one deferred tool. Only the
// Name is included — full schemas are fetched on demand via tool_search,
// matching ref/ Claude Code's name-only advertisement.
type DeferredToolSpec struct {
	Name string
}

// SkillRef is the prompt-side view of a user-installed skill — just the
// name and description we advertise to the model. The sysprompt package
// deliberately does not depend on pkg/skill; the caller
// flattens the skill registry into this struct.
type SkillRef struct {
	Name        string
	Description string
}

// PromptContext is the input bundle for every AgentDefinition.BuildSystemPrompt.
// Build once per agent at construction time and pass by value — builders
// read-only. Zero values render cleanly: empty environment fields become
// "(unknown)" / "(unset)"; empty Skills / ProjectMemory / UserProfile
// suppress their respective sections.
type PromptContext struct {
	// Identity
	AgentName string    // e.g. "evva" — the name the main agent introduces itself as.
	Today     time.Time // anchors the model in absolute time; zero = use time.Now() at render.

	// OmitDate drops the "- Today:" line from the environment section. Long-
	// running personas (swarm members) set it so the system-prompt prefix stays
	// bit-stable across rebuilds — a drifting date would bust the prompt cache for
	// a swarm that runs for weeks. The time they need arrives in wake/run prompts,
	// never in the static system prompt (RP-5). Driven by AgentDefinition.LongRunning.
	OmitDate bool

	// Environment
	OS       string // runtime.GOOS — "darwin", "linux", "windows".
	Shell    string // basename of $SHELL — "zsh", "bash", ...
	WorkDir  string // absolute path of the current working directory.
	EvvaHome string // ~/.evva — where skills, memory, and config live.
	Env      string // "dev" | "prod" — dev gates the feedback section.
	Model    string // canonical model id ("claude-opus-4-7", "deepseek-chat", ...). Empty = skip the model line in env block.

	// Catalogs
	Skills         []SkillRef         // advertised skill list; empty = skip the section.
	DeferredTools  []DeferredToolSpec // deferred-tool catalog; rendered as a <functions> block in the main prompt. Empty = skip the section.

	// Memory (loaded by internal/memdir)
	WorkdirMemory string // contents of <workdir>/EVVA.md (user-authored); "" = skip.
	MemoryIndex   string // <appHome>/memory/MEMORY.md body (truncated, inject-ready); "" = skip.

	// EnableAutoMemory gates the typed-memory guidance + the MEMORY.md index
	// sections in the main prompt. false → both sections are suppressed so the
	// model isn't told about a memory system it can't use this session.
	EnableAutoMemory bool
}

// DetectContext returns a PromptContext with the runtime-detectable fields
// (Today, OS, Shell, WorkDir) populated. Caller fills AgentName, EvvaHome,
// Env, Skills, and the two memory fields — those live in configs.AppConfig
// and on the memdir.Snapshot, which the sysprompt module deliberately does
// not import.
func DetectContext(agentName, evvaHome, env string) PromptContext {
	workdir, _ := os.Getwd()
	return PromptContext{
		AgentName: agentName,
		Today:     time.Now(),
		OS:        runtime.GOOS,
		Shell:     detectShell(),
		WorkDir:   workdir,
		EvvaHome:  evvaHome,
		Env:       env,
	}
}

// detectShell returns the basename of $SHELL, lowercased — "zsh", "bash",
// "fish", etc. Falls back to "" when SHELL is unset (Windows, restricted
// containers); environmentSection renders that as "(unknown)" so the line
// stays readable.
func detectShell() string {
	s := os.Getenv("SHELL")
	if s == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(s))
}
