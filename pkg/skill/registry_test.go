package skill

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestLoadRegistry_BothMissing_NoError(t *testing.T) {
	r, err := LoadRegistry("/no/such/home", "/no/such/workdir")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(r.List()) != 0 {
		t.Errorf("expected empty registry, got %d entries", len(r.List()))
	}
	if len(r.Warnings) != 0 {
		t.Errorf("missing dirs should not warn: %v", r.Warnings)
	}
}

func TestLoadRegistry_HomeOnly(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "git-commit", "# git-commit how to commit (rules) in a git branch\n\nbody here\n")

	r, err := LoadRegistry(home, "")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	got, ok := r.Get("git-commit")
	if !ok {
		t.Fatal("git-commit not found")
	}
	if got.Description != "how to commit (rules) in a git branch" {
		t.Errorf("description: got %q", got.Description)
	}
	if got.Source != SourceHome {
		t.Errorf("source: got %v want home", got.Source)
	}
}

func TestLoadRegistry_WorkDirOverridesHome(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeSkill(t, home, "git-commit", "# git-commit home-version\n\nHOME_BODY\n")
	writeSkill(t, wd, "git-commit", "# git-commit workdir-version\n\nWORKDIR_BODY\n")

	r, err := LoadRegistry(home, wd)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	got, ok := r.Get("git-commit")
	if !ok {
		t.Fatal("git-commit not found")
	}
	if got.Source != SourceWorkDir {
		t.Errorf("override failed: source=%v want workdir", got.Source)
	}
	if got.Description != "workdir-version" {
		t.Errorf("description: got %q", got.Description)
	}
	body, err := r.LoadBody("git-commit")
	if err != nil {
		t.Fatalf("LoadBody: %v", err)
	}
	if !strings.Contains(body, "WORKDIR_BODY") || strings.Contains(body, "HOME_BODY") {
		t.Errorf("LoadBody picked the wrong file: %q", body)
	}

	// override produces a warning so users notice shadowing
	foundOverrideWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "overrides") && strings.Contains(w, "git-commit") {
			foundOverrideWarn = true
			break
		}
	}
	if !foundOverrideWarn {
		t.Errorf("expected override warning; got %v", r.Warnings)
	}
}

func TestLoadRegistry_SortedListAndNames(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "zeta", "# zeta z-skill\n")
	writeSkill(t, home, "alpha", "# alpha a-skill\n")
	writeSkill(t, home, "mid", "# mid m-skill\n")

	r, _ := LoadRegistry(home, "")
	got := r.Names()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Names len: got %v want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("Names[%d]: got %q want %q", i, got[i], n)
		}
	}
}

func TestLoadRegistry_MalformedFirstLine_Skipped(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "ok-skill", "# ok-skill a fine skill\nbody\n")
	writeSkill(t, home, "bad-no-hash", "ok-skill without leading hash\nbody\n")
	writeSkill(t, home, "empty-title", "# \nbody\n")

	r, _ := LoadRegistry(home, "")
	if _, ok := r.Get("ok-skill"); !ok {
		t.Error("ok-skill should load")
	}
	if _, ok := r.Get("bad-no-hash"); ok {
		t.Error("bad-no-hash should be skipped")
	}
	if _, ok := r.Get("empty-title"); ok {
		t.Error("empty-title should be skipped")
	}
	if len(r.Warnings) < 2 {
		t.Errorf("expected >=2 warnings for malformed skills, got %v", r.Warnings)
	}
}

func TestLoadRegistry_FolderNameWinsOnMismatch(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "git-commit", "# different-name description text\nbody\n")

	r, _ := LoadRegistry(home, "")
	got, ok := r.Get("git-commit")
	if !ok {
		t.Fatal("skill not found under folder name")
	}
	if got.Description != "description text" {
		t.Errorf("description: got %q", got.Description)
	}
	if _, ok := r.Get("different-name"); ok {
		t.Error("title name should not register a duplicate entry")
	}
	mismatchWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "different-name") && strings.Contains(w, "git-commit") {
			mismatchWarn = true
			break
		}
	}
	if !mismatchWarn {
		t.Errorf("expected mismatch warning; got %v", r.Warnings)
	}
}

