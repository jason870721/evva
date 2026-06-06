package agentdef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Active       []tools.ToolName
	Deferred     []tools.ToolName
	Schedule     *Schedule // optional recurring timer (RP-7)
}

// profileWrite is the on-disk profile.yml shape WriteMemberDir emits. It is a
// write-only subset of profileYml (omitempty everywhere) so a freshly authored
// member's profile.yml carries only what the operator actually set.
type profileWrite struct {
	WhenToUse string       `yaml:"when_to_use,omitempty"`
	Schedule  *scheduleYml `yaml:"schedule,omitempty"`
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

	pw := profileWrite{WhenToUse: spec.WhenToUse}
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
