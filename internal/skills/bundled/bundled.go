// Package bundled registers evva's first-party Markdown skills into a
// skill.Registry. Each SKILL.md file lives under content/<name>/SKILL.md and
// is embedded in the binary via go:embed (embed.go). Register reads the first
// line of each file for the description, wraps the body in a lazy BodyFunc,
// and calls Registry.AddBundled — so any user disk skill of the same name
// silently wins (see pkg/skill.SourceBundled).
//
// The package is private. Downstream SDK consumers that want to ship their
// own skill content build their own skill.Registry via skill.NewRegistry() +
// Add(...) and pass it through agent.WithSkillRegistry.
package bundled

import (
	"fmt"
	"strings"

	"github.com/johnny1110/evva/pkg/skill"
)

// Register overlays every embedded skill onto reg via Registry.AddBundled,
// which silently skips names already present — so disk-loaded entries (Home
// or WorkDir) override the bundled body without warning. A nil reg is a
// no-op (matching the agent wiring's nil-safety stance). Returns any
// non-fatal warnings (e.g. an embedded SKILL.md with a malformed title);
// callers surface them the way they surface skill.Registry.Warnings.
func Register(reg *skill.Registry) []string {
	if reg == nil {
		return nil
	}
	var warns []string
	for _, name := range bundledNames {
		meta, err := buildMeta(name)
		if err != nil {
			warns = append(warns, fmt.Sprintf("bundled skill %q: %v", name, err))
			continue
		}
		if err := reg.AddBundled(meta); err != nil {
			warns = append(warns, fmt.Sprintf("bundled skill %q: register: %v", name, err))
		}
	}
	return warns
}

// buildMeta parses the embedded SKILL.md's first non-blank line for the
// description and wraps the full file content (title line included — the
// SKILL tool's framing relies on it) in a lazy BodyFunc read only when the
// model dispatches the skill. Title parsing delegates to skill.ParseTitleLine
// so the disk loader and this path validate identical shapes; a title token
// that disagrees with the bundled name is a content bug and is rejected.
func buildMeta(name string) (skill.SkillMeta, error) {
	raw, err := readBundled(name)
	if err != nil {
		return skill.SkillMeta{}, err
	}
	first := firstNonBlankLine(raw)
	if first == "" {
		return skill.SkillMeta{}, fmt.Errorf("no title line")
	}
	titleName, desc, err := skill.ParseTitleLine(first)
	if err != nil {
		return skill.SkillMeta{}, err
	}
	if titleName != name {
		return skill.SkillMeta{}, fmt.Errorf("title names %q but bundled name is %q", titleName, name)
	}
	body := raw
	return skill.SkillMeta{
		Name:        name,
		Description: desc,
		BodyFunc:    func() (string, error) { return body, nil },
	}, nil
}

// firstNonBlankLine returns the first line of raw that is non-blank after
// trimming. The returned line itself is not trimmed (ParseTitleLine trims).
func firstNonBlankLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}
