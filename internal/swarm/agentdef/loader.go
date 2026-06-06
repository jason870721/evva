package agentdef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	"gopkg.in/yaml.v3"
)

// Role marks a member as the Leader or a Worker. In Veronica BOTH are root
// agents (the main/sub split is a leadership role, not evva's spawn semantics);
// Role drives the As mapping and which tool set the space injects (SPRD-1-7).
type Role string

const (
	RoleLeader Role = "leader"
	RoleWorker Role = "worker"
)

// Loaded is one agent directory turned into the public SDK objects needed to
// construct a live agent (done in SPRD-1-4), plus the parsed extras the
// scheduler/space consume.
type Loaded struct {
	Def      agent.AgentDefinition // ready for agent.New / registry.Register
	Skills   *skill.Registry       // never nil (an empty registry when no skills/)
	Schedule *Schedule             // nil when the profile declares no schedule
	Effort   string                // profile effort; applied at construction (1-4)
	Role     Role
}

// Warning is a non-fatal load issue (e.g. a malformed SKILL.md). Surfaced by
// BuildAll; never blocks the build.
type Warning struct {
	Agent string
	Msg   string
}

func (w Warning) Error() string { return fmt.Sprintf("agentdef: %s: %s", w.Agent, w.Msg) }

// Loader turns on-disk agent directories into Loaded values. It holds no
// process-wide state — Build is pure and re-callable, which is what makes
// dynamic hot-load (SPRD-1-6) and restart-rebuild (SPRD-1-11) just another call.
type Loader struct{}

// NewLoader returns a Loader.
func NewLoader() *Loader { return &Loader{} }

// profileYml is the on-disk schema for <agent>/profile.yml. Every field is
// optional.
type profileYml struct {
	Model           string       `yaml:"model"`
	Effort          string       `yaml:"effort"`
	WhenToUse       string       `yaml:"when_to_use"`
	InjectMemory    bool         `yaml:"inject_memory"`
	AdvertiseSkills bool         `yaml:"advertise_skills"`
	Schedule        *scheduleYml `yaml:"schedule"`
}

// Build reads ONE agent directory (agents/{main,sub}/{name}/) and returns a
// Loaded. It is pure and side-effect-free: only reads, no writes, no global
// state — calling it twice on the same dir yields equal results.
//
// system_prompt.md is required; tools/active.yml, tools/deferr.yml, profile.yml,
// and skills/ are optional (absent → empty/zero).
func (l *Loader) Build(dir string, role Role) (Loaded, error) {
	name := filepath.Base(dir)

	promptBytes, err := os.ReadFile(filepath.Join(dir, "system_prompt.md"))
	if err != nil {
		return Loaded{}, fmt.Errorf("agentdef: %s: read system_prompt.md: %w", name, err)
	}
	prompt := string(promptBytes)
	if strings.TrimSpace(prompt) == "" {
		return Loaded{}, fmt.Errorf("agentdef: %s: system_prompt.md is empty", name)
	}

	active, err := readToolList(filepath.Join(dir, "tools", "active.yml"))
	if err != nil {
		return Loaded{}, fmt.Errorf("agentdef: %s: %w", name, err)
	}
	deferred, err := readToolList(filepath.Join(dir, "tools", "deferr.yml"))
	if err != nil {
		return Loaded{}, fmt.Errorf("agentdef: %s: %w", name, err)
	}

	prof, err := readProfile(filepath.Join(dir, "profile.yml"))
	if err != nil {
		return Loaded{}, fmt.Errorf("agentdef: %s: %w", name, err)
	}

	sched, err := parseScheduleYml(prof.Schedule)
	if err != nil {
		return Loaded{}, fmt.Errorf("agentdef: %s: %w", name, err)
	}

	// LoadRegistry never errors (a missing skills/ dir is the normal state);
	// per-skill problems land in skills.Warnings, surfaced by BuildAll.
	skills, _ := skill.LoadRegistry(filepath.Join(dir, "skills"), "")

	def := agent.AgentDefinition{
		Name:            name,
		WhenToUse:       prof.WhenToUse,
		As:              asForRole(role),
		InjectMemory:    prof.InjectMemory,
		AdvertiseSkills: prof.AdvertiseSkills,
		ActiveTools:     active,
		DeferredTools:   deferred,
		Model:           prof.Model,
		SystemPrompt:    prompt,
	}
	return Loaded{Def: def, Skills: skills, Schedule: sched, Effort: prof.Effort, Role: role}, nil
}

// BuildAll resolves every member of a manifest to its directory under
// <workdir>/agents/{main,sub}/ and Builds it (leader first, then workers in
// order). The returned warnings aggregate each agent's skill-load warnings.
func (l *Loader) BuildAll(workdir string, m Manifest) ([]Loaded, []Warning, error) {
	loaded := make([]Loaded, 0, 1+len(m.Workers))
	var warnings []Warning

	add := func(dir string, role Role, manifestSched *Schedule) error {
		one, err := l.Build(dir, role)
		if err != nil {
			return err
		}
		// Manifest schedule is authoritative over the agent's profile.yml (RP-7
		// §3.7) — the whole team's cadence is declared in one versioned file.
		if manifestSched != nil {
			one.Schedule = manifestSched
		}
		for _, w := range one.Skills.Warnings {
			warnings = append(warnings, Warning{Agent: one.Def.Name, Msg: w})
		}
		loaded = append(loaded, one)
		return nil
	}

	if err := add(filepath.Join(workdir, "agents", "main", m.Leader.Agent), RoleLeader, m.Leader.Schedule); err != nil {
		return nil, nil, err
	}
	for _, wk := range m.Workers {
		if err := add(filepath.Join(workdir, "agents", "sub", wk.Agent), RoleWorker, wk.Schedule); err != nil {
			return nil, nil, err
		}
	}
	return loaded, warnings, nil
}

func asForRole(r Role) []string {
	if r == RoleLeader {
		return []string{"main"}
	}
	return []string{"subagent"}
}

// readToolList parses a flat YAML list of tool names (the shape of
// tools/active.yml and tools/deferr.yml). A missing file is not an error —
// an agent may legitimately have no active or no deferred tools.
func readToolList(path string) ([]tools.ToolName, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var list []tools.ToolName
	if err := yaml.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return list, nil
}

// readProfile parses profile.yml. A missing file yields the zero profile (no
// overrides), which is valid.
func readProfile(path string) (profileYml, error) {
	var p profileYml
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("read profile.yml: %w", err)
	}
	if err := yaml.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("parse profile.yml: %w", err)
	}
	return p, nil
}
