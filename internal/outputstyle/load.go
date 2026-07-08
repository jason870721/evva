package outputstyle

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnny1110/evva/internal/memdir"
)

// DirName is the flat directory both tiers load styles from:
// <appHome>/output-styles/*.md (user) and <workdir>/.evva/output-styles/*.md
// (project). Same two-root layout as skills, but one flat dir — a style is a
// single file, not a bundle.
const DirName = "output-styles"

// LoadAll returns the merged style catalog: built-ins, overlaid by the user
// tier, overlaid by the project tier (project wins on a name clash, mirroring
// pkg/skill's AppHome-then-WorkDir precedence and ref's built-in < user <
// project order). Non-fatal problems — an unreadable dir entry, a style file
// with an empty body — are skipped and reported as warnings; a broken style
// file must never break a session.
func LoadAll(appHome, workdir string) (map[string]Style, []string) {
	all := BuiltIns()
	var warnings []string
	if appHome != "" {
		warnings = append(warnings, loadDir(all, filepath.Join(appHome, DirName), SourceUser)...)
	}
	if workdir != "" {
		warnings = append(warnings, loadDir(all, filepath.Join(workdir, ".evva", DirName), SourceProject)...)
	}
	return all, warnings
}

// loadDir folds every *.md under dir into all, overwriting same-named
// entries (later tier wins). A missing dir is the common case and returns
// no warnings.
func loadDir(all map[string]Style, dir, source string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{"outputstyle: read " + dir + ": " + err.Error()}
	}
	var warnings []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		st, warn := loadFile(path, source)
		if warn != "" {
			warnings = append(warnings, warn)
			continue
		}
		all[st.Name] = st
	}
	return warnings
}

// loadFile parses one style file. The frontmatter supplies name /
// description / keep-coding-instructions; the body is the style prompt.
// Mirrors ref/src/outputStyles/loadOutputStylesDir.ts: the name falls back
// to the filename, the description to a generic blurb, and the keep flag is
// true ONLY when the frontmatter says true/"true" (ref's `=== true` check —
// an absent key means the style replaces the coding doctrine).
func loadFile(path, source string) (Style, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Style{}, "outputstyle: read " + path + ": " + err.Error()
	}
	fm, body := memdir.ParseFrontmatter(string(raw))
	prompt := strings.TrimSpace(body)
	if prompt == "" {
		return Style{}, "outputstyle: " + path + " has no prompt body; skipped"
	}
	name := strings.TrimSpace(fm["name"])
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	desc := strings.TrimSpace(fm["description"])
	if desc == "" {
		desc = "Custom " + name + " output style"
	}
	return Style{
		Name:                   name,
		Description:            desc,
		Prompt:                 prompt,
		KeepCodingInstructions: strings.EqualFold(strings.TrimSpace(fm["keep-coding-instructions"]), "true"),
		Source:                 source,
	}, ""
}

// Sorted flattens the catalog for pickers: default first, then the
// remaining built-ins, then disk styles, alphabetical within each group.
func Sorted(all map[string]Style) []Style {
	out := make([]Style, 0, len(all))
	for _, st := range all {
		out = append(out, st)
	}
	rank := func(s Style) int {
		switch {
		case s.Name == DefaultName:
			return 0
		case s.Source == SourceBuiltIn:
			return 1
		default:
			return 2
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}
