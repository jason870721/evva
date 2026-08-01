package agent

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/tools/mode"
)

// finalizeIsolation is the spawn.go helper that bridges the child-agent
// exit into worktree cleanup. The clean-exit path auto-removes the
// worktree and returns the child's resp unchanged; the dirty-exit path
// preserves the worktree and appends its path + branch to resp so the
// parent can surface them to the user.

func makeGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("git", "add", "README")
	run("git", "commit", "-q", "-m", "init")
	return dir
}

func TestFinalizeIsolation_CleanExitRemovesWorktree(t *testing.T) {
	repo := makeGitRepo(t)
	ctx := context.Background()

	sess, err := mode.CreateForSubagent(ctx, repo, "clean")
	if err != nil {
		t.Fatalf("CreateForSubagent: %v", err)
	}

	a := &Agent{logger: slog.Default()}
	resp := finalizeIsolation(ctx, &sess, nil, "child summary", a)

	if resp != "child summary" {
		t.Errorf("clean-exit resp should be unchanged; got %q", resp)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed; stat err=%v", err)
	}
}

func TestFinalizeIsolation_DirtyExitPreservesAndReports(t *testing.T) {
	repo := makeGitRepo(t)
	ctx := context.Background()

	sess, err := mode.CreateForSubagent(ctx, repo, "dirty")
	if err != nil {
		t.Fatalf("CreateForSubagent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.Path, "noise"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write noise: %v", err)
	}

	a := &Agent{logger: slog.Default()}
	resp := finalizeIsolation(ctx, &sess, nil, "child summary", a)

	if !strings.Contains(resp, "child summary") {
		t.Errorf("resp must preserve child summary; got %q", resp)
	}
	if !strings.Contains(resp, "worktree_path:") || !strings.Contains(resp, sess.Path) {
		t.Errorf("dirty-exit resp must include worktree_path=%s; got %q", sess.Path, resp)
	}
	if !strings.Contains(resp, "worktree_branch:") || !strings.Contains(resp, sess.Branch) {
		t.Errorf("dirty-exit resp must include worktree_branch=%s; got %q", sess.Branch, resp)
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Errorf("worktree should still exist on dirty exit; got err=%v", err)
	}
	// Clean up the dirty worktree so it doesn't leak into other tests'
	// expectations (TempDir handles physical cleanup either way).
	mode.CleanupSubagentWorktree(ctx, sess, true)
}

func TestFinalizeIsolation_NilSessionIsNoop(t *testing.T) {
	a := &Agent{logger: slog.Default()}
	resp := finalizeIsolation(context.Background(), nil, nil, "untouched", a)
	if resp != "untouched" {
		t.Errorf("nil session should pass resp through; got %q", resp)
	}
}
