// Package skill implements user-installed Markdown skills and the SKILL tool
// that invokes them.
//
// Skills live in two directories. Both layouts are identical:
//
//	<root>/skills/
//	  <skill-name>/
//	    SKILL.md
//
// LoadRegistry reads EvvaHome first then WorkDir, so a workdir-local skill
// transparently overrides a same-named home skill. The first line of every
// SKILL.md is parsed as `# <skill-name> <description>`; the body is whatever
// follows. The SKILL tool wraps the body as "Follow these instructions" when
// the model invokes a skill, so the file content is treated as opaque
// Markdown — the package does not impose structure beyond the title line.
package skill

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillSource identifies which directory a skill was loaded from. WorkDir
// always wins on a name clash; this field is exposed mostly for logging
// and a future `/skills` slash command that wants to surface the origin.
type SkillSource string

const (
	SourceHome    SkillSource = "home"
	SourceWorkDir SkillSource = "workdir"
)

// SkillMeta is the resolved metadata for a single skill. Body content is
// loaded on demand via Registry.LoadBody so the prompt path stays cheap.
type SkillMeta struct {
	Name        string
	Description string
	Path        string // absolute path to SKILL.md
	Source      SkillSource
}

// Registry is the merged catalog of installed skills. Construct via
// LoadRegistry; methods are safe to call from any goroutine because the map
// is set once at construction and only read afterwards.
type Registry struct {
	skills map[string]SkillMeta
	// Warnings collects non-fatal load issues (malformed first lines, name
	// mismatches, unreadable files). Callers may surface these at startup;
	// the loader never blocks boot on them.
	Warnings []string
}

// LoadRegistry scans homeSkillsDir then workdirSkillsDir and returns the
// merged registry. Missing directories are treated as empty — having no
// skills installed is the normal state, not an error.
//
// The order matters: workdir skills overwrite home skills with the same
// folder name. The override is recorded as a warning so the user can spot
// surprise shadowing.
func LoadRegistry(homeSkillsDir, workdirSkillsDir string) (*Registry, error) {
	r := &Registry{skills: map[string]SkillMeta{}}
	r.loadDir(homeSkillsDir, SourceHome)
	r.loadDir(workdirSkillsDir, SourceWorkDir)
	return r, nil
}

// loadDir walks one root, parses each child folder's SKILL.md, and inserts
// or overrides entries in r. Per-skill errors are recorded as warnings; only
// a complete read failure of the root is silently ignored (a missing dir is
// the expected state when the user hasn't installed anything yet).
func (r *Registry) loadDir(root string, src SkillSource) {
	if strings.TrimSpace(root) == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			r.warnf("skill: read dir %q: %v", root, err)
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := e.Name()
		path := filepath.Join(root, folder, "SKILL.md")
		desc, ok := r.parseFirstLine(path, folder)
		if !ok {
			continue
		}
		if existing, dup := r.skills[folder]; dup && existing.Source != src {
			r.warnf("skill: %q from %s overrides %s", folder, src, existing.Source)
		}
		r.skills[folder] = SkillMeta{
			Name:        folder,
			Description: desc,
			Path:        path,
			Source:      src,
		}
	}
}

// parseFirstLine opens path, finds the first non-blank line, and validates
// the `# <skill-name> <description>` shape. Returns the description on
// success. Any failure is recorded as a warning and ok=false so the caller
// skips the skill — we never poison the registry with half-broken entries.
//
// The folder name is the canonical identifier. When the title line names a
// different skill we warn and keep the folder name, since that's what the
// LLM will pass to the SKILL tool.
func (r *Registry) parseFirstLine(path, folder string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		r.warnf("skill: open %q: %v", path, err)
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4*1024), 1*1024*1024)

	var first string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			first = line
			break
		}
	}
	if err := scanner.Err(); err != nil {
		r.warnf("skill: read %q: %v", path, err)
		return "", false
	}
	if first == "" {
		r.warnf("skill: %q has no title line", path)
		return "", false
	}
	if !strings.HasPrefix(first, "# ") {
		r.warnf("skill: %q first line must start with `# `: got %q", path, first)
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(first, "# "))
	if rest == "" {
		r.warnf("skill: %q has empty title", path)
		return "", false
	}

	// Split into name token + description. We accept both `<name> <desc>`
	// (the documented form) and a bare `<name>` (description empty).
	parts := strings.SplitN(rest, " ", 2)
	titleName := parts[0]
	desc := ""
	if len(parts) == 2 {
		desc = strings.TrimSpace(parts[1])
	}
	if titleName != folder {
		r.warnf("skill: %q title names %q but folder is %q; using folder name", path, titleName, folder)
	}
	return desc, true
}

func (r *Registry) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// Get returns the meta for a skill by name. ok=false when unknown.
func (r *Registry) Get(name string) (SkillMeta, bool) {
	if r == nil {
		return SkillMeta{}, false
	}
	m, ok := r.skills[name]
	return m, ok
}

// List returns every known skill sorted by name. Used by the sysprompt
// builder so the # Skills section is deterministic across runs.
func (r *Registry) List() []SkillMeta {
	if r == nil {
		return nil
	}
	out := make([]SkillMeta, 0, len(r.skills))
	for _, m := range r.skills {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns just the skill names, sorted. Convenience for callers that
// only need the catalog identifiers (e.g. the unknown-skill error message).
func (r *Registry) Names() []string {
	list := r.List()
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.Name
	}
	return out
}

// LoadBody reads the full SKILL.md content for the named skill verbatim.
// The SKILL tool wraps this output before returning it to the model.
func (r *Registry) LoadBody(name string) (string, error) {
	m, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return "", fmt.Errorf("skill %q read %s: %w", name, m.Path, err)
	}
	return string(b), nil
}
