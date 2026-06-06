package agentdef

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

func leaderDir() string  { return filepath.Join("testdata", "agents", "main", "leader") }
func backendDir() string { return filepath.Join("testdata", "agents", "sub", "backend-dev") }
func frontDir() string   { return filepath.Join("testdata", "agents", "sub", "frontend-dev") }

func TestBuildLeaderFields(t *testing.T) {
	got, err := (&Loader{}).Build(leaderDir(), RoleLeader)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := got.Def
	if d.Name != "leader" {
		t.Errorf("Name = %q", d.Name)
	}
	if !reflect.DeepEqual(d.As, []string{"main"}) {
		t.Errorf("As = %v, want [main]", d.As)
	}
	// SystemPrompt is the verbatim file body.
	raw, _ := os.ReadFile(filepath.Join(leaderDir(), "system_prompt.md"))
	if d.SystemPrompt != string(raw) {
		t.Errorf("SystemPrompt not verbatim:\n got %q\nwant %q", d.SystemPrompt, string(raw))
	}
	wantActive := []tools.ToolName{"task_create", "task_assign", "task_verify", "list_members", "send_message"}
	if !reflect.DeepEqual(d.ActiveTools, wantActive) {
		t.Errorf("ActiveTools = %v, want %v", d.ActiveTools, wantActive)
	}
	if !reflect.DeepEqual(d.DeferredTools, []tools.ToolName{"task_list"}) {
		t.Errorf("DeferredTools = %v", d.DeferredTools)
	}
	if d.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q", d.Model)
	}
	if d.WhenToUse == "" || !d.InjectMemory || !d.AdvertiseSkills {
		t.Errorf("profile fields: WhenToUse=%q InjectMemory=%v AdvertiseSkills=%v", d.WhenToUse, d.InjectMemory, d.AdvertiseSkills)
	}
	if got.Effort != "high" {
		t.Errorf("Effort = %q, want high", got.Effort)
	}
	if got.Role != RoleLeader {
		t.Errorf("Role = %q", got.Role)
	}
	if got.Schedule != nil {
		t.Errorf("Schedule = %+v, want nil", got.Schedule)
	}
	if got.Skills == nil {
		t.Fatal("Skills registry is nil")
	}
	if _, ok := got.Skills.Get("standup"); !ok {
		t.Errorf("skill %q not loaded", "standup")
	}
}

func TestBuildWorkerCronSchedule(t *testing.T) {
	got, err := (&Loader{}).Build(backendDir(), RoleWorker)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !reflect.DeepEqual(got.Def.As, []string{"subagent"}) {
		t.Errorf("As = %v, want [subagent]", got.Def.As)
	}
	if got.Schedule == nil || got.Schedule.Cron != "*/5 * * * *" {
		t.Fatalf("Schedule = %+v, want cron */5 * * * *", got.Schedule)
	}
	if got.Schedule.Prompt != "scan the backend for failing health checks" {
		t.Errorf("Schedule.Prompt = %q, want the profile's wake prompt (RP-7)", got.Schedule.Prompt)
	}
	if got.Def.Model != "claude-sonnet-4-6" || got.Effort != "medium" {
		t.Errorf("model/effort = %q/%q", got.Def.Model, got.Effort)
	}
}

func TestBuildWorkerEverySchedule(t *testing.T) {
	got, err := (&Loader{}).Build(frontDir(), RoleWorker)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got.Schedule == nil || got.Schedule.Every != 30*time.Second {
		t.Fatalf("Schedule = %+v, want every 30s", got.Schedule)
	}
	// No model override → empty (inherits parent); no effort.
	if got.Def.Model != "" || got.Effort != "" {
		t.Errorf("model/effort = %q/%q, want empty", got.Def.Model, got.Effort)
	}
}

func TestBuildMissingPrompt(t *testing.T) {
	// A dir with no system_prompt.md must error.
	if _, err := (&Loader{}).Build(t.TempDir(), RoleWorker); err == nil {
		t.Fatal("want error when system_prompt.md is missing")
	}
}

func TestBuildEmptyPrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "system_prompt.md"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Loader{}).Build(dir, RoleWorker); err == nil {
		t.Fatal("want error when system_prompt.md is blank")
	}
}

// TestBuildReCallable covers AC#4: Build is pure — twice on the same dir yields
// equal results.
func TestBuildReCallable(t *testing.T) {
	l := &Loader{}
	a, err := l.Build(backendDir(), RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.Build(backendDir(), RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Def, b.Def) {
		t.Errorf("Def differs across calls")
	}
	if !reflect.DeepEqual(a.Schedule, b.Schedule) {
		t.Errorf("Schedule differs: %+v vs %+v", a.Schedule, b.Schedule)
	}
	if a.Effort != b.Effort || a.Role != b.Role {
		t.Errorf("Effort/Role differ")
	}
}

func TestBuildAll(t *testing.T) {
	m, err := LoadManifest(filepath.Join("testdata", "evva-swarm.yml"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, warnings, err := (&Loader{}).BuildAll("testdata", m)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("loaded %d agents, want 3", len(loaded))
	}
	// Leader first, then workers in manifest order.
	wantNames := []string{"leader", "backend-dev", "frontend-dev"}
	wantRoles := []Role{RoleLeader, RoleWorker, RoleWorker}
	for i, l := range loaded {
		if l.Def.Name != wantNames[i] || l.Role != wantRoles[i] {
			t.Errorf("loaded[%d] = %s/%s, want %s/%s", i, l.Def.Name, l.Role, wantNames[i], wantRoles[i])
		}
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none (fixtures are clean)", warnings)
	}
}

// TestBuildAllManifestScheduleOverridesProfile: a schedule declared on a member
// in the manifest is authoritative over that agent's profile.yml schedule, while
// a member without a manifest schedule keeps its profile one (RP-7 §3.7).
func TestBuildAllManifestScheduleOverridesProfile(t *testing.T) {
	m := Manifest{
		Name:   "team",
		Leader: Member{Agent: "leader"},
		Workers: []Member{
			// backend-dev's profile declares cron "*/5 * * * *"; the manifest overrides it.
			{Agent: "backend-dev", Schedule: &Schedule{Cron: "0 0 * * *", Prompt: "nightly sweep"}},
			// frontend-dev has no manifest schedule → its profile "every 30s" stands.
			{Agent: "frontend-dev"},
		},
	}
	loaded, _, err := (&Loader{}).BuildAll("testdata", m)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	find := func(name string) Loaded {
		for _, l := range loaded {
			if l.Def.Name == name {
				return l
			}
		}
		t.Fatalf("loaded missing %q", name)
		return Loaded{}
	}
	if be := find("backend-dev").Schedule; be == nil || be.Cron != "0 0 * * *" || be.Prompt != "nightly sweep" {
		t.Errorf("backend-dev schedule = %+v, want manifest cron 0 0 * * * / nightly sweep (override)", be)
	}
	if fe := find("frontend-dev").Schedule; fe == nil || fe.Every != 30*time.Second {
		t.Errorf("frontend-dev schedule = %+v, want profile every 30s (no manifest override)", fe)
	}
}

func TestBuildAllMissingAgentDir(t *testing.T) {
	m := Manifest{Name: "x", Leader: Member{Agent: "ghost"}}
	if _, _, err := (&Loader{}).BuildAll("testdata", m); err == nil {
		t.Fatal("want error when an agent dir is missing")
	}
}
