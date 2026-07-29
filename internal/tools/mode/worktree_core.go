package mode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file holds the reusable worktree core — package-level functions with no
// dependency on the WorktreeController / session machinery (which is
// single-agent, tool-driven). They exist so the swarm layer
// (internal/swarm, the worktree-isolation wave) can provision one persistent
// worktree per opted-in member and let the leader integrate a member's
// committed work with the SAME abort-on-conflict merge contract the single-agent
// enter/exit tools use — instead of forking a second, drifting implementation.
//
//   ProvisionMemberWorktree — deterministic, restart-safe provision/reattach.
//   MergeBranch             — the executeMerge core, minus session/teardown.
//   RefreshWorktree         — ff-only drift control after a merge.
//
// exit_worktree's merge action is a thin caller of MergeBranch (see
// worktree.go); the two share one merge core by construction.

// ProvisionMemberWorktree provisions — or reattaches to — a persistent,
// deterministically-named worktree for a long-lived swarm member. Unlike
// CreateForSubagent it appends NO random suffix: the name must be stable so a
// service restart / swarm rebuild reattaches to the same branch instead of
// orphaning the member's work. slug is the member-scoped identifier (the swarm
// passes "swarm-<member>"); it is folded to a filesystem/ref-safe form here.
//
// Reattach semantics (idempotent across restart), after `git worktree prune`
// clears admin entries whose directories are gone:
//   - dir + admin entry present   → reuse (already attached)
//   - branch present, dir missing → `git worktree add <path> <branch>` (no -b)
//   - neither                     → `git worktree add -b <branch> <path> HEAD`
//
// The returned session has CreatedBySubagent=false: swarm-member worktrees are
// torn down by the space's own lifecycle (RemoveMember / ResetSpace), never by
// CleanupSubagentWorktree (which keys off that flag).
func ProvisionMemberWorktree(ctx context.Context, rootWorkdir, slug string) (WorktreeSession, error) {
	if rootWorkdir == "" {
		return WorktreeSession{}, errors.New("root workdir is empty")
	}
	repoRoot, err := gitTopLevel(ctx, rootWorkdir)
	if err != nil {
		return WorktreeSession{}, fmt.Errorf("not in a git repository: %w", err)
	}
	flat := sanitizeForSlug(slug)
	if flat == "" {
		return WorktreeSession{}, fmt.Errorf("empty worktree slug for %q", slug)
	}
	worktreePath := worktreeDirFor(repoRoot, flat)
	branch := branchNameFor(flat)

	// Prune first so "registered" below implies "still on disk" — a worktree
	// whose dir was deleted out from under git leaves a stale admin entry that
	// would make `git worktree add` fail with "already registered".
	_, _ = runGit(ctx, repoRoot, "worktree", "prune")

	session := WorktreeSession{
		OriginalWorkdir: rootWorkdir,
		Path:            worktreePath,
		Branch:          branch,
		Slug:            flat,
		RepoRoot:        repoRoot,
		CreatedAt:       time.Now(),
	}

	registered := worktreeRegistered(ctx, repoRoot, worktreePath)
	_, statErr := os.Stat(worktreePath)
	dirExists := statErr == nil

	switch {
	case registered && dirExists:
		return session, nil // already attached — reuse as-is
	case registered && !dirExists:
		// Stale admin entry prune didn't catch — clear it so the re-add below
		// starts clean.
		_, _ = runGit(ctx, repoRoot, "worktree", "remove", "--force", worktreePath)
	case dirExists && !registered:
		// A stray directory occupies the path but git doesn't know it — refuse
		// rather than clobber (git worktree add would fail confusingly).
		return WorktreeSession{}, fmt.Errorf("worktree path %s exists but is not a registered worktree — remove it and retry", worktreePath)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return WorktreeSession{}, fmt.Errorf("create worktree parent dir: %w", err)
	}

	if gitBranchExists(ctx, repoRoot, branch) {
		// The branch survived a previous run; re-add its worktree WITHOUT -b so
		// the member reattaches to its accumulated history.
		if out, gErr := runGit(ctx, repoRoot, "worktree", "add", worktreePath, branch); gErr != nil {
			return WorktreeSession{}, fmt.Errorf("git worktree add (reattach): %v: %s", gErr, out)
		}
		return session, nil
	}

	// Fresh: new branch off HEAD.
	if out, gErr := runGit(ctx, repoRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD"); gErr != nil {
		return WorktreeSession{}, fmt.Errorf("git worktree add: %v: %s", gErr, out)
	}
	return session, nil
}

