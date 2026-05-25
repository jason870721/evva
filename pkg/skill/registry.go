// Package skill implements user-installed Markdown skills and the SKILL tool
// that invokes them. The package is public so downstream apps embedding the
// evva agent runtime can build their own skill catalogs — either by dropping
// SKILL.md files under a directory (the disk path) or by registering skills
// programmatically through Registry.Add (the SDK path).
//
// Skills live in two directories. Both layouts are identical:
//
//	<root>/skills/
//	  <skill-name>/
//	    SKILL.md
//
// LoadRegistry reads AppHome first then WorkDir, so a workdir-local skill
// transparently overrides a same-named home skill. The first line of every
// SKILL.md is parsed as `# <skill-name> <description>`; the body is whatever
// follows. The SKILL tool wraps the body as "Follow these instructions" when
// the model invokes a skill, so the file content is treated as opaque
// Markdown — the package does not impose structure beyond the title line.
//
// Programmatic skills (added via Registry.Add) skip the filesystem entirely:
// they carry a BodyFunc the registry calls when the model dispatches the
// skill. This is the path SDK consumers use to bundle skills inside their
// binary via go:embed, fetch them over the network, or generate them on the
// fly.
package skill

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillSource identifies where a skill was loaded from. Precedence on a
// name clash, highest to lowest: Programmatic (an explicit SDK choice the
// host made at startup) > WorkDir > Home > Bundled (evva's own first-party
// content, embedded in the binary). Bundled is intentionally lowest so a
// user's disk skill or a host's programmatic skill silently overrides it —
// overriding a bundled body is the documented extension point, not a
// surprise, so that override is NOT recorded as a Warning. The field is
// exposed mostly for logging and a future `/skills` slash command that
// wants to surface origin.
type SkillSource string

const (
	SourceHome         SkillSource = "home"
	SourceWorkDir      SkillSource = "workdir"
	SourceProgrammatic SkillSource = "programmatic"
	// SourceBundled is the lowest-precedence tier: a same-named disk skill
	// (Home or WorkDir) or a Programmatic skill wins silently. Registered
	// via Registry.AddBundled (see internal/skills/bundled).
	SourceBundled SkillSource = "bundled"
)

// SkillMeta is the resolved metadata for a single skill. Body content is
// loaded on demand via Registry.LoadBody so the prompt path stays cheap.
//
// For disk-loaded skills, Path points to SKILL.md and BodyFunc is nil — the
// registry reads the file at dispatch time. For programmatic skills, Path
// is empty and BodyFunc returns the body string when invoked; the host can
// back it with any source (embed.FS, network, generator, ...).
type SkillMeta struct {
	Name        string
	Description string
	Path        string // absolute path to SKILL.md; empty for programmatic skills
	Source      SkillSource
	// BodyFunc, when non-nil, is the lazy body loader the registry calls
	// instead of reading Path. Programmatic skills MUST set this; disk
	// skills MUST leave it nil.
	BodyFunc func() (string, error)
}

// Registry is the merged catalog of installed skills. Construct via
// LoadRegistry (disk) or NewRegistry (programmatic-only); methods are safe
// to call from any goroutine because the map is set once at construction
// and only mutated through Add — which runs at host bootstrap time before
// the agent starts dispatching tools.
type Registry struct {
	skills map[string]SkillMeta
	// Warnings collects non-fatal load issues (malformed first lines, name
	// mismatches, unreadable files). Callers may surface these at startup;
	// the loader never blocks boot on them.
	Warnings []string
}

// NewRegistry returns an empty registry for programmatic-only use. Hosts
// that want the on-disk override behavior should call LoadRegistry; hosts
// that bundle every skill in code can construct here and call Add.
//
// Mixed use is fine: LoadRegistry first, then Add for any programmatic
// extras the host wants alongside the disk catalog.
func NewRegistry() *Registry {
	return &Registry{skills: map[string]SkillMeta{}}
}

