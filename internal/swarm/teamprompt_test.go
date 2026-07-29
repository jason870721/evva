package swarm

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

func TestInjectTeamProtocol_RoleSpecific(t *testing.T) {
	persona := "# Backend Engineer\nYou build APIs."

	leader := injectTeamProtocol(persona, "lead", "vero-tech-swarm", agentdef.RoleLeader, true, nil, worktreeGrounding{})
	worker := injectTeamProtocol(persona, "backend-a", "vero-tech-swarm", agentdef.RoleWorker, true, nil, worktreeGrounding{})

	// Persona leads in both (grounding + protocol are appended after it).
	if !strings.HasPrefix(leader, persona) || !strings.HasPrefix(worker, persona) {
		t.Fatal("persona should lead the composed prompt")
	}

	// RP-5: each member is grounded in its space/name/role, with no date/time.
	if !strings.Contains(worker, "# Your place in the swarm") ||
		!strings.Contains(worker, "vero-tech-swarm") ||
		!strings.Contains(worker, "backend-a") ||
		!strings.Contains(worker, "role: worker") {
		t.Errorf("worker prompt missing swarm grounding:\n%s", worker)
	}
	if !strings.Contains(leader, "lead") || !strings.Contains(leader, "role: leader") {
		t.Errorf("leader prompt missing swarm grounding:\n%s", leader)
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

	// RP-C: the leader closes the advice loop back to teammates (leader-only).
	if !strings.Contains(leader, "Close the loop") {
		t.Error("leader protocol should instruct closing the advice loop")
	}
	if strings.Contains(worker, "Close the loop") {
		t.Error("advice-loop closure is leader-only")
	}

	// RP-26 Part B: the leader is taught to institutionalize procedures via
	// skill_publish (and only the leader — workers don't hold the tool).
	if !strings.Contains(leader, "skill_publish") || !strings.Contains(leader, "Institutionalize") {
		t.Error("leader protocol should teach skill_publish institutionalization")
	}
	if strings.Contains(worker, "skill_publish") {
		t.Error("skill_publish guidance is leader-only")
	}

	// BB: everyone learns to trust the wake brief's board and refresh it with
	// blackboard_read; only the leader learns the curation discipline.
	for _, p := range []string{leader, worker} {
		if !strings.Contains(p, "Team blackboard") || !strings.Contains(p, "blackboard_read") {
			t.Error("common protocol should teach the blackboard brief + mid-run refresh")
		}
	}
	if !strings.Contains(leader, "Maintain the team blackboard") || !strings.Contains(leader, "blackboard_write") {
		t.Error("leader protocol should teach blackboard curation")
	}
	if strings.Contains(worker, "blackboard_write") {
		t.Error("blackboard_write guidance is leader-only")
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
	// RP-5: each member is grounded in its space/name/role (space name is "team").
	if !strings.Contains(ld.SystemPrompt, "# Your place in the swarm") ||
		!strings.Contains(ld.SystemPrompt, "**Swarm space:** team") ||
		!strings.Contains(ld.SystemPrompt, "leader (role: leader)") {
		t.Errorf("leader prompt missing swarm grounding:\n%s", ld.SystemPrompt)
	}

	wd, _ := sp.reg.Get("worker-a")
	if !strings.Contains(wd.SystemPrompt, "Your role: a worker") {
		t.Error("worker protocol not injected")
	}
	if strings.Contains(wd.SystemPrompt, "Your role: the leader") {
		t.Error("worker wrongly got the leader protocol")
	}
	if !strings.Contains(wd.SystemPrompt, "worker-a (role: worker)") {
		t.Errorf("worker prompt missing swarm grounding:\n%s", wd.SystemPrompt)
	}
	if !strings.Contains(wd.SystemPrompt, "task_propose") {
		t.Error("worker protocol missing the proposal inlet (RP-23)")
	}
}

// A member that authored no persona still gets a usable, protocol-only prompt.
func TestInjectTeamProtocol_BlankPersona(t *testing.T) {
	out := injectTeamProtocol("", "backend-a", "vero-tech-swarm", agentdef.RoleWorker, true, nil, worktreeGrounding{})
	if strings.HasPrefix(out, "\n") {
		t.Error("blank persona should not leave leading blank lines")
	}
	if !strings.Contains(out, "Working in a swarm") || !strings.Contains(out, "Your role: a worker") {
		t.Error("protocol-only prompt should still carry the full protocol")
	}
}

// RP-25: the memory protocol is injected only for members that can actually
// maintain memory files (write/edit), names the member's own tier-correct dir,
// and stays out of write-less members' prompts entirely.
func TestInjectTeamProtocol_MemoryProtocol(t *testing.T) {
	leader := injectTeamProtocol("p", "lead", "s", agentdef.RoleLeader, true, nil, worktreeGrounding{})
	worker := injectTeamProtocol("p", "friday", "s", agentdef.RoleWorker, true, nil, worktreeGrounding{})
	readonly := injectTeamProtocol("p", "observer", "s", agentdef.RoleWorker, false, nil, worktreeGrounding{})

	if !strings.Contains(leader, "## Your long-term memory") ||
		!strings.Contains(leader, "agents/main/lead/memory") {
		t.Errorf("leader memory protocol missing or mis-pathed:\n%s", leader)
	}
	if !strings.Contains(worker, "agents/sub/friday/memory") ||
		!strings.Contains(worker, "MEMORY.md") ||
		!strings.Contains(worker, "type: user|feedback|project|reference") {
		t.Errorf("worker memory protocol missing the memdir conventions:\n%s", worker)
	}
	if strings.Contains(readonly, "## Your long-term memory") {
		t.Error("a member without write/edit must not get the memory protocol")
	}
}