// PreflightWorktreeRepo reports whether rootWorkdir can host member worktrees
// at all: it must be inside a git repository AND that repository must have at
// least one commit (a worktree is created from HEAD, so an empty repo cannot
// host one). Callers use it to fail a swarm register up front with a targeted
// message rather than failing member-by-member deep inside construction.
func PreflightWorktreeRepo(ctx context.Context, rootWorkdir string) error {
	if _, err := gitTopLevel(ctx, rootWorkdir); err != nil {
		return errors.New("not a git repository")
	}
	if _, err := runGit(ctx, rootWorkdir, "rev-parse", "--verify", "HEAD"); err != nil {
		return errors.New("the repository has no commits yet — a worktree is created from HEAD")
	}
	return nil
}

// MergeReport is the structured outcome of MergeBranch. The swarm's
// worktree_merge tool and the exit_worktree merge action both format it into
// their own model-facing message; keeping the git result structured (rather
// than a pre-formatted string) is what lets the two share one merge core.
type MergeReport struct {
	// BaseBranch is the branch the merge targeted (the base checkout's HEAD).
	BaseBranch string
	// Ahead is the number of commits integrated (0 on a no-op).
	Ahead int
	// FilesChanged is the number of files the merge brought in.
	FilesChanged int
	// Conflicts is non-empty when the merge hit a conflict: it was aborted, the
	// base was left clean, and nothing was applied. These are the conflicted
	// paths for the caller to hand back to the worker.
	Conflicts []string
	// NoOp is true when there was nothing to integrate (base untouched).
	NoOp bool
}

// MergeBranch integrates childBranch into the current branch of the base
// checkout at baseDir (the primary worktree), under the abort-on-conflict
// contract: it refuses a dirty base, refuses an unclean source worktree,
// no-ops when there is nothing to integrate, merges `--no-ff` otherwise, and on
// conflict runs `git merge --abort` and returns the conflicted paths — the base
// branch is NEVER left half-merged. Unlike the exit_worktree tool it does NOT
// tear the child worktree down: the base tip advances, the child worktree and
// its branch stay (the swarm reuses them for the member's next task).
//
// A conflict is reported via MergeReport.Conflicts with a nil error (an
// actionable outcome, not a failure). A dirty base, an unclean source, or a
// genuine git error returns a non-nil error; callers add their own tool prefix.
func MergeBranch(ctx context.Context, baseDir, childBranch string) (MergeReport, error) {
	baseBranchRaw, err := runGit(ctx, baseDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return MergeReport{}, fmt.Errorf("cannot resolve base branch: %w", err)
	}
	baseBranch := strings.TrimSpace(baseBranchRaw)
	report := MergeReport{BaseBranch: baseBranch}

	// A dirty base makes `git merge` fail confusingly — refuse early with a
	// clear message. Only tracked modifications block; untracked files (notably
	// the .evva/worktrees/ dirs that live inside the repo) do not.
	if baseStatus, sErr := runGit(ctx, baseDir, "status", "--porcelain", "--untracked-files=no"); sErr != nil {
		return report, fmt.Errorf("cannot inspect base checkout: %w", sErr)
	} else if strings.TrimSpace(baseStatus) != "" {
		return report, fmt.Errorf("base branch %q has uncommitted changes at %s — commit or stash them before merging", baseBranch, baseDir)
	}

	// Refuse an unclean source — only UNCOMMITTED work blocks a merge (committed
	// work is exactly what we integrate). A branch with no live worktree has
	// nothing uncommitted to inspect, so skip the check there.
	if childPath := worktreePathForBranch(ctx, baseDir, childBranch); childPath != "" {
		if uncommitted, uErr := worktreeUncommitted(ctx, childPath); uErr != nil {
			return report, fmt.Errorf("cannot inspect worktree on branch %s: %w", childBranch, uErr)
		} else if uncommitted > 0 {
			return report, fmt.Errorf("worktree on branch %q has %d uncommitted change(s) — the worker must commit before its work can be merged", childBranch, uncommitted)
		}
	}

	// Nothing to integrate → no-op, never an error, leaves everything untouched.
	ahead, _, err := aheadBehind(ctx, baseDir, baseBranch, childBranch)
	if err != nil {
		return report, fmt.Errorf("cannot count commits to integrate: %w", err)
	}
	if ahead == 0 {
		report.NoOp = true
		return report, nil
	}

	// Capture the file set this merge will bring in, for the report.
	filesOut, _ := runGit(ctx, baseDir, "diff", "--name-only", baseBranch+"..."+childBranch)
	report.FilesChanged = countNonEmptyLines(filesOut)

	mergeOut, mergeErr := runGit(ctx, baseDir, "merge", "--no-ff", "-m",
		fmt.Sprintf("Merge %s into %s", childBranch, baseBranch), childBranch)
	if mergeErr != nil {
		// Capture conflicts, abort, leave the base clean and the worktree intact.
		conflicts, _ := runGit(ctx, baseDir, "diff", "--name-only", "--diff-filter=U")
		_, _ = runGit(ctx, baseDir, "merge", "--abort")
		conflictList := splitNonEmptyLines(conflicts)
		if len(conflictList) == 0 {
			// A merge failure with no conflicted paths is a genuine error, not a
			// content conflict (e.g. a git invocation problem).
			return report, fmt.Errorf("merge of %q failed (aborted, base intact): %s", childBranch, strings.TrimSpace(mergeOut))
		}
		report.Conflicts = conflictList
		return report, nil
	}

	report.Ahead = ahead
	return report, nil
}

