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
	Effort   string                // profile effort pin (low|medium|high|ultra); applied at construction
	Role     Role
	// PermissionMode is the member's manifest permission-stance override
	// ("" = inherit the space setting; RP-24). Manifest-only by design —
	// trust tiering is a team-composition decision, so it lives in
	// evva-swarm.yml where the whole roster's stances read in one file,
	// not in each agent's own profile.yml.
	PermissionMode string
	// Worktree is the member's manifest worktree-isolation override
	// ("" = inherit settings.worktree_isolation; SWT). Manifest-only for the
	// same reason as PermissionMode: whether a member edits in its own
	// checkout is a team-composition decision, so the whole roster's stances
	// read in one file. Resolve with ResolveWorktree.
	Worktree string
	// Sandbox is the member's manifest sandbox override (SBX), the worktree
	// knob's twin. Resolve with ResolveSandbox.
	Sandbox string
	// FromPersona marks a member synthesized from a manifest persona entry
	// (RP-29): no disk dir was read; the space resolves the def from its
	// persona registry at assembly time.
	FromPersona bool
	// PersonaSource names the registry persona to resolve when it differs
	// from Def.Name — set only on ephemeral clones of persona members (DWF):
	// the clone registers under its own name but composes from the base
	// persona. Empty means "resolve Def.Name" (every non-clone).
	PersonaSource string
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
//
// sharedSkills, when non-empty, is the space-level shared skill dir (RP-26)
// merged UNDER the member's own skills/: both load into one registry, and on
// a name collision the member's version wins (local overrides global — the
// shadowing is surfaced as a registry warning). "" skips the merge, which is
// also exactly the pre-RP-26 behavior.
func (l *Loader) Build(dir string, role Role, sharedSkills string) (Loaded, error) {
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
	// per-skill problems land in skills.Warnings, surfaced by BuildAll. Source
	// order = (shared, member): the second dir overrides the first on a name
	// collision, which is the RP-26 precedence — member beats shared.
	skills, _ := skill.LoadRegistry(sharedSkills, filepath.Join(dir, "skills"))

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

	shared := SharedSkillsDir(workdir)
	add := func(dir string, role Role, mem Member) error {
		if mem.FromPersona {
			// A persona member has no disk dir — the space resolves its def
			// from the persona registry at assembly time (RP-29). Skills here
			// is a placeholder; constructMember composes the real layered
			// catalog (persona-own + shared + member-local).
			loaded = append(loaded, Loaded{
				Def:         agent.AgentDefinition{Name: mem.Agent, WhenToUse: mem.WhenToUse, Model: mem.Model},
				FromPersona: true, Role: role, Schedule: mem.Schedule,
				Effort: mem.Effort, PermissionMode: mem.PermissionMode,
				Worktree: mem.Worktree,
				Sandbox:  mem.Sandbox,
				Skills:   skill.NewRegistry(),
			})
			return nil
		}
		one, err := l.Build(dir, role, shared)
		if err != nil {
			return err
		}
		// Manifest schedule is authoritative over the agent's profile.yml (RP-7
		// §3.7) — the whole team's cadence is declared in one versioned file.
		if mem.Schedule != nil {
			one.Schedule = mem.Schedule
		}
		// Same precedence for the RP-29 manifest overrides.
		if mem.Model != "" {
			one.Def.Model = mem.Model
		}
		if mem.Effort != "" {
			one.Effort = mem.Effort
		}
		if mem.WhenToUse != "" {
			one.Def.WhenToUse = mem.WhenToUse
		}
		one.PermissionMode = mem.PermissionMode
		one.Worktree = mem.Worktree
		one.Sandbox = mem.Sandbox
		for _, w := range one.Skills.Warnings {
			warnings = append(warnings, Warning{Agent: one.Def.Name, Msg: w})
		}
		loaded = append(loaded, one)
		return nil
	}

	if err := add(filepath.Join(workdir, "agents", "main", m.Leader.Agent), RoleLeader, m.Leader); err != nil {
		return nil, nil, err
	}
	for _, wk := range m.Workers {
		if err := add(filepath.Join(workdir, "agents", "sub", wk.Agent), RoleWorker, wk); err != nil {
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
