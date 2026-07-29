package mode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the reusable worktree core (worktree_core.go) that the swarm's
// worktree-isolation wave builds on. They exercise the package-level functions
// directly — no controller, no session, no tool plumbing — because that is
// exactly the surface internal/swarm consumes.
//
// Fixtures reuse newFakeRepo (worktree_test.go), which already carries the
// cross-platform trio: repo-local git identity, core.autocrlf=false, and an
// EvalSymlinks'd temp dir.

// provisionT provisions a member worktree and fails the test on error.
func provisionT(t *testing.T, repo, slug string) WorktreeSession {
	t.Helper()
	sess, err := ProvisionMemberWorktree(context.Background(), repo, slug)
	if err != nil {
		t.Fatalf("ProvisionMemberWorktree(%q): %v", slug, err)
	}
	return sess
}

// commitInT writes file=content inside dir and commits it.
func commitInT(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s in %s: %v", file, dir, err)
	}
	gitRunT(t, dir, "add", file)
	gitRunT(t, dir, "commit", "-q", "-m", "commit "+file)
}

// samePath compares two paths the cross-platform way: `git worktree list`
// emits forward slashes even on Windows while filepath.Join builds OS-native
// separators (the worktree_list.go lesson).
func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

// --- ProvisionMemberWorktree: the reattach matrix -----------------------

func TestProvisionMemberWorktree_FreshIsDeterministic(t *testing.T) {
	repo := newFakeRepo(t)

	sess := provisionT(t, repo, "swarm-qa")

	wantPath := worktreeDirFor(repo, "swarm-qa")
	if !samePath(sess.Path, wantPath) {
		t.Errorf("session.Path: got %q want %q", sess.Path, wantPath)
	}
	if want := "worktree-swarm-qa"; sess.Branch != want {
		t.Errorf("session.Branch: got %q want %q", sess.Branch, want)
	}
	if sess.OriginalWorkdir != repo {
		t.Errorf("session.OriginalWorkdir: got %q want %q", sess.OriginalWorkdir, repo)
	}
	// Swarm worktrees are owned by the space lifecycle, never by
	// CleanupSubagentWorktree — which keys off this flag.
	if sess.CreatedBySubagent {
		t.Error("CreatedBySubagent should be false for a swarm member worktree")
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Errorf("worktree dir should exist: %v", err)
	}
	if !gitBranchExists(context.Background(), repo, sess.Branch) {
		t.Errorf("branch %q should exist", sess.Branch)
	}

	// The defining difference from CreateForSubagent: no random suffix, so a
	// second process/restart lands on the identical name.
	again := provisionT(t, repo, "swarm-qa")
	if !samePath(again.Path, sess.Path) || again.Branch != sess.Branch {
		t.Errorf("provision is not deterministic: first %q/%q, second %q/%q",
			sess.Path, sess.Branch, again.Path, again.Branch)
	}
}

func TestProvisionMemberWorktree_ReuseKeepsWork(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "work.txt", "in progress\n")

	// dir + admin entry both present → reuse as-is, nothing recreated.
	again := provisionT(t, repo, "swarm-qa")

	if !samePath(again.Path, sess.Path) {
		t.Fatalf("reattach moved the worktree: got %q want %q", again.Path, sess.Path)
	}
	got, err := os.ReadFile(filepath.Join(again.Path, "work.txt"))
	if err != nil {
		t.Fatalf("uncommitted-to-base work should survive reuse: %v", err)
	}
	if string(got) != "in progress\n" {
		t.Errorf("work.txt content: got %q want %q", got, "in progress\n")
	}
}

