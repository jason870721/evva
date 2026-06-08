package agentdef

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
)

// TestWriteRemoveSkillRoundTrip (RP-10-5): WriteSkill authors a SKILL.md the skill
// loader reads back (name + description + body) under the role-correct dir, and
// RemoveSkill deletes it. Illegal names, duplicates, and empty bodies are rejected.
func TestWriteRemoveSkillRoundTrip(t *testing.T) {
	wd := t.TempDir()

	// Worker skills live under agents/sub/<name>/skills/; the leader under agents/main/.
	if got := SkillsDir(wd, RoleWorker, "qa"); got != filepath.Join(wd, "agents", "sub", "qa", "skills") {
		t.Errorf("worker SkillsDir = %q", got)
	}
	if got := SkillsDir(wd, RoleLeader, "lead"); got != filepath.Join(wd, "agents", "main", "lead", "skills") {
		t.Errorf("leader SkillsDir = %q", got)
	}

	if err := WriteSkill(wd, RoleWorker, "qa", "pnl-report", "summarise the PnL", "Step 1...\nStep 2..."); err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}
	reg, _ := skill.LoadRegistry(SkillsDir(wd, RoleWorker, "qa"), "")
	m, ok := reg.Get("pnl-report")
	if !ok {
		t.Fatalf("skill not loaded back; have %v", reg.Names())
	}
	if m.Description != "summarise the PnL" {
		t.Errorf("description = %q, want %q", m.Description, "summarise the PnL")
	}
	if body, _ := reg.LoadBody("pnl-report"); !strings.Contains(body, "Step 1") {
		t.Errorf("body = %q", body)
	}

	if err := WriteSkill(wd, RoleWorker, "qa", "pnl-report", "x", "y"); err == nil {
		t.Error("writing a duplicate skill should fail")
	}
	if err := WriteSkill(wd, RoleWorker, "qa", "../escape", "x", "y"); err == nil {
		t.Error("illegal skill name should be rejected")
	}
	if err := WriteSkill(wd, RoleWorker, "qa", "blank", "x", "   "); err == nil {
		t.Error("empty body should be rejected")
	}

	if err := RemoveSkill(wd, RoleWorker, "qa", "pnl-report"); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}
	reg2, _ := skill.LoadRegistry(SkillsDir(wd, RoleWorker, "qa"), "")
	if _, ok := reg2.Get("pnl-report"); ok {
		t.Error("skill still present after RemoveSkill")
	}
}

// TestWriteMemberDirRoundTrip: an operator spec serialised by WriteMemberDir is
// read back faithfully by the loader (RP-8) — system prompt, when_to_use, the
// active/deferred tool split, and the optional schedule all survive.
func TestWriteMemberDirRoundTrip(t *testing.T) {
	wd := t.TempDir()
	spec := MemberSpec{
		Name:         "qa-bot",
		SystemPrompt: "You are QA.",
		WhenToUse:    "QA and testing.",
		Model:        "claude-sonnet-4-6",
		Effort:       "high",
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
	if ld.Def.Model != "claude-sonnet-4-6" || ld.Effort != "high" {
		t.Errorf("model/effort pins = %q / %q, want claude-sonnet-4-6 / high", ld.Def.Model, ld.Effort)
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
	if err := WriteMemberDir(wd, MemberSpec{Name: "badmodel", SystemPrompt: "x", Model: "gpt-99"}); err == nil {
		t.Error("unknown model should error")
	}
	if err := WriteMemberDir(wd, MemberSpec{Name: "badeffort", SystemPrompt: "x", Effort: "max"}); err == nil {
		t.Error("invalid effort should error")
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
