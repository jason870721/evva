package main

import (
	"context"

	"github.com/johnny1110/evva/pkg/worktree"
)

// This file is the separate-module compile proof for the public worktree
// surface, in the same spirit as inbox_drainer.go above.
//
// Veronica global invariant #1 says everything under internal/swarm reaches the
// agent runtime only through pkg/* (scripts/depcheck.sh enforces it). That rule
// is only meaningful if the pkg/* side is genuinely usable from outside the
// module — otherwise "route it through a facade" degrades into moving an import
// to somewhere the linter cannot see it.
//
// full-host is its own Go module, so Go forbids it from importing
// evva/internal/**. The fact that the code below compiles is therefore proof
// that a third party can provision worktrees, probe them, and merge them back
// with exactly the primitives evva's own multi-agent layer uses — including
// through the type aliases, which resolve to internal definitions the compiler
// will not let this module name directly.
//
// Compile-only: the flagship example is a single-agent TUI and never calls it.

// worktreeRoundTrip exercises the full public lifecycle: provision, probe,
// integrate, refresh, tear down.
func worktreeRoundTrip(ctx context.Context, repoRoot, member string) error {
	if err := worktree.Preflight(ctx, repoRoot); err != nil {
		return err
	}

	sess, err := worktree.Provision(ctx, repoRoot, member)
	if err != nil {
		return err
	}
	// Session is an alias for an internal type — naming its fields here is the
	// part that proves the alias is usable across the module boundary.
	_ = sess.Path
	_ = sess.Branch
	_ = sess.RepoRoot

	if st, err := worktree.ProbeStatus(ctx, sess); err == nil {
		_, _, _ = st.Ahead, st.Behind, st.Dirty
	}

	report, err := worktree.Merge(ctx, repoRoot, sess.Branch)
	if err != nil {
		return err
	}
	// A conflict is an outcome, not an error — the public contract says so.
	if len(report.Conflicts) == 0 && !report.NoOp {
		if err := worktree.Refresh(ctx, sess.Path, report.BaseBranch); err != nil {
			return err
		}
	}

	_, _ = worktree.Remove(ctx, sess, false)
	return nil
}

// worktreeBranchName shows the name-before-provision helper, which a host needs
// when it must render a branch name before the worktree exists.
func worktreeBranchName(member string) string { return worktree.Branch(member) }

// worktreeReclaim shows orphan reclamation after a crash.
func worktreeReclaim(ctx context.Context, repoRoot string) (int, error) {
	sessions, err := worktree.List(ctx, repoRoot)
	if err != nil {
		return 0, err
	}
	return len(sessions), nil
}