func TestProvisionMemberWorktree_ReattachBranchOnly(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "work.txt", "committed\n")

	// `git worktree remove` drops the dir + admin entry but keeps the branch —
	// the RemoveMember-then-re-add shape.
	gitRunT(t, repo, "worktree", "remove", "--force", sess.Path)
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Fatalf("precondition: worktree dir should be gone, stat err = %v", err)
	}
	if !gitBranchExists(context.Background(), repo, sess.Branch) {
		t.Fatal("precondition: branch should survive worktree remove")
	}

	again := provisionT(t, repo, "swarm-qa")

	if again.Branch != sess.Branch {
		t.Errorf("reattached to the wrong branch: got %q want %q", again.Branch, sess.Branch)
	}
	// Re-added WITHOUT -b, so the member's accumulated history is still there.
	got, err := os.ReadFile(filepath.Join(again.Path, "work.txt"))
	if err != nil {
		t.Fatalf("committed history should be restored on reattach: %v", err)
	}
	if string(got) != "committed\n" {
		t.Errorf("work.txt content: got %q want %q", got, "committed\n")
	}
}

func TestProvisionMemberWorktree_StaleAdminEntry(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "work.txt", "committed\n")

	// Delete the directory out from under git — the admin entry survives and
	// would make a plain `git worktree add` fail with "already registered".
	// This is the crash/rm -rf shape a restart has to tolerate.
	if err := os.RemoveAll(sess.Path); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	again := provisionT(t, repo, "swarm-qa")

	if again.Branch != sess.Branch {
		t.Errorf("branch: got %q want %q", again.Branch, sess.Branch)
	}
	got, err := os.ReadFile(filepath.Join(again.Path, "work.txt"))
	if err != nil {
		t.Fatalf("history should survive a stale admin entry: %v", err)
	}
	if string(got) != "committed\n" {
		t.Errorf("work.txt content: got %q want %q", got, "committed\n")
	}
}

func TestProvisionMemberWorktree_StrayDirectoryRefused(t *testing.T) {
	repo := newFakeRepo(t)

	// Something that is not a worktree occupies the target path. Clobbering it
	// could destroy operator data, so provision must refuse.
	stray := worktreeDirFor(repo, "swarm-qa")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatalf("mkdir stray: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stray, "precious.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	_, err := ProvisionMemberWorktree(context.Background(), repo, "swarm-qa")
	if err == nil {
		t.Fatal("expected refusal when a stray directory occupies the worktree path")
	}
	if !strings.Contains(err.Error(), "not a registered worktree") {
		t.Errorf("error should explain the stray dir; got %q", err)
	}
	if _, sErr := os.Stat(filepath.Join(stray, "precious.txt")); sErr != nil {
		t.Errorf("refusal must not clobber the existing directory: %v", sErr)
	}
}

func TestProvisionMemberWorktree_Rejects(t *testing.T) {
	t.Run("non-repo", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := ProvisionMemberWorktree(context.Background(), dir, "swarm-qa"); err == nil {
			t.Fatal("expected an error outside a git repository")
		} else if !strings.Contains(err.Error(), "not in a git repository") {
			t.Errorf("error should name the cause; got %q", err)
		}
	})

	t.Run("empty workdir", func(t *testing.T) {
		if _, err := ProvisionMemberWorktree(context.Background(), "", "swarm-qa"); err == nil {
			t.Fatal("expected an error for an empty root workdir")
		}
	})

	t.Run("slug sanitizes to nothing", func(t *testing.T) {
		repo := newFakeRepo(t)
		if _, err := ProvisionMemberWorktree(context.Background(), repo, "///"); err == nil {
			t.Fatal("expected an error for a slug that sanitizes to empty")
		}
	})
}

// --- MergeBranch: the abort-on-conflict contract ------------------------

func TestMergeBranch_Clean(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "a.txt", "from worker\n")

	report, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	if report.NoOp || len(report.Conflicts) > 0 {
		t.Fatalf("expected a clean merge; got %+v", report)
	}
	if report.BaseBranch != "main" {
		t.Errorf("BaseBranch: got %q want %q", report.BaseBranch, "main")
	}
	if report.Ahead != 1 {
		t.Errorf("Ahead: got %d want 1", report.Ahead)
	}
	if report.FilesChanged != 1 {
		t.Errorf("FilesChanged: got %d want 1", report.FilesChanged)
	}
	if got, rErr := os.ReadFile(filepath.Join(repo, "a.txt")); rErr != nil {
		t.Errorf("merged file should be on the base checkout: %v", rErr)
	} else if string(got) != "from worker\n" {
		t.Errorf("a.txt on base: got %q want %q", got, "from worker\n")
	}

	// The contract that separates MergeBranch from exit_worktree's merge
	// action: the child worktree survives, ready for the member's next task.
	if _, sErr := os.Stat(sess.Path); sErr != nil {
		t.Errorf("MergeBranch must NOT tear the worktree down: %v", sErr)
	}
	if !gitBranchExists(context.Background(), repo, sess.Branch) {
		t.Error("MergeBranch must not delete the member's branch")
	}
}

