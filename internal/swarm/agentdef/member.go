package agentdef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
	"gopkg.in/yaml.v3"
)

// MemberSpec is an operator-authored worker definition (RP-8): the inputs of the
// web "add agent" form. It is the domain shape WriteMemberDir serialises to disk
// so the existing loader (Build) can then hot-load it via AddMember. Collaboration
// tools are NOT listed here — they are injected by role at construction, so the
// form never shows them and the spec never carries them.
type MemberSpec struct {
	Name         string
	SystemPrompt string
	WhenToUse    string
	Model        string // optional model pin; empty = configured default. Fixed at creation.
	Effort       string // optional effort pin (low|medium|high|ultra); empty = default. Fixed at creation.
	Active       []tools.ToolName
	Deferred     []tools.ToolName
	Schedule     *Schedule // optional recurring timer (RP-7)
}

// profileWrite is the on-disk profile.yml shape WriteMemberDir emits. It is a
// write-only subset of profileYml (omitempty everywhere) so a freshly authored
// member's profile.yml carries only what the operator actually set.
type profileWrite struct {
	Model     string       `yaml:"model,omitempty"`
	Effort    string       `yaml:"effort,omitempty"`
	WhenToUse string       `yaml:"when_to_use,omitempty"`
	Schedule  *scheduleYml `yaml:"schedule,omitempty"`
}

// ValidateModelEffort checks a member's optional model / effort pins against
// the built-in catalogs. Empty values are valid (use the configured defaults).
// Used by WriteMemberDir to reject a bad add-agent form before touching disk —
// the form's choices come from the same catalogs. Hand-authored profile.yml
// pins are deliberately looser (constructMember accepts custom-registry model
// ids the constant table doesn't know).
func ValidateModelEffort(model, effort string) error {
	if m := strings.TrimSpace(model); m != "" {
		if _, ok := constant.GetModel(m); !ok {
			return fmt.Errorf("agentdef: unknown model %q", m)
		}
	}
	if e := strings.TrimSpace(effort); e != "" {
		if llm.ParseEffort(e) == 0 {
			return fmt.Errorf("agentdef: invalid effort %q (want low|medium|high|ultra)", e)
		}
	}
	return nil
}

// validMemberName rejects anything that could escape <workdir>/agents/sub/ or
// collide with the loader's path assumptions: path separators, "..", a leading
// dot (hidden dirs), or an empty name. The space still enforces per-space name
// uniqueness separately (invariant #2).
func validMemberName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("agentdef: member name is required")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("agentdef: illegal member name %q (no path separators, '..', or leading dot)", name)
	}
	return nil
}

// memberDir is the on-disk home of a worker definition.
func memberDir(workdir, name string) string {
	return filepath.Join(workdir, "agents", "sub", name)
}

// agentDir is the on-disk home of any member: the leader under agents/main/, a
// worker under agents/sub/ — matching the loader's BuildAll layout. memberDir is the
// worker-only RP-8 variant (create/remove only ever target workers); skills exist
// for the leader too, so the skill helpers are role-aware (RP-10).
func agentDir(workdir string, role Role, name string) string {
	tier := "sub"
	if role == RoleLeader {
		tier = "main"
	}
	return filepath.Join(workdir, "agents", tier, name)
}

// SkillsDir is a member's on-disk skills directory (<agentDir>/skills/). The
// supervisor reloads a member's catalog by re-scanning it via skill.LoadRegistry
// (RP-10-4); WriteSkill / RemoveSkill add and remove skill subfolders under it.
func SkillsDir(workdir string, role Role, name string) string {
	return filepath.Join(agentDir(workdir, role, name), "skills")
}

// MemberDirExists reports whether a worker's on-disk definition already exists.
// CreateMember uses it to tell "author a new member" (no dir) from "mount an
// existing dir by name" (the CLI add-member path).
func MemberDirExists(workdir, name string) bool {
	if validMemberName(name) != nil {
		return false
	}
	_, err := os.Stat(memberDir(workdir, name))
	return err == nil
}

