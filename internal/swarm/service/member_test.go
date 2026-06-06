package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/webapi"

	// Populate pkg/toolset.DefaultRegistry so SelectableTools sees the real
	// catalog. depcheck only scans production imports, so this test-only import of
	// an internal package is allowed.
	_ "github.com/johnny1110/evva/internal/toolset"
)

// TestServiceScheduleCRUD: the operator may set/clear ANY member's schedule via
// the service — including the LEADER's (no self-guard, the symmetric complement
// to RP-7's leader tool). The roster snapshot reflects it; bad input errors.
func TestServiceScheduleCRUD(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc) // leader + worker

	// The operator schedules the LEADER — RP-8's whole point.
	if err := svc.SetSchedule(id, "leader", "*/30 * * * *", "lead patrol"); err != nil {
		t.Fatalf("SetSchedule(leader): %v", err)
	}
	if c, p := memberSchedule(t, svc, id, "leader"); c != "*/30 * * * *" || p != "lead patrol" {
		t.Errorf("leader schedule = %q / %q after set", c, p)
	}

	if err := svc.ClearSchedule(id, "leader"); err != nil {
		t.Fatalf("ClearSchedule(leader): %v", err)
	}
	if c, _ := memberSchedule(t, svc, id, "leader"); c != "" {
		t.Errorf("leader cron = %q after clear, want empty", c)
	}

	// Guard rails.
	if err := svc.SetSchedule(id, "worker", "not a cron", "x"); err == nil {
		t.Error("bad cron should error")
	}
	if err := svc.SetSchedule(id, "ghost", "* * * * *", "x"); err == nil {
		t.Error("unknown member should error")
	}
	if err := svc.SetSchedule("nope", "worker", "* * * * *", "x"); err == nil {
		t.Error("unknown space should error")
	}
}

// TestServiceCreateRemoveNotifiesLeader: creating/removing a worker brings it
// in/out of the roster AND drops a "system"-authored note to the leader (only the
// when_to_use). The leader is unique — it cannot be removed.
func TestServiceCreateRemoveNotifiesLeader(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	if err := svc.CreateMember(id, webapi.MemberSpec{Name: "qa", SystemPrompt: "You are QA.", WhenToUse: "QA and regression testing"}); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if !hasMember(svc, id, "qa") {
		t.Fatal("qa not in roster after create")
	}
	waitSystemMsg(t, svc, id, "qa", "QA and regression testing")

	// Leader can't be removed.
	if err := svc.RemoveMember(id, "leader", false); err == nil {
		t.Error("removing the leader should error")
	}
	if err := svc.RemoveMember(id, "qa", false); err != nil {
		t.Fatalf("RemoveMember(qa): %v", err)
	}
	if hasMember(svc, id, "qa") {
		t.Error("qa still in roster after remove")
	}
	waitSystemMsg(t, svc, id, "qa", "left the team")
}

// TestServiceManifestRewrite: a Register'd space (real evva-swarm.yml) stays
// authoritative — create adds the worker to the manifest, remove drops it, and
// deleteDir also erases the on-disk dir (RP-8 restart durability).
func TestServiceManifestRewrite(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	svc.loadConfig = scriptedLoadConfig(t.TempDir())
	dir := writeTeamFixture(t) // leader + worker-a + worker-b, real manifest
	id, err := svc.Register(dir, "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	manifestPath := filepath.Join(dir, "evva-swarm.yml")

	if err := svc.CreateMember(id, webapi.MemberSpec{Name: "qa", SystemPrompt: "You are QA."}); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if !manifestHasWorker(t, manifestPath, "qa") {
		t.Error("manifest missing qa after create (not restart-durable)")
	}

	if err := svc.RemoveMember(id, "qa", false); err != nil {
		t.Fatalf("RemoveMember(qa): %v", err)
	}
	if manifestHasWorker(t, manifestPath, "qa") {
		t.Error("manifest still lists qa after remove")
	}

	// deleteDir erases the on-disk definition; the manifest is dropped first so a
	// restart never references the missing dir.
	if err := svc.RemoveMember(id, "worker-a", true); err != nil {
		t.Fatalf("RemoveMember(worker-a, deleteDir): %v", err)
	}
	if manifestHasWorker(t, manifestPath, "worker-a") {
		t.Error("manifest still lists worker-a after remove")
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "sub", "worker-a")); !os.IsNotExist(err) {
		t.Error("worker-a dir should be gone with deleteDir=true")
	}
}

// TestServiceSelectableTools: the add-agent catalog includes real worker tools
// and excludes collaboration + operator/runtime tools.
func TestServiceSelectableTools(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	set := map[string]bool{}
	for _, n := range svc.SelectableTools() {
		set[n] = true
	}
	for _, want := range []string{"read", "write", "bash"} {
		if !set[want] {
			t.Errorf("SelectableTools missing worker tool %q", want)
		}
	}
	for _, deny := range []string{"send_message", "task_create", "schedule_set", "ask_user_question", "config"} {
		if set[deny] {
			t.Errorf("SelectableTools should exclude %q", deny)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func memberSchedule(t *testing.T, svc *Service, id, name string) (cron, prompt string) {
	t.Helper()
	roster, ok := svc.Roster(id)
	if !ok {
		t.Fatalf("Roster(%q) not found", id)
	}
	for _, m := range roster {
		if m.Name == name {
			return m.Cron, m.SchedulePrompt
		}
	}
	t.Fatalf("member %q not in roster", name)
	return "", ""
}

func hasMember(svc *Service, id, name string) bool {
	roster, ok := svc.Roster(id)
	if !ok {
		return false
	}
	for _, m := range roster {
		if m.Name == name {
			return true
		}
	}
	return false
}

func waitSystemMsg(t *testing.T, svc *Service, id string, must ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _ := svc.Messages(id)
		for _, m := range msgs {
			if m.Sender != "system" {
				continue
			}
			all := true
			for _, s := range must {
				if !strings.Contains(m.Body, s) {
					all = false
					break
				}
			}
			if all {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no system message containing %v", must)
}

func manifestHasWorker(t *testing.T, path, name string) bool {
	t.Helper()
	m, err := agentdef.LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	for _, w := range m.Workers {
		if w.Agent == name {
			return true
		}
	}
	return false
}
