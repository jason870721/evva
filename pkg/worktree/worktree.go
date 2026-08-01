// Package worktree is the public surface for git-worktree isolation: provision
// a persistent, deterministically-named worktree per long-lived agent, probe
// its integration state, and merge its committed work back under an
// abort-on-conflict contract.
//
// It exists because the multi-agent layer needs these primitives and must not
// reach into evva's internals to get them. The invariant (Veronica global #1,
// enforced by scripts/depcheck.sh) is that everything under internal/swarm
// reaches the agent runtime ONLY through pkg/* — so anything the swarm needs
// is by definition part of evva's public contract, and a downstream module
// building its own multi-agent system gets the same primitives on the same
// terms.
//
// This is a thin facade over the shipped implementation in internal/tools/mode,
// which is the pattern depcheck.sh documents for the public SDK packages: the
// types are ALIASES, not copies, so there is exactly one WorktreeSession in the
// process and no conversion at the boundary. The single-agent enter_worktree /
// exit_worktree tools and the swarm therefore share one merge core by
// construction rather than by discipline.
//
// The eventual cleanup is to relocate the implementation here and leave the
// aliases pointing the other way; that move drags ten unexported helpers out
// of a 700-line tool file and belongs in its own change.
package worktree

import (
	"context"

	"github.com/johnny1110/evva/internal/tools/mode"
)

// Session is one provisioned worktree: where it lives, which branch it holds,
// and the repository root it was anchored at.
//
// An alias rather than a struct copy — a copy would need conversion at every
// boundary and would drift the moment a field was added on one side.
type Session = mode.WorktreeSession

// MergeReport is the structured outcome of Merge. Structured rather than
// pre-formatted so each caller renders its own message: the swarm's
// worktree_merge tool and the exit_worktree action word theirs differently.
type MergeReport = mode.MergeReport

// Status is a worktree's integration state — how far ahead/behind its branch
// is and whether it has uncommitted work.
type Status = mode.WorktreeStatus

// Provision provisions, or reattaches to, a persistent worktree for slug.
//
// Deterministic by design: no random suffix, so a restart reattaches to the
// same branch instead of orphaning the previous run's work.
func Provision(ctx context.Context, rootWorkdir, slug string) (Session, error) {
	return mode.ProvisionMemberWorktree(ctx, rootWorkdir, slug)
}

// Branch returns the branch name Provision will use for slug, without touching
// git. For callers that must name a branch before its worktree exists.
func Branch(slug string) string { return mode.MemberBranch(slug) }

// Preflight reports whether rootWorkdir can host worktrees at all: inside a git
// repository, and with at least one commit (a worktree is created from HEAD).
// Callers use it to fail up front with a targeted message instead of failing
// deep inside per-member construction.
func Preflight(ctx context.Context, rootWorkdir string) error {
	return mode.PreflightWorktreeRepo(ctx, rootWorkdir)
}

// Merge integrates childBranch into the current branch of the base checkout at
// baseDir, under the abort-on-conflict contract: it refuses a dirty base,
// refuses an unclean source, no-ops when there is nothing to integrate, merges
// --no-ff otherwise, and on conflict runs `git merge --abort` and returns the
// conflicted paths. The base branch is NEVER left half-merged.
//
// A conflict is an actionable outcome, not a failure: it arrives via
// MergeReport.Conflicts with a nil error. Only a dirty base, an unclean source,
// or a genuine git error returns non-nil.
//
// Does NOT tear the child worktree down — the base tip advances and the child
// stays, ready for its next task.
func Merge(ctx context.Context, baseDir, childBranch string) (MergeReport, error) {
	return mode.MergeBranch(ctx, baseDir, childBranch)
}

// Refresh fast-forwards a worktree onto baseBranch. Drift control after a
// merge; ff-only, so it can never create a merge commit in the child.
func Refresh(ctx context.Context, wtPath, baseBranch string) error {
	return mode.RefreshWorktree(ctx, wtPath, baseBranch)
}

// Remove tears a worktree down and reports whether it went, plus a
// human-readable summary. force removes even with uncommitted work; without
// it, a dirty worktree is kept and the summary says why.
func Remove(ctx context.Context, sess Session, force bool) (bool, string) {
	return mode.RemoveMemberWorktree(ctx, sess, force)
}

// List enumerates the worktrees provisioned under rootWorkdir. Used to reclaim
// orphans left by a crash — a restart that finds worktrees with no owning
// member cleans them up.
func List(ctx context.Context, rootWorkdir string) ([]Session, error) {
	return mode.ListMemberWorktrees(ctx, rootWorkdir)
}

// ProbeStatus probes one worktree against the base checkout's current branch.
// Best-effort by design: a probe that cannot run returns an error and callers
// omit the column rather than failing the whole roster.
func ProbeStatus(ctx context.Context, sess Session) (Status, error) {
	return mode.MemberWorktreeStatus(ctx, sess)
}