// WriteMemberDir serialises a MemberSpec into <workdir>/agents/sub/<name>/ so the
// loader can Build it: system_prompt.md, profile.yml (when_to_use + optional
// schedule), and tools/active.yml + tools/deferr.yml. It refuses an unsafe name,
// an empty system prompt, or clobbering an existing member dir — the caller
// (Supervisor.CreateMember) hot-loads via AddMember after this returns.
func WriteMemberDir(workdir string, spec MemberSpec) error {
	if err := validMemberName(spec.Name); err != nil {
		return err
	}
	if strings.TrimSpace(spec.SystemPrompt) == "" {
		return errors.New("agentdef: system prompt is required")
	}
	if err := ValidateModelEffort(spec.Model, spec.Effort); err != nil {
		return err
	}
	dir := memberDir(workdir, spec.Name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("agentdef: member %q already exists on disk", spec.Name)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		return fmt.Errorf("agentdef: create member dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "system_prompt.md"), []byte(spec.SystemPrompt), 0o644); err != nil {
		return fmt.Errorf("agentdef: write system_prompt.md: %w", err)
	}

	pw := profileWrite{
		Model:     strings.TrimSpace(spec.Model),
		Effort:    strings.TrimSpace(spec.Effort),
		WhenToUse: spec.WhenToUse,
	}
	if spec.Schedule != nil {
		sy := &scheduleYml{Cron: spec.Schedule.Cron, Prompt: spec.Schedule.Prompt}
		if spec.Schedule.Every > 0 {
			sy.Every = spec.Schedule.Every.String()
		}
		pw.Schedule = sy
	}
	pb, err := yaml.Marshal(pw)
	if err != nil {
		return fmt.Errorf("agentdef: marshal profile.yml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.yml"), pb, 0o644); err != nil {
		return fmt.Errorf("agentdef: write profile.yml: %w", err)
	}

	if err := writeToolList(filepath.Join(dir, "tools", "active.yml"), spec.Active); err != nil {
		return err
	}
	if err := writeToolList(filepath.Join(dir, "tools", "deferr.yml"), spec.Deferred); err != nil {
		return err
	}
	return nil
}

// RemoveMemberDir deletes a worker's on-disk definition (the advanced "delete the
// directory too" path of web remove, RP-8). It is also the rollback for a
// CreateMember whose hot-load failed after the dir was written. Safe on a missing
// dir (RemoveAll); the name is validated to stay inside agents/sub/.
func RemoveMemberDir(workdir, name string) error {
	if err := validMemberName(name); err != nil {
		return err
	}
	return os.RemoveAll(memberDir(workdir, name))
}

// validSkillName guards a skill folder name the same way validMemberName guards a
// member: no path separators, "..", leading dot, or emptiness — it becomes a
// directory under <member>/skills/.
func validSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("agentdef: skill name is required")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("agentdef: illegal skill name %q (no path separators, '..', or leading dot)", name)
	}
	return nil
}

// WriteSkill authors a skill at <member>/skills/<name>/SKILL.md in the
// `# <name> <description>` + body shape the loader parses (skill.ParseTitleLine — the
// first token must equal the folder name, which it does). It refuses an unsafe name,
// an empty body, or clobbering an existing skill; the caller reloads the member after
// (RP-10-4) so the new skill enters the prompt + skill tool. Skills are User-authored
// only — no agent-facing tool writes here (RP-10 discipline).
func WriteSkill(workdir string, role Role, member, name, description, body string) error {
	if err := validSkillName(name); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("agentdef: skill body is required")
	}
	dir := filepath.Join(SkillsDir(workdir, role, member), name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("agentdef: skill %q already exists", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("agentdef: create skill dir: %w", err)
	}
	title := "# " + name
	if d := strings.TrimSpace(description); d != "" {
		title += " " + d
	}
	content := title + "\n\n" + strings.TrimRight(body, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("agentdef: write SKILL.md: %w", err)
	}
	return nil
}

// RemoveSkill deletes a skill folder under a member's skills/ dir (the web remove
// path). Safe on a missing dir (RemoveAll); the name is validated to stay inside
// skills/. The caller reloads the member afterwards.
func RemoveSkill(workdir string, role Role, member, name string) error {
	if err := validSkillName(name); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(SkillsDir(workdir, role, member), name))
}

// writeToolList serialises a flat tool-name list (the shape readToolList parses).
func writeToolList(path string, names []tools.ToolName) error {
	b, err := yaml.Marshal(names)
	if err != nil {
		return fmt.Errorf("agentdef: marshal %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("agentdef: write %s: %w", filepath.Base(path), err)
	}
	return nil
}
