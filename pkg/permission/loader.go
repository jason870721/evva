package permission

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Warning is a non-fatal load error. The caller surfaces these on stderr
// like memdir / skill warnings; the agent still starts so a malformed
// permissions.json doesn't brick the session.
type Warning struct {
	Path string
	Err  error
}

func (w Warning) Error() string {
	if w.Path == "" {
		return w.Err.Error()
	}
	return fmt.Sprintf("%s: %v", w.Path, w.Err)
}

// fileShape is the JSON layout on disk. Three lists of rule strings, each
// associated with a Behavior. Matches the ref settings.json
// `permissions: { allow, deny, ask }` block — files written by Claude Code
// can be loaded by evva directly.
type fileShape struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
}

// Load reads permission rules from <workdir>/.evva/permissions.json and
// <evvaHome>/permissions.json. A missing file is not an error (returns
// empty rules + no warning). A malformed file produces a Warning and is
// otherwise skipped.
//
// The returned Store is populated and ready to use; callers can add
// session rules at runtime via Store.AddSessionRule.
func Load(workdir, evvaHome string) (*Store, []Warning) {
	return LoadMember(workdir, evvaHome, "")
}

// LoadMember is Load plus an optional member-scoped file: a per-member
// permissions.json whose rules load into ONLY the returned store (RP-11). This
// is how a swarm grants one non-leader a narrow lever — e.g. risk-monitor's
// store gets "http_request(POST .../halt)" while every other member's store
// (and POST .../strategy) still asks — without a shared file widening everyone.
// memberPath == "" makes it identical to Load. Member rules carry project-source
// semantics (a persistent configured grant, not a transient session approval).
func LoadMember(workdir, evvaHome, memberPath string) (*Store, []Warning) {
	store := NewStore()
	var warns []Warning
	var rules []Rule
	add := func(path string, src Source) {
		if path == "" {
			return
		}
		got, w := loadFile(path, src)
		rules = append(rules, got...)
		warns = append(warns, w...)
	}

	if workdir != "" {
		add(filepath.Join(workdir, ".evva", "permissions.json"), SourceProject)
	}
	if evvaHome != "" {
		add(filepath.Join(evvaHome, "permissions.json"), SourceUser)
	}
	add(memberPath, SourceProject)

	store.ReplaceAll(rules)
	return store, warns
}

func loadFile(path string, src Source) ([]Rule, []Warning) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []Warning{{Path: path, Err: err}}
	}

	var shape fileShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, []Warning{{Path: path, Err: fmt.Errorf("invalid json: %w", err)}}
	}

	var out []Rule
	var warns []Warning
	parse := func(entries []string, b Behavior) {
		for _, s := range entries {
			toolName, content, ok := ParseRule(s)
			if !ok {
				warns = append(warns, Warning{Path: path, Err: fmt.Errorf("invalid rule %q", s)})
				continue
			}
			out = append(out, Rule{
				ToolName: toolName,
				Content:  content,
				Behavior: b,
				Source:   src,
			})
		}
	}
	parse(shape.Permissions.Allow, BehaviorAllow)
	parse(shape.Permissions.Deny, BehaviorDeny)
	parse(shape.Permissions.Ask, BehaviorAsk)
	return out, warns
}

// Save writes the project-scope rules from store to
// <workdir>/.evva/permissions.json. User-scope rules are not written by
// Save — the user maintains <evvaHome>/permissions.json by hand. Session
// rules are intentionally not persisted.
//
// Creates the .evva directory if missing.
func Save(workdir string, store *Store) error {
	if workdir == "" {
		return errors.New("permission: workdir required for Save")
	}
	dir := filepath.Join(workdir, ".evva")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("permission: mkdir %s: %w", dir, err)
	}

	shape := fileShape{}
	for _, r := range store.projectRules() {
		entry := FormatRule(r.ToolName, r.Content)
		switch r.Behavior {
		case BehaviorAllow:
			shape.Permissions.Allow = append(shape.Permissions.Allow, entry)
		case BehaviorDeny:
			shape.Permissions.Deny = append(shape.Permissions.Deny, entry)
		case BehaviorAsk:
			shape.Permissions.Ask = append(shape.Permissions.Ask, entry)
		}
	}

	out, err := json.MarshalIndent(shape, "", "  ")
	if err != nil {
		return fmt.Errorf("permission: marshal: %w", err)
	}
	path := filepath.Join(dir, "permissions.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("permission: write %s: %w", path, err)
	}
	return nil
}
