package swarm

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

// SWT-4: the worktree protocol section. A worker learns the commit-before-done
// discipline only when IT is isolated; the leader learns the integration half
// whenever ANY member is; a space with no worktrees renders neither.

func TestWorktreeProtocol_IsolatedWorker(t *testing.T) {
	got := worktreeProtocol(agentdef.RoleWorker, worktreeGrounding{Branch: "worktree-swarm-qa", TeamIsolated: true})
	if got == "" {
		t.Fatal("an isolated worker must be taught its worktree protocol")
	}
	// The branch it actually sits on, and the three rules that make the loop work.
	for _, want := range []string{
		"worktree-swarm-qa",
		"Commit before you report done",
		"Start each task by merging the base branch",
		"Resolve conflicts in YOUR worktree",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("worker protocol missing %q:\n%s", want, got)
		}
	}
}

func TestWorktreeProtocol_SharedWorkdirWorkerGetsNothing(t *testing.T) {
	// A member that opted out sits on the shared checkout — teaching it a
	// worktree discipline it cannot follow is noise.
	if got := worktreeProtocol(agentdef.RoleWorker, worktreeGrounding{TeamIsolated: true}); got != "" {
		t.Errorf("a non-isolated worker should get no worktree section, got:\n%s", got)
	}
}

func TestWorktreeProtocol_Leader(t *testing.T) {
	got := worktreeProtocol(agentdef.RoleLeader, worktreeGrounding{TeamIsolated: true})
	if got == "" {
		t.Fatal("the leader must learn the integration protocol when the team runs worktrees")
	}
	for _, want := range []string{
		"worktree_merge",
		"inspect → `worktree_merge` → `task_verify`",
		"Nothing to integrate",
		"nothing is applied",
		"dirty",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("leader protocol missing %q:\n%s", want, got)
		}
	}
	// D8: the leader is told it stays on the base checkout and is the only
	// one who may merge. (The text does contain "your own branch" — that is
	// the recipe it relays TO a worker on conflict, not a claim about itself.)
	if !strings.Contains(got, "You stay on the base checkout") {
		t.Errorf("the leader must be told it stays on the base checkout (D8):\n%s", got)
	}
}

func TestWorktreeProtocol_OffIsByteIdentical(t *testing.T) {
	none := worktreeGrounding{}
	for _, role := range []agentdef.Role{agentdef.RoleLeader, agentdef.RoleWorker} {
		if got := worktreeProtocol(role, none); got != "" {
			t.Errorf("%s: a space with no worktrees should render nothing, got:\n%s", role, got)
		}
		with := teamProtocolSuffix("m", "space", role, false, nil, none)
		without := teamProtocolSuffix("m", "space", role, false, nil, worktreeGrounding{})
		if with != without {
			t.Errorf("%s: prompt should be byte-identical to the pre-SWT form", role)
		}
		if strings.Contains(with, "worktree") {
			t.Errorf("%s: no worktree text should leak into a non-isolated prompt:\n%s", role, with)
		}
	}
}

// The section reaches the assembled prompt, not just the helper.
func TestWorktreeProtocolReachesSuffix(t *testing.T) {
	worker := teamProtocolSuffix("qa", "coders", agentdef.RoleWorker, false, nil,
		worktreeGrounding{Branch: "worktree-swarm-qa", TeamIsolated: true})
	if !strings.Contains(worker, "## Your own worktree") {
		t.Errorf("worker suffix missing the worktree section:\n%s", worker)
	}
	leader := teamProtocolSuffix("lead", "coders", agentdef.RoleLeader, false, nil,
		worktreeGrounding{TeamIsolated: true})
	if !strings.Contains(leader, "## Integrating isolated work") {
		t.Errorf("leader suffix missing the integration section:\n%s", leader)
	}
}
