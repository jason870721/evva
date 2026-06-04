package swarm

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

func TestInjectTeamProtocol_RoleSpecific(t *testing.T) {
	persona := "# Backend Engineer\nYou build APIs."

	leader := injectTeamProtocol(persona, agentdef.RoleLeader)
	worker := injectTeamProtocol(persona, agentdef.RoleWorker)

	// Persona leads in both.
	if !strings.HasPrefix(leader, persona) || !strings.HasPrefix(worker, persona) {
		t.Fatal("persona should lead the composed prompt")
	}

	// Common protocol present in both.
	for _, p := range []string{leader, worker} {
		if !strings.Contains(p, "Working in a swarm") || !strings.Contains(p, "list_members") {
			t.Error("common collaboration protocol missing")
		}
	}

	// Leader gets the leader protocol + its ledger-writing tools; worker does not.
	if !strings.Contains(leader, "Your role: the leader") {
		t.Error("leader protocol missing")
	}
	for _, tool := range []string{"task_create", "task_assign", "task_verify"} {
		if !strings.Contains(leader, tool) {
			t.Errorf("leader protocol should mention %q", tool)
		}
		if strings.Contains(worker, tool) {
			t.Errorf("worker protocol must not mention leader-only %q", tool)
		}
	}

	// Worker gets the worker protocol + its read-only task tools.
	if !strings.Contains(worker, "Your role: a worker") {
		t.Error("worker protocol missing")
	}
	for _, tool := range []string{"my_tasks", "task_get"} {
		if !strings.Contains(worker, tool) {
			t.Errorf("worker protocol should mention %q", tool)
		}
	}
}

// TestNewSpaceInjectsProtocol proves the wiring: after assembly, each member's
// registered persona carries its authored prompt AND its role protocol — the
// operator declared neither the mechanics nor the tools.
func TestNewSpaceInjectsProtocol(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("s", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	ld, ok := sp.reg.Get("leader")
	if !ok {
		t.Fatal("leader persona not registered")
	}
	if !strings.Contains(ld.SystemPrompt, "You are leader.") {
		t.Error("authored persona missing from leader prompt")
	}
	if !strings.Contains(ld.SystemPrompt, "Your role: the leader") {
		t.Error("leader protocol not injected into the registered persona")
	}

	wd, _ := sp.reg.Get("worker-a")
	if !strings.Contains(wd.SystemPrompt, "Your role: a worker") {
		t.Error("worker protocol not injected")
	}
	if strings.Contains(wd.SystemPrompt, "Your role: the leader") {
		t.Error("worker wrongly got the leader protocol")
	}
}

// A member that authored no persona still gets a usable, protocol-only prompt.
func TestInjectTeamProtocol_BlankPersona(t *testing.T) {
	out := injectTeamProtocol("", agentdef.RoleWorker)
	if strings.HasPrefix(out, "\n") {
		t.Error("blank persona should not leave leading blank lines")
	}
	if !strings.Contains(out, "Working in a swarm") || !strings.Contains(out, "Your role: a worker") {
		t.Error("protocol-only prompt should still carry the full protocol")
	}
}
