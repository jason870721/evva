package swarm

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
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

// forgetWorktree drops a member's worktree record. The branch on disk is NOT
// touched — teardown is RemoveMemberWorktree's job; this only stops the space
// claiming a member it no longer has.
func (sp *SwarmSpace) forgetWorktree(name string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	delete(sp.worktrees, name)
}

// releaseMemberWorktree tears a removed member's worktree down (SWT-5), but
// never destroys work: a worktree holding uncommitted changes or commits the
// base has not taken is PRESERVED, and the leader plus the operator get one
// durable notice naming the branch so the work is recoverable. Removal is the
// accidental path — reset is the deliberate one, and only that forces.
func (s *Supervisor) releaseMemberWorktree(name string) {
	sess, ok := s.sp.memberWorktree(name)
	if !ok {
		return
	}
	s.sp.forgetWorktree(name)
	if removed, summary := mode.RemoveMemberWorktree(s.sp.ctx, sess, false); !removed {
		s.notifyOps(name, "Worktree preserved for removed member "+name,
			fmt.Sprintf("%s left the team with unintegrated work in its worktree (%s), so the worktree was KEPT "+
				"rather than deleted.\n\nBranch: %s\nPath: %s\n\nThe work is not lost: merge the branch (or inspect "+
				"it) to recover it, then delete the branch and worktree by hand. Nothing will reuse them.",
				name, summary, sess.Branch, filepath.ToSlash(sess.Path)))
	}
}

// ResetWorktrees force-removes every swarm-member worktree and branch in the
// repo at workdir (SWT-5). Reset means blank slate — it already wipes the
// ledger and every transcript — so this one path deliberately destroys
// uncommitted work; RemoveMember, the accidental path, preserves it instead.
//
// It sweeps by REPO rather than by a space's live records, so worktrees
// orphaned by a crash are cleared too, and it runs after the space is torn
// down (nothing is holding those checkouts open). Best-effort: each failure is
// logged and the sweep continues. A non-repo workdir is a silent no-op.
func ResetWorktrees(ctx context.Context, workdir string, log *slog.Logger) {
	sessions, err := mode.ListMemberWorktrees(ctx, workdir)
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if removed, summary := mode.RemoveMemberWorktree(ctx, sess, true); !removed && log != nil {
			log.Warn("swarm: reset: worktree removal failed", "branch", sess.Branch, "detail", summary)
		}
	}
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