func TestLoadRegistry_BareNameNoDescription(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "solo", "# solo\nbody\n")

	r, _ := LoadRegistry(home, "")
	got, ok := r.Get("solo")
	if !ok {
		t.Fatal("solo not loaded")
	}
	if got.Description != "" {
		t.Errorf("expected empty description; got %q", got.Description)
	}
}

func TestLoadRegistry_LoadBodyUnknown(t *testing.T) {
	r, _ := LoadRegistry("", "")
	if _, err := r.LoadBody("nope"); err == nil {
		t.Error("expected error for unknown skill")
	}
}

func TestProgrammaticSkill_AddAndLoadBody(t *testing.T) {
	r := NewRegistry()
	err := r.Add(SkillMeta{
		Name:        "hello",
		Description: "say hi",
		BodyFunc:    func() (string, error) { return "hello body", nil },
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := r.Get("hello")
	if !ok {
		t.Fatal("hello not registered")
	}
	if got.Source != SourceProgrammatic {
		t.Errorf("source: got %v want programmatic", got.Source)
	}
	body, err := r.LoadBody("hello")
	if err != nil {
		t.Fatalf("LoadBody: %v", err)
	}
	if body != "hello body" {
		t.Errorf("body: got %q want %q", body, "hello body")
	}
}

func TestProgrammaticSkill_Validation(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(SkillMeta{Name: "", BodyFunc: func() (string, error) { return "", nil }}); err == nil {
		t.Error("expected error for empty name")
	}
	if err := r.Add(SkillMeta{Name: "no-body"}); err == nil {
		t.Error("expected error for missing BodyFunc")
	}
	bf := func() (string, error) { return "x", nil }
	if err := r.Add(SkillMeta{Name: "dup", BodyFunc: bf}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := r.Add(SkillMeta{Name: "dup", BodyFunc: bf}); err == nil {
		t.Error("expected duplicate-name error on second Add")
	}
}

func TestProgrammaticSkill_BodyFuncError(t *testing.T) {
	r := NewRegistry()
	sentinel := errors.New("boom")
	_ = r.Add(SkillMeta{Name: "bad", BodyFunc: func() (string, error) { return "", sentinel }})
	_, err := r.LoadBody("bad")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped BodyFunc error; got %v", err)
	}
}

func TestProgrammaticSkill_MixedWithDisk(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "disk-skill", "# disk-skill from disk\nbody\n")
	r, _ := LoadRegistry(home, "")
	if err := r.Add(SkillMeta{
		Name:     "prog-skill",
		BodyFunc: func() (string, error) { return "prog body", nil },
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	names := r.Names()
	want := []string{"disk-skill", "prog-skill"}
	if len(names) != len(want) {
		t.Fatalf("Names len: got %v want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("Names[%d]: got %q want %q", i, names[i], n)
		}
	}
}

func TestNewRegistry_EmptyByDefault(t *testing.T) {
	r := NewRegistry()
	if len(r.List()) != 0 {
		t.Errorf("expected empty registry; got %v", r.List())
	}
	if len(r.Names()) != 0 {
		t.Errorf("expected empty names; got %v", r.Names())
	}
}

func TestAddBundled_Insert(t *testing.T) {
	r := NewRegistry()
	err := r.AddBundled(SkillMeta{
		Name:        "commit",
		Description: "make a commit",
		BodyFunc:    func() (string, error) { return "# commit make a commit\nbody", nil },
	})
	if err != nil {
		t.Fatalf("AddBundled: %v", err)
	}
	got, ok := r.Get("commit")
	if !ok {
		t.Fatal("commit not registered")
	}
	if got.Source != SourceBundled {
		t.Errorf("source: got %v want bundled", got.Source)
	}
}

func TestAddBundled_ForcesSourceBundled(t *testing.T) {
	r := NewRegistry()
	// Caller lies about Source; AddBundled must override to bundled.
	_ = r.AddBundled(SkillMeta{
		Name:     "x",
		Source:   SourceProgrammatic,
		BodyFunc: func() (string, error) { return "b", nil },
	})
	if got, _ := r.Get("x"); got.Source != SourceBundled {
		t.Errorf("source: got %v want bundled", got.Source)
	}
}

func TestAddBundled_SkipsExisting(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "commit", "# commit disk version\nDISK_BODY\n")
	r, _ := LoadRegistry(home, "")
	warnsBefore := len(r.Warnings)

	err := r.AddBundled(SkillMeta{
		Name:     "commit",
		BodyFunc: func() (string, error) { return "BUNDLED_BODY", nil },
	})
	if err != nil {
		t.Fatalf("AddBundled should silently skip, got error: %v", err)
	}
	if got, _ := r.Get("commit"); got.Source != SourceHome {
		t.Errorf("disk skill should win: source=%v want home", got.Source)
	}
	if len(r.Warnings) != warnsBefore {
		t.Errorf("override of bundled must not warn; warnings grew: %v", r.Warnings)
	}
	body, _ := r.LoadBody("commit")
	if !strings.Contains(body, "DISK_BODY") {
		t.Errorf("LoadBody returned bundled body, expected disk: %q", body)
	}
}

func TestAddBundled_Validates(t *testing.T) {
	r := NewRegistry()
	if err := r.AddBundled(SkillMeta{Name: "", BodyFunc: func() (string, error) { return "", nil }}); err == nil {
		t.Error("expected error for empty name")
	}
	if err := r.AddBundled(SkillMeta{Name: "no-body"}); err == nil {
		t.Error("expected error for nil BodyFunc")
	}
	var nilReg *Registry
	if err := nilReg.AddBundled(SkillMeta{Name: "x", BodyFunc: func() (string, error) { return "", nil }}); err == nil {
		t.Error("expected error for nil registry")
	}
}

func TestAddBundled_OverriddenByLoadDir(t *testing.T) {
	r := NewRegistry()
	_ = r.AddBundled(SkillMeta{Name: "commit", BodyFunc: func() (string, error) { return "BUNDLED", nil }})

	wd := t.TempDir()
	writeSkill(t, wd, "commit", "# commit workdir version\nWORKDIR\n")
	r.loadDir(wd, SourceWorkDir)

	if got, _ := r.Get("commit"); got.Source != SourceWorkDir {
		t.Errorf("loadDir should replace bundled: source=%v want workdir", got.Source)
	}
	warned := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "overrides") && strings.Contains(w, "commit") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected cross-source override warning; got %v", r.Warnings)
	}
}