// RefreshWorktree fast-forwards the worktree at wtPath onto baseBranch — the D4
// drift control the swarm runs after a successful merge so the just-merged
// member's branch tracks the new base tip. Refuses a dirty worktree (never
// auto-resets uncommitted work) and is a no-op when already up to date.
// `--ff-only` means it can only ever advance a branch that is strictly behind
// base (exactly the post-merge state); it never creates a merge commit.
func RefreshWorktree(ctx context.Context, wtPath, baseBranch string) error {
	if uncommitted, err := worktreeUncommitted(ctx, wtPath); err != nil {
		return fmt.Errorf("cannot inspect worktree at %s: %w", wtPath, err)
	} else if uncommitted > 0 {
		return fmt.Errorf("worktree at %s has uncommitted changes — skipping refresh", wtPath)
	}
	if out, err := runGit(ctx, wtPath, "merge", "--ff-only", baseBranch); err != nil {
		return fmt.Errorf("ff-only merge of %s: %v: %s", baseBranch, err, strings.TrimSpace(out))
	}
	return nil
}

// --- small helpers ----------------------------------------------------------

// gitBranchExists reports whether refs/heads/<branch> exists in the repo.
func gitBranchExists(ctx context.Context, repoRoot, branch string) bool {
	_, err := runGit(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// worktreeRegistered reports whether git currently tracks a worktree at path.
// Paths are compared through filepath.Clean because `git worktree list` emits
// forward slashes even on Windows while worktreeDirFor builds OS-native paths
// (the worktree_list.go cross-platform lesson).
func worktreeRegistered(ctx context.Context, fromDir, path string) bool {
	entries, err := parseWorktreeList(ctx, fromDir)
	if err != nil {
		return false
	}
	target := filepath.Clean(path)
	for _, e := range entries {
		if filepath.Clean(e.Path) == target {
			return true
		}
	}
	return false
}

// worktreePathForBranch returns the on-disk path of the live worktree checked
// out on branch, or "" when no worktree currently has it. fromDir may be any
// worktree in the repo (the list is repo-wide).
func worktreePathForBranch(ctx context.Context, fromDir, branch string) string {
	entries, err := parseWorktreeList(ctx, fromDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Branch == branch {
			return e.Path
		}
	}
	return ""
}

// splitNonEmptyLines splits s on newlines and returns the trimmed, non-empty
// lines (the conflicted-path list for a MergeReport).
func splitNonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
