package agentdef

import (
	"path/filepath"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

// TestWriteMemberDirRoundTrip: an operator spec serialised by WriteMemberDir is
// read back faithfully by the loader (RP-8) — system prompt, when_to_use, the
// active/deferred tool split, and the optional schedule all survive.
func TestWriteMemberDirRoundTrip(t *testing.T) {
	wd := t.TempDir()
	spec := MemberSpec{
		Name:         "qa-bot",
		SystemPrompt: "You are QA.",
		WhenToUse:    "QA and testing.",
		Active:       []tools.ToolName{"read", "bash"},
		Deferred:     []tools.ToolName{"grep"},
		Schedule:     &Schedule{Cron: "*/30 * * * *", Prompt: "patrol the suite"},
	}
	if err := WriteMemberDir(wd, spec); err != nil {
		t.Fatalf("WriteMemberDir: %v", err)
	}
	ld, err := (&Loader{}).Build(filepath.Join(wd, "agents", "sub", "qa-bot"), RoleWorker)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ld.Def.SystemPrompt != "You are QA." || ld.Def.WhenToUse != "QA and testing." {
		t.Errorf("prompt/whenToUse = %q / %q", ld.Def.SystemPrompt, ld.Def.WhenToUse)
	}
	if len(ld.Def.ActiveTools) != 2 || ld.Def.ActiveTools[0] != "read" || ld.Def.ActiveTools[1] != "bash" {
		t.Errorf("ActiveTools = %v", ld.Def.ActiveTools)
	}
	if len(ld.Def.DeferredTools) != 1 || ld.Def.DeferredTools[0] != "grep" {
		t.Errorf("DeferredTools = %v", ld.Def.DeferredTools)
	}
	if ld.Schedule == nil || ld.Schedule.Cron != "*/30 * * * *" || ld.Schedule.Prompt != "patrol the suite" {
		t.Errorf("Schedule = %+v", ld.Schedule)
	}
}

// TestWriteMemberDirRejects: unsafe names, an empty prompt, and clobbering an
// existing dir all fail cleanly; MemberDirExists/RemoveMemberDir track the dir.
func TestWriteMemberDirRejects(t *testing.T) {
	wd := t.TempDir()
	for _, bad := range []string{"", "../escape", "a/b", ".hidden"} {
		if err := WriteMemberDir(wd, MemberSpec{Name: bad, SystemPrompt: "x"}); err == nil {
			t.Errorf("WriteMemberDir(name=%q) = nil, want error", bad)
		}
	}
	if err := WriteMemberDir(wd, MemberSpec{Name: "noprompt"}); err == nil {
		t.Error("empty system prompt should error")
	}
	if err := WriteMemberDir(wd, MemberSpec{Name: "dup", SystemPrompt: "x"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !MemberDirExists(wd, "dup") {
		t.Error("MemberDirExists = false after write")
	}
	if err := WriteMemberDir(wd, MemberSpec{Name: "dup", SystemPrompt: "y"}); err == nil {
		t.Error("clobbering an existing member dir should error")
	}
	if err := RemoveMemberDir(wd, "dup"); err != nil {
		t.Fatalf("RemoveMemberDir: %v", err)
	}
	if MemberDirExists(wd, "dup") {
		t.Error("MemberDirExists = true after remove")
	}
}

// TestManifestWriteRoundTripAndWorkerEdits: WriteManifest round-trips through
// LoadManifest, and AddWorker/RemoveWorker keep the membership invariants (RP-8).
func TestManifestWriteRoundTripAndWorkerEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evva-swarm.yml")
	m := Manifest{
		Name:     "team",
		Workdir:  ".",
		Leader:   Member{Agent: "lead"},
		Settings: Settings{PermissionMode: "default", MaxIterations: 10},
	}
	if err := m.AddWorker("w1"); err != nil {
		t.Fatalf("AddWorker: %v", err)
	}
	if err := m.AddWorker("w1"); err == nil {
		t.Error("duplicate worker should error")
	}
	if err := m.AddWorker("lead"); err == nil {
		t.Error("adding the leader as a worker should error")
	}
	if err := WriteManifest(path, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got.Leader.Agent != "lead" || len(got.Workers) != 1 || got.Workers[0].Agent != "w1" {
		t.Errorf("round-trip = %+v", got)
	}
	if got.Settings.MaxIterations != 10 || got.Settings.PermissionMode != "default" {
		t.Errorf("settings lost: %+v", got.Settings)
	}
	got.RemoveWorker("w1")
	if len(got.Workers) != 0 {
		t.Errorf("RemoveWorker left %d workers", len(got.Workers))
	}
}