func TestMergeBranch_Conflict(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")

	// Both sides edit README differently → guaranteed conflict.
	commitInT(t, sess.Path, "README", "worker version\n")
	commitInT(t, repo, "README", "base version\n")

	report, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err != nil {
		t.Fatalf("a conflict is an outcome, not an error: %v", err)
	}

	if len(report.Conflicts) != 1 || report.Conflicts[0] != "README" {
		t.Errorf("Conflicts: got %v want [README]", report.Conflicts)
	}
	if report.NoOp {
		t.Error("NoOp should be false on a conflict")
	}

	// Aborted clean: no merge in progress, base content byte-identical.
	if _, sErr := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); !os.IsNotExist(sErr) {
		t.Errorf("merge should have been aborted; MERGE_HEAD stat err = %v", sErr)
	}
	if got, rErr := os.ReadFile(filepath.Join(repo, "README")); rErr != nil {
		t.Errorf("read base README: %v", rErr)
	} else if string(got) != "base version\n" {
		t.Errorf("base content must be untouched: got %q want %q", got, "base version\n")
	}
	if status := gitRunT(t, repo, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(status) != "" {
		t.Errorf("base checkout should be clean after abort; got %q", status)
	}
	// The worker keeps its worktree so it can resolve on its own side.
	if _, sErr := os.Stat(sess.Path); sErr != nil {
		t.Errorf("worktree should be intact after a conflict: %v", sErr)
	}
}

func TestMergeBranch_NoOp(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	baseTip := strings.TrimSpace(gitRunT(t, repo, "rev-parse", "HEAD"))

	// Nothing committed on the member's branch — the leader's tell that the
	// worker reported done without committing.
	report, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err != nil {
		t.Fatalf("a no-op must never error: %v", err)
	}

	if !report.NoOp {
		t.Errorf("expected NoOp; got %+v", report)
	}
	if report.Ahead != 0 {
		t.Errorf("Ahead: got %d want 0", report.Ahead)
	}
	if now := strings.TrimSpace(gitRunT(t, repo, "rev-parse", "HEAD")); now != baseTip {
		t.Errorf("base tip moved on a no-op: %q → %q", baseTip, now)
	}
}

func TestMergeBranch_RefusesUncleanSource(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "a.txt", "committed\n")
	// Uncommitted work on top — the worker isn't done.
	if err := os.WriteFile(filepath.Join(sess.Path, "b.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	gitRunT(t, sess.Path, "add", "b.txt")

	_, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err == nil {
		t.Fatal("expected a refusal for an unclean source worktree")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error should name the cause; got %q", err)
	}
}

func TestMergeBranch_RefusesDirtyBase(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "a.txt", "from worker\n")

	// Operator edited a tracked file on the base checkout mid-swarm.
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("operator edit\n"), 0o644); err != nil {
		t.Fatalf("dirty the base: %v", err)
	}

	_, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err == nil {
		t.Fatal("expected a refusal for a dirty base checkout")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("error should name the cause; got %q", err)
	}
}

func TestMergeBranch_UntrackedBaseFilesDoNotBlock(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "a.txt", "from worker\n")

	// The .evva/worktrees/ dirs themselves live untracked inside the repo, so
	// untracked content must never block a merge.
	if err := os.WriteFile(filepath.Join(repo, "scratch.log"), []byte("noise\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	report, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err != nil {
		t.Fatalf("untracked base files must not block a merge: %v", err)
	}
	if report.Ahead != 1 {
		t.Errorf("Ahead: got %d want 1", report.Ahead)
	}
}

