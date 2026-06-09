package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_EmptyDirsAreSilent(t *testing.T) {
	store, warns := Load("", "")
	if store == nil {
		t.Fatal("Load returned nil store")
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings; got %v", warns)
	}
}

func TestLoad_MissingFilesAreSilent(t *testing.T) {
	wd := t.TempDir()
	home := t.TempDir()
	_, warns := Load(wd, home)
	if len(warns) != 0 {
		t.Errorf("missing files should not warn; got %v", warns)
	}
}

func TestLoad_ProjectAndUserSourcesMerge(t *testing.T) {
	wd := t.TempDir()
	home := t.TempDir()

	writeFile(t, filepath.Join(wd, ".evva", "permissions.json"), `{
		"permissions": {
			"allow": ["bash(git:*)"],
			"deny":  ["bash(rm:*)"]
		}
	}`)
	writeFile(t, filepath.Join(home, "permissions.json"), `{
		"permissions": {
			"allow": ["read"]
		}
	}`)

	store, warns := Load(wd, home)
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}

	snap := store.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 rules; got %d (%+v)", len(snap), snap)
	}
	// Each rule should be tagged with the correct source.
	var seenProject, seenUser int
	for _, r := range snap {
		switch r.Source {
		case SourceProject:
			seenProject++
		case SourceUser:
			seenUser++
		}
	}
	if seenProject != 2 || seenUser != 1 {
		t.Errorf("sources: project=%d user=%d (want 2+1)", seenProject, seenUser)
	}
}

func TestLoad_MalformedJSONWarns(t *testing.T) {
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, ".evva", "permissions.json"), `{not json`)
	_, warns := Load(wd, "")
	if len(warns) == 0 {
		t.Fatal("expected a warning for malformed JSON")
	}
}

func TestLoad_InvalidRuleStringWarnsButContinues(t *testing.T) {
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, ".evva", "permissions.json"), `{
		"permissions": {
			"allow": ["", "bash(ok)"]
		}
	}`)
	store, warns := Load(wd, "")
	if len(warns) == 0 {
		t.Error("expected a warning for the empty rule string")
	}
	if n := len(store.Snapshot()); n != 1 {
		t.Errorf("the valid rule should still be loaded; got %d", n)
	}
}

func TestSaveRoundtrip(t *testing.T) {
	wd := t.TempDir()
	store := NewStore()
	store.ReplaceAll([]Rule{
		{ToolName: "bash", Content: "git:*", Behavior: BehaviorAllow, Source: SourceProject},
		{ToolName: "bash", Content: "rm:*", Behavior: BehaviorDeny, Source: SourceProject},
	})
	if err := Save(wd, store); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store2, warns := Load(wd, "")
	if len(warns) != 0 {
		t.Errorf("warns on reload: %v", warns)
	}
	if n := len(store2.Snapshot()); n != 2 {
		t.Errorf("expected 2 rules after reload; got %d", n)
	}
}

// TestLoadMember (RP-11) proves the per-member scoped lever: a member-scoped
// permissions.json grants a narrow http_request lever into ONLY the returned
// store — a plain Load (any other member) does not get it, and POST to a
// different url still asks. This is the isolation the swarm relies on.
func TestLoadMember(t *testing.T) {
	dir := t.TempDir()
	memberPath := filepath.Join(dir, "permissions.json")
	writeFile(t, memberPath, `{"permissions":{"allow":["http_request(POST http://127.0.0.1:7777/halt)"]}}`)

	halt := ToolCall{Name: "http_request", Input: []byte(`{"method":"POST","url":"http://127.0.0.1:7777/halt"}`)}
	strat := ToolCall{Name: "http_request", Input: []byte(`{"method":"POST","url":"http://127.0.0.1:7777/strategy"}`)}

	store, warns := LoadMember("", "", memberPath)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if d := Decide(halt, ModeDefault, store, Hint{}, "", ""); d.Behavior != BehaviorAllow {
		t.Errorf("scoped halt lever: got %v, want allow", d.Behavior)
	}
	if d := Decide(strat, ModeDefault, store, Hint{}, "", ""); d.Behavior != BehaviorAsk {
		t.Errorf("unscoped strategy lever: got %v, want ask", d.Behavior)
	}

	// The grant is isolated: a store without the member file (any other member)
	// does NOT inherit it.
	shared, _ := Load("", "")
	if d := Decide(halt, ModeDefault, shared, Hint{}, "", ""); d.Behavior != BehaviorAsk {
		t.Errorf("non-member store should not hold the grant: got %v, want ask", d.Behavior)
	}

	// A missing member file is silent (identical to Load).
	if _, w := LoadMember("", "", filepath.Join(dir, "nope.json")); len(w) != 0 {
		t.Errorf("missing member file should be silent, got %v", w)
	}
}