// Add inserts a programmatic skill into the registry. The skill's Source
// field is force-set to SourceProgrammatic so the origin is always honest
// regardless of how the caller filled the struct.
//
// Validation:
//   - Name must be non-empty.
//   - BodyFunc must be non-nil (the SKILL tool would have nothing to return).
//   - A name already present in the registry is rejected. To override a
//     disk skill the caller should use a different name or delete the
//     disk entry before adding.
func (r *Registry) Add(m SkillMeta) error {
	if r == nil {
		return fmt.Errorf("skill: nil registry")
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("skill: name is required")
	}
	if m.BodyFunc == nil {
		return fmt.Errorf("skill: %q has no BodyFunc (programmatic skills must supply one)", m.Name)
	}
	if _, dup := r.skills[m.Name]; dup {
		return fmt.Errorf("skill: %q already registered", m.Name)
	}
	if r.skills == nil {
		r.skills = map[string]SkillMeta{}
	}
	m.Source = SourceProgrammatic
	r.skills[m.Name] = m
	return nil
}

// AddBundled inserts a skill at the SourceBundled tier — evva's own
// first-party content. It differs from Add in two ways:
//
//  1. If a skill with the same Name already exists (typically loaded from
//     disk by LoadRegistry, or added programmatically), AddBundled silently
//     skips the insert and returns nil. The existing entry wins WITHOUT a
//     Warning — overriding a bundled body is the documented extension point,
//     not surprise shadowing.
//  2. Source is force-set to SourceBundled regardless of the caller's value,
//     mirroring how Add force-sets SourceProgrammatic.
//
// Validation matches Add: Name non-empty, BodyFunc non-nil, non-nil
// receiver. Callers live in internal/skills/bundled; external SDK consumers
// should use Add for content they ship.
func (r *Registry) AddBundled(m SkillMeta) error {
	if r == nil {
		return fmt.Errorf("skill: nil registry")
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("skill: name is required")
	}
	if m.BodyFunc == nil {
		return fmt.Errorf("skill: bundled %q has no BodyFunc", m.Name)
	}
	if r.skills == nil {
		r.skills = map[string]SkillMeta{}
	}
	if _, exists := r.skills[m.Name]; exists {
		return nil // user/programmatic override wins; silent skip.
	}
	m.Source = SourceBundled
	r.skills[m.Name] = m
	return nil
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
	titleName, desc, err := ParseTitleLine(first)
	if err != nil {
		r.warnf("skill: %q: %v", path, err)
		return "", false
	}
	if titleName != folder {
		r.warnf("skill: %q title names %q but folder is %q; using folder name", path, titleName, folder)
	}
	return desc, true
}

// ParseTitleLine parses a SKILL.md's first non-blank line — the canonical
// `# <name> [<description>]` shape — into its name token and optional
// description. It is the single source of truth both the disk loader
// (parseFirstLine) and the bundled loader (internal/skills/bundled) call, so
// a title that loads as a disk skill also loads as a bundled skill and vice
// versa. Errors carry no package prefix; callers wrap them with context.
func ParseTitleLine(line string) (name, description string, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", "", fmt.Errorf("empty title line")
	}
	if !strings.HasPrefix(trimmed, "# ") {
		return "", "", fmt.Errorf("title line must start with `# `: got %q", trimmed)
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
	if rest == "" {
		return "", "", fmt.Errorf("empty title")
	}
	// Accept both `<name> <desc>` (documented) and a bare `<name>`.
	parts := strings.SplitN(rest, " ", 2)
	name = parts[0]
	if len(parts) == 2 {
		description = strings.TrimSpace(parts[1])
	}
	return name, description, nil
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

// LoadBody returns the full body content for the named skill. Disk-loaded
// skills are read from SkillMeta.Path; programmatic skills invoke
// SkillMeta.BodyFunc. The SKILL tool wraps the output before handing it
// back to the model.
func (r *Registry) LoadBody(name string) (string, error) {
	m, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	if m.BodyFunc != nil {
		body, err := m.BodyFunc()
		if err != nil {
			return "", fmt.Errorf("skill %q body: %w", name, err)
		}
		return body, nil
	}
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return "", fmt.Errorf("skill %q read %s: %w", name, m.Path, err)
	}
	return string(b), nil
}
