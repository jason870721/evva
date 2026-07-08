package outputstyle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStyle(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltIns(t *testing.T) {
	b := BuiltIns()
	if len(b) != 3 {
		t.Fatalf("want 3 built-ins, got %d", len(b))
	}
	def := b[DefaultName]
	if def.Prompt != "" {
		t.Errorf("default must carry no overlay prompt, got %q", def.Prompt)
	}
	for _, name := range []string{"Explanatory", "Learning"} {
		st, ok := b[name]
		if !ok {
			t.Fatalf("missing built-in %s", name)
		}
		if !st.KeepCodingInstructions {
			t.Errorf("%s must keep coding instructions", name)
		}
		if st.Prompt == "" || st.Source != SourceBuiltIn {
			t.Errorf("%s malformed: prompt empty=%v source=%q", name, st.Prompt == "", st.Source)
		}
	}
	if !strings.Contains(b["Explanatory"].Prompt, "★ Insight") {
		t.Error("Explanatory prompt lost the insight block")
	}
	if !strings.Contains(b["Learning"].Prompt, "● **Learn by Doing**") {
		t.Error("Learning prompt lost the Learn-by-Doing block")
	}
}

func TestLoadAll_MissingDirsIsClean(t *testing.T) {
	all, warns := LoadAll(t.TempDir(), t.TempDir())
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(all) != 3 {
		t.Fatalf("want just the built-ins, got %d entries", len(all))
	}
}

func TestLoadAll_PrecedenceProjectOverUserOverBuiltin(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeStyle(t, filepath.Join(home, DirName), "pirate.md",
		"---\nname: pirate\ndescription: user tier\n---\nTalk like a home pirate.")
	writeStyle(t, filepath.Join(work, ".evva", DirName), "pirate.md",
		"---\nname: pirate\ndescription: project tier\n---\nTalk like a project pirate.")
	// A disk style may shadow a built-in, including default (ref behavior).
	writeStyle(t, filepath.Join(work, ".evva", DirName), "Explanatory.md",
		"---\nname: Explanatory\n---\nShadowed explainer.")

	all, warns := LoadAll(home, work)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	p := all["pirate"]
	if p.Source != SourceProject || p.Description != "project tier" {
		t.Errorf("project tier should win: got source=%q desc=%q", p.Source, p.Description)
	}
	if !strings.Contains(p.Prompt, "project pirate") {
		t.Errorf("wrong body won: %q", p.Prompt)
	}
	if all["Explanatory"].Prompt != "Shadowed explainer." {
		t.Errorf("disk style should shadow built-in, got %q", all["Explanatory"].Prompt)
	}
}

func TestLoadFile_FallbacksAndKeepFlag(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, DirName)
	// No frontmatter at all: name from filename, generic description,
	// keep-coding-instructions absent → replace mode (ref's === true check).
	writeStyle(t, dir, "bare-voice.md", "Just a body.")
	writeStyle(t, dir, "keeper.md",
		"---\nname: keeper\nkeep-coding-instructions: true\n---\nBody.")
	writeStyle(t, dir, "replacer.md",
		"---\nname: replacer\nkeep-coding-instructions: false\n---\nBody.")

	all, warns := LoadAll(home, "")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	bare := all["bare-voice"]
	if bare.Name != "bare-voice" || bare.Description != "Custom bare-voice output style" {
		t.Errorf("filename/description fallback broken: %+v", bare)
	}
	if bare.KeepCodingInstructions {
		t.Error("absent keep-coding-instructions must mean replace mode")
	}
	if !all["keeper"].KeepCodingInstructions {
		t.Error("keep-coding-instructions: true not honored")
	}
	if all["replacer"].KeepCodingInstructions {
		t.Error("keep-coding-instructions: false not honored")
	}
}

func TestLoadAll_EmptyBodySkippedWithWarning(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, filepath.Join(home, DirName), "empty.md", "---\nname: empty\n---\n   \n")

	all, warns := LoadAll(home, "")
	if _, ok := all["empty"]; ok {
		t.Error("empty-body style must be skipped")
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "no prompt body") {
		t.Errorf("want one no-body warning, got %v", warns)
	}
}

func TestResolve(t *testing.T) {
	all := BuiltIns()
	if st, warn := Resolve(all, ""); st.Name != DefaultName || warn != "" {
		t.Errorf("empty name must resolve to default cleanly, got %q / %q", st.Name, warn)
	}
	if st, warn := Resolve(all, "Explanatory"); st.Name != "Explanatory" || warn != "" {
		t.Errorf("known style broken: %q / %q", st.Name, warn)
	}
	st, warn := Resolve(all, "ghost")
	if st.Name != DefaultName {
		t.Errorf("unknown style must fall back to default, got %q", st.Name)
	}
	if !strings.Contains(warn, "ghost") {
		t.Errorf("fallback warning should name the missing style, got %q", warn)
	}
}

func TestSorted(t *testing.T) {
	all := BuiltIns()
	all["zeta"] = Style{Name: "zeta", Source: SourceProject, Prompt: "z"}
	all["alpha"] = Style{Name: "alpha", Source: SourceUser, Prompt: "a"}

	got := Sorted(all)
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	want := []string{DefaultName, "Explanatory", "Learning", "alpha", "zeta"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names, want)
	}
}
