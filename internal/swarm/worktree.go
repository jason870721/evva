package swarm

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/tools/mode"
)

// MergeResult is the outcome of integrating one member's branch onto the base
// checkout: the git-level report plus whatever the post-merge housekeeping had
// to say.
type MergeResult struct {
	mode.MergeReport
	// Member is the member whose branch was merged.
	Member string
	// Branch is the branch that was merged (worktree-swarm-<member>).
	Branch string
	// RefreshWarning is non-empty when the merge landed but the member's own
	// branch could not be fast-forwarded onto the new base tip afterwards
	// (D4) — almost always because the member has uncommitted work in flight.
	// Never fatal: the merge itself already succeeded.
	RefreshWarning string
}

// MergeMemberWorktree integrates a member's committed work onto the space's
// base checkout under the abort-on-conflict contract, then fast-forwards that
// member's own branch onto the new base tip (D4 drift control).
//
// The leader is the only caller by design (§4): it is one agent with one run
// slot, so leader-driven merges are serialized by construction — `git merge`
// from two processes into one checkout is unsafe, and this is what makes the
// base branch just another leader-owned ledger. A conflict is a normal,
// visible outcome (Conflicts non-empty, nil error): the base is aborted clean
// and the worker resolves on its own side.
func (sp *SwarmSpace) MergeMemberWorktree(ctx context.Context, member string) (MergeResult, error) {
	sess, ok := sp.memberWorktree(member)
	if !ok {
		if _, known := sp.Roster.roleOf(member); !known {
			return MergeResult{}, fmt.Errorf("no such member %q (%s)", member, joinNames(sp.isolatedMembers()))
		}
		return MergeResult{}, fmt.Errorf("member %q does not run under worktree isolation — it edits the shared workdir directly, so there is nothing to merge", member)
	}

	report, err := mode.MergeBranch(ctx, sp.Workdir, sess.Branch)
	if err != nil {
		return MergeResult{}, err
	}
	res := MergeResult{MergeReport: report, Member: member, Branch: sess.Branch}
	if report.NoOp || len(report.Conflicts) > 0 {
		return res, nil
	}

	// D4: keep the member's branch tracking the base tip so its next task does
	// not start from a stale base. Best-effort — a member with uncommitted work
	// in flight must never be auto-reset.
	if rErr := mode.RefreshWorktree(ctx, sess.Path, report.BaseBranch); rErr != nil {
		res.RefreshWarning = rErr.Error()
	}
	return res, nil
}

// worktreeGroundingFor computes a member's worktree situation for the team
// protocol (SWT-4). Called at registerDef — one phase BEFORE the worktree is
// provisioned — so the branch name comes from mode.MemberBranch, which derives
// it deterministically instead of reading a live session. The leader never
// gets a branch (D8) but learns the integration half whenever the team runs
// any worktrees.
func (sp *SwarmSpace) worktreeGroundingFor(name string, role agentdef.Role, override string) worktreeGrounding {
	g := worktreeGrounding{TeamIsolated: sp.teamIsolated}
	if role != agentdef.RoleLeader && agentdef.ResolveWorktree(sp.settings.WorktreeIsolation, override) {
		g.Branch = mode.MemberBranch(memberWorktreeSlug(name))
		g.TeamIsolated = true
	}
	return g
}

// isolatedMembers lists the members currently running under worktree
// isolation, sorted — the actionable half of an unknown-member error.
func (sp *SwarmSpace) isolatedMembers() []string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	out := make([]string, 0, len(sp.worktrees))
	for name := range sp.worktrees {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return "no members run under worktree isolation"
	}
	return "members under worktree isolation: " + strings.Join(names, ", ")
}