func TestMergeBranch_BranchWithoutLiveWorktree(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "a.txt", "from worker\n")

	// The RemoveMember shape: the worktree is gone, the branch still holds the
	// member's committed work. There is no working tree to inspect for
	// uncommitted changes, so that guard must be skipped rather than error.
	gitRunT(t, repo, "worktree", "remove", "--force", sess.Path)

	report, err := MergeBranch(context.Background(), repo, sess.Branch)
	if err != nil {
		t.Fatalf("merging a branch with no live worktree: %v", err)
	}
	if report.Ahead != 1 || len(report.Conflicts) > 0 {
		t.Fatalf("expected a clean merge; got %+v", report)
	}
	if got, rErr := os.ReadFile(filepath.Join(repo, "a.txt")); rErr != nil {
		t.Errorf("merged file should be on base: %v", rErr)
	} else if string(got) != "from worker\n" {
		t.Errorf("a.txt: got %q want %q", got, "from worker\n")
	}
}

// --- RefreshWorktree: D4 drift control ----------------------------------

func TestRefreshWorktree_FastForwards(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, sess.Path, "a.txt", "from worker\n")

	// Leader merges the member, advancing base past the member's branch.
	if _, err := MergeBranch(context.Background(), repo, sess.Branch); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	commitInT(t, repo, "base-only.txt", "base moved on\n")

	if err := RefreshWorktree(context.Background(), sess.Path, "main"); err != nil {
		t.Fatalf("RefreshWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sess.Path, "base-only.txt")); err != nil {
		t.Errorf("worktree should have fast-forwarded onto the new base tip: %v", err)
	}
	// ff-only never fabricates a merge commit.
	baseTip := strings.TrimSpace(gitRunT(t, repo, "rev-parse", "main"))
	wtTip := strings.TrimSpace(gitRunT(t, sess.Path, "rev-parse", "HEAD"))
	if baseTip != wtTip {
		t.Errorf("worktree tip should equal base tip after ff: %q vs %q", wtTip, baseTip)
	}
}

func TestRefreshWorktree_RefusesDirty(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")
	commitInT(t, repo, "base-only.txt", "base moved on\n")

	// Uncommitted work in the member's worktree — never auto-reset it.
	if err := os.WriteFile(filepath.Join(sess.Path, "wip.txt"), []byte("in flight\n"), 0o644); err != nil {
		t.Fatalf("write wip.txt: %v", err)
	}
	gitRunT(t, sess.Path, "add", "wip.txt")

	err := RefreshWorktree(context.Background(), sess.Path, "main")
	if err == nil {
		t.Fatal("expected a refusal for a dirty worktree")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error should name the cause; got %q", err)
	}
	if got, rErr := os.ReadFile(filepath.Join(sess.Path, "wip.txt")); rErr != nil || string(got) != "in flight\n" {
		t.Errorf("in-flight work must be untouched: content %q err %v", got, rErr)
	}
}

func TestRefreshWorktree_UpToDateIsNoOp(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")

	if err := RefreshWorktree(context.Background(), sess.Path, "main"); err != nil {
		t.Fatalf("refreshing an already-current worktree should succeed: %v", err)
	}
}

func TestRefreshWorktree_DivergedFails(t *testing.T) {
	repo := newFakeRepo(t)
	sess := provisionT(t, repo, "swarm-qa")

	// Both sides move → not a fast-forward. ff-only must refuse rather than
	// create a merge commit in the member's worktree behind its back.
	commitInT(t, sess.Path, "worker.txt", "worker moved\n")
	commitInT(t, repo, "base.txt", "base moved\n")

	err := RefreshWorktree(context.Background(), sess.Path, "main")
	if err == nil {
		t.Fatal("expected ff-only to refuse a diverged branch")
	}
	if _, sErr := os.Stat(filepath.Join(sess.Path, "base.txt")); !os.IsNotExist(sErr) {
		t.Errorf("nothing should have been merged into the worktree; stat err = %v", sErr)
	}
}
