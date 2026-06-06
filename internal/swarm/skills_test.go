package swarm

import (
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
)

func countTool(list []tools.ToolName, n tools.ToolName) int {
	c := 0
	for _, tn := range list {
		if tn == n {
			c++
		}
	}
	return c
}

// TestRegisterDefForcesSkills (RP-10-1): the swarm forces AdvertiseSkills=true and
// injects the built-in skill tool on EVERY member, overriding the on-disk profile —
// and without duplicating a skill tool a member already declared.
func TestRegisterDefForcesSkills(t *testing.T) {
	cfg := stubConfig(t)
	loaded := []agentdef.Loaded{
		{ // leader explicitly DISABLES advertise + has no skill tool → both forced on
			Def:    agent.AgentDefinition{Name: "leader", SystemPrompt: "lead", AdvertiseSkills: false, ActiveTools: []tools.ToolName{tools.READ_FILE}, Model: stubModel},
			Skills: skill.NewRegistry(), Role: agentdef.RoleLeader,
		},
		{ // worker already lists the skill tool → must not be duplicated
			Def:    agent.AgentDefinition{Name: "worker", SystemPrompt: "work", ActiveTools: []tools.ToolName{tools.SKILL, tools.READ_FILE}, Model: stubModel},
			Skills: skill.NewRegistry(), Role: agentdef.RoleWorker,
		},
	}
	sp, err := NewSpace("s", testManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	for _, name := range []string{"leader", "worker"} {
		def, ok := sp.reg.Get(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if !def.AdvertiseSkills {
			t.Errorf("%s: AdvertiseSkills not forced true", name)
		}
		if c := countTool(def.ActiveTools, tools.SKILL); c != 1 {
			t.Errorf("%s: skill tool count = %d, want exactly 1; tools=%v", name, c, def.ActiveTools)
		}
		if c := countTool(def.ActiveTools, tools.READ_FILE); c != 1 {
			t.Errorf("%s: read tool dropped/duplicated (count %d); tools=%v", name, c, def.ActiveTools)
		}
	}
}

// TestReloadMemberSkills (RP-10-4): once a skill is authored on disk, ReloadMemberSkills
// re-scans the member's dir and the live agent's catalog reflects it — an idle member
// applies it at the next run-loop tick (via the serve boundary drain).
func TestReloadMemberSkills(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("s", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	t.Cleanup(sp.Shutdown) // LIFO: runs AFTER startSup's cancel+Wait, so loops are down first
	sup := startSup(t, sp)

	ag, ok := sp.agentOf("worker-a")
	if !ok {
		t.Fatal("worker-a agent missing")
	}
	if len(ag.Skills()) != 0 {
		t.Fatalf("worker-a should start with no skills; got %v", ag.Skills())
	}

	if err := agentdef.WriteSkill(cfg.WorkDir, agentdef.RoleWorker, "worker-a", "newskill", "a fresh skill", "do the thing"); err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}
	if err := sup.ReloadMemberSkills("worker-a"); err != nil {
		t.Fatalf("ReloadMemberSkills: %v", err)
	}

	waitFor(t, 2*time.Second, "worker-a's live catalog reflects the reload", func() bool {
		for _, s := range ag.Skills() {
			if s.Name == "newskill" {
				return true
			}
		}
		return false
	})

	// A delete + reload removes it again.
	if err := agentdef.RemoveSkill(cfg.WorkDir, agentdef.RoleWorker, "worker-a", "newskill"); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}
	if err := sup.ReloadMemberSkills("worker-a"); err != nil {
		t.Fatalf("ReloadMemberSkills (remove): %v", err)
	}
	waitFor(t, 2*time.Second, "worker-a's catalog drops the deleted skill", func() bool {
		return len(ag.Skills()) == 0
	})
}