func TestParseTitleLine(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantDesc string
		wantErr  bool
	}{
		{"# name desc here", "name", "desc here", false},
		{"# name", "name", "", false},
		{"  # name desc  ", "name", "desc", false},
		{"# name   two  spaces", "name", "two  spaces", false},
		{"#name", "", "", true},
		{"name desc", "", "", true},
		{"", "", "", true},
		{"# ", "", "", true},
		{"#", "", "", true},
	}
	for _, c := range cases {
		name, desc, err := ParseTitleLine(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseTitleLine(%q): expected error, got name=%q desc=%q", c.in, name, desc)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTitleLine(%q): unexpected error %v", c.in, err)
			continue
		}
		if name != c.wantName || desc != c.wantDesc {
			t.Errorf("ParseTitleLine(%q): got (%q, %q) want (%q, %q)", c.in, name, desc, c.wantName, c.wantDesc)
		}
	}
}

// TestPromptPath_DoesNotCallBodyFunc pins acceptance criterion A5: prompt
// assembly (List/Names/Get) must never invoke BodyFunc — only LoadBody may.
// A panicking BodyFunc proves it.
func TestPromptPath_DoesNotCallBodyFunc(t *testing.T) {
	r := NewRegistry()
	_ = r.Add(SkillMeta{
		Name:        "panic-skill",
		Description: "must not load at prompt time",
		BodyFunc:    func() (string, error) { panic("BodyFunc called during prompt assembly") },
	})
	_ = r.List()
	_ = r.Names()
	if _, ok := r.Get("panic-skill"); !ok {
		t.Fatal("panic-skill missing")
	}
	defer func() {
		if recover() == nil {
			t.Error("expected LoadBody to invoke the panicking BodyFunc")
		}
	}()
	_, _ = r.LoadBody("panic-skill")
}
