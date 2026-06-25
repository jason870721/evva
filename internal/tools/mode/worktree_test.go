package mode

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/tools"
)

// --- slug / branch helpers --------------------------------------------

func TestValidateSlug_Accepts(t *testing.T) {
	for _, in := range []string{
		"demo",
		"feature/x",
		"a.b_c-d",
		"nested/sub/segment",
	} {
		if _, err := validateSlug(in); err != nil {
			t.Errorf("validateSlug(%q) unexpected error: %v", in, err)
		}
	}
}

func TestValidateSlug_Rejects(t *testing.T) {
	for _, in := range []string{
		"",         // empty handled upstream but still invalid here
		"/leading", // leading slash
		"trailing/",
		"a//b",  // empty segment
		"a/./b", // forbidden segment
		"a/../b",
		"has space",
		"semi;colon",
		strings.Repeat("x", maxSlugLen+1),
	} {
		if _, err := validateSlug(in); err == nil {
			t.Errorf("validateSlug(%q): expected error, got none", in)
		}
	}
}

func TestFlattenAndBranch(t *testing.T) {
	if got := flattenSlug("feature/x/y"); got != "feature+x+y" {
		t.Errorf("flattenSlug: got %q want feature+x+y", got)
	}
	if got := branchNameFor("feature+x"); got != "worktree-feature+x" {
		t.Errorf("branchNameFor: got %q", got)
	}
	if got := worktreeDirFor("/repo", "demo"); got != filepath.Join("/repo", ".evva", "worktrees", "demo") {
		t.Errorf("worktreeDirFor: got %q", got)
	}
}

// --- fake controller for Execute tests --------------------------------

type fakeWorktreeController struct {
	workdir string
	logger  *slog.Logger
	session atomic.Pointer[WorktreeSession]
	// switchErr forces SwitchWorkdir to fail; used to exercise rollback.
	switchErr error
	switches  []string
}

func (f *fakeWorktreeController) Workdir() string { return f.workdir }
func (f *fakeWorktreeController) SwitchWorkdir(path string) error {
	if f.switchErr != nil {
		return f.switchErr
	}
	f.workdir = path
	f.switches = append(f.switches, path)
	return nil
}
func (f *fakeWorktreeController) WorktreeSession() *WorktreeSession { return f.session.Load() }
func (f *fakeWorktreeController) BeginWorktreeSession(s WorktreeSession) {
	f.session.Store(&s)
}
func (f *fakeWorktreeController) EndWorktreeSession() { f.session.Store(nil) }
func (f *fakeWorktreeController) AgentID() string     { return "test-agent" }
func (f *fakeWorktreeController) Logger() *slog.Logger {
	if f.logger == nil {
		f.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return f.logger
}

func newFakeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// macOS t.TempDir() returns a logical path under /var/folders/…, but
	// `git rev-parse --show-toplevel` resolves /var → /private/var, so any
	// assertion comparing the test's dir against a session.Path derived from
	// git would mismatch. Canonicalize up front so both sides line up.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks on tempdir: %v", err)
	}
	dir = resolved
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		// Make a deterministic identity so `git commit` doesn't bail on a
		// missing user.email in CI sandboxes.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", "-b", "main")
	// Set a REPO-LOCAL identity (not just env on the test's own git calls):
	// the merge action commits via the production runGit helper, which
	// doesn't inject identity env, so on a host with no global git config
	// (e.g. the Windows CI runner) `git merge --no-ff` would fail with
	// "Committer identity unknown". Repo-local config is picked up by every
	// git invocation in this repo, including production code's.
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "test")
	// Pin autocrlf off so git round-trips file content verbatim. The Windows
	// CI runner defaults core.autocrlf=true globally, which rewrites LF→CRLF
	// on checkout (e.g. when `git merge --abort` restores the working tree) —
	// that would defeat byte-exact content assertions like the conflict test's.
	run("git", "config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("git", "add", "README")
	run("git", "commit", "-q", "-m", "init")
	return dir
}

// --- EnterWorktree end-to-end ------------------------------------------

func TestEnterWorktree_HappyPath(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	tool := NewEnterWorktree(func() WorktreeController { return ctrl })

	res, err := tool.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{"name":"demo"}`))
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %q", res.Content)
	}

	wtPath := filepath.Join(repo, permission.WorktreeDirSegment, "demo")
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree path %q should exist: %v", wtPath, err)
	}
	if !strings.Contains(res.Content, "worktree-demo") {
		t.Errorf("result should report branch name; got %q", res.Content)
	}
	sess := ctrl.WorktreeSession()
	if sess == nil {
		t.Fatal("controller should be in a worktree session")
	}
	if sess.Path != wtPath {
		t.Errorf("session.Path: got %q want %q", sess.Path, wtPath)
	}
	if sess.OriginalWorkdir != repo {
		t.Errorf("session.OriginalWorkdir: got %q want %q", sess.OriginalWorkdir, repo)
	}
	if ctrl.workdir != wtPath {
		t.Errorf("controller did not switch workdir: %q", ctrl.workdir)
	}
}

func TestEnterWorktree_RandomSlug(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	tool := NewEnterWorktree(func() WorktreeController { return ctrl })

	res, _ := tool.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{}`))
	if res.IsError {
		t.Fatalf("expected success with random slug; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "wt-") {
		t.Errorf("random slug should start with wt-; got %q", res.Content)
	}
}

func TestEnterWorktree_RejectsWhenAlreadyInSession(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	ctrl.BeginWorktreeSession(WorktreeSession{
		OriginalWorkdir: repo,
		Path:            repo + "/x",
		Branch:          "worktree-x",
		Slug:            "x",
		CreatedAt:       time.Now(),
	})
	tool := NewEnterWorktree(func() WorktreeController { return ctrl })

	res, _ := tool.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{"name":"another"}`))
	if !res.IsError {
		t.Fatal("expected refusal when already in a session")
	}
	if !strings.Contains(res.Content, "already in a worktree session") {
		t.Errorf("expected 'already in a worktree session'; got %q", res.Content)
	}
}

func TestEnterWorktree_RejectsInvalidSlug(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	tool := NewEnterWorktree(func() WorktreeController { return ctrl })

	res, _ := tool.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"name":"has space"}`))
	if !res.IsError || !strings.Contains(res.Content, "invalid characters") {
		t.Errorf("expected invalid-slug error; got %q", res.Content)
	}
}

func TestEnterWorktree_NoController(t *testing.T) {
	tool := NewEnterWorktree(nil)
	res, _ := tool.Execute(context.Background(), slog.Default(), json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(res.Content, "no worktree controller") {
		t.Errorf("expected no-controller error; got %q", res.Content)
	}
}

// --- ExitWorktree -----------------------------------------------------

func TestExitWorktree_NoOpWhenNoSession(t *testing.T) {
	ctrl := &fakeWorktreeController{workdir: "/tmp"}
	tool := NewExitWorktree(func() WorktreeController { return ctrl })

	res, _ := tool.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"action":"keep"}`))
	if res.IsError {
		t.Fatalf("no-op should not be an error; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "no worktree session active") {
		t.Errorf("expected no-op message; got %q", res.Content)
	}
}

func TestExitWorktree_Keep(t *testing.T) {
	repo := newFakeRepo(t)
	// Pre-stage: create the worktree via EnterWorktree so the on-disk
	// state matches what ExitWorktree expects.
	ctrl := &fakeWorktreeController{workdir: repo}
	enter := NewEnterWorktree(func() WorktreeController { return ctrl })
	if res, _ := enter.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{"name":"keepme"}`)); res.IsError {
		t.Fatalf("setup enter failed: %s", res.Content)
	}
	wtPath := ctrl.WorktreeSession().Path

	exit := NewExitWorktree(func() WorktreeController { return ctrl })
	res, _ := exit.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"action":"keep"}`))
	if res.IsError {
		t.Fatalf("exit keep failed: %s", res.Content)
	}
	if ctrl.WorktreeSession() != nil {
		t.Error("session should be cleared after exit")
	}
	if ctrl.workdir != repo {
		t.Errorf("workdir should be restored to %q; got %q", repo, ctrl.workdir)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree should still exist on keep; stat err=%v", err)
	}
}

func TestExitWorktree_RemoveClean(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	enter := NewEnterWorktree(func() WorktreeController { return ctrl })
	if res, _ := enter.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{"name":"goodbye"}`)); res.IsError {
		t.Fatalf("setup enter failed: %s", res.Content)
	}
	wtPath := ctrl.WorktreeSession().Path

	exit := NewExitWorktree(func() WorktreeController { return ctrl })
	res, _ := exit.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"action":"remove"}`))
	if res.IsError {
		t.Fatalf("exit remove failed: %s", res.Content)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone; stat err=%v", err)
	}
}

func TestExitWorktree_RemoveDirtyRefusesWithoutDiscard(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	enter := NewEnterWorktree(func() WorktreeController { return ctrl })
	if res, _ := enter.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{"name":"dirty"}`)); res.IsError {
		t.Fatalf("setup enter failed: %s", res.Content)
	}
	wtPath := ctrl.WorktreeSession().Path

	// Make a change in the worktree.
	if err := os.WriteFile(filepath.Join(wtPath, "scratch"), []byte("noise\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	exit := NewExitWorktree(func() WorktreeController { return ctrl })
	res, _ := exit.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"action":"remove"}`))
	if !res.IsError || !strings.Contains(res.Content, "uncommitted") {
		t.Errorf("expected refusal listing uncommitted changes; got %q", res.Content)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree should still exist after refusal; stat err=%v", err)
	}
}

func TestExitWorktree_RemoveDirtyWithDiscard(t *testing.T) {
	repo := newFakeRepo(t)
	ctrl := &fakeWorktreeController{workdir: repo}
	enter := NewEnterWorktree(func() WorktreeController { return ctrl })
	if res, _ := enter.Execute(context.Background(), ctrl.Logger(), json.RawMessage(`{"name":"discardme"}`)); res.IsError {
		t.Fatalf("setup enter failed: %s", res.Content)
	}
	wtPath := ctrl.WorktreeSession().Path
	if err := os.WriteFile(filepath.Join(wtPath, "scratch"), []byte("noise\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	exit := NewExitWorktree(func() WorktreeController { return ctrl })
	res, _ := exit.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"action":"remove","discard_changes":true}`))
	if res.IsError {
		t.Fatalf("expected success with discard_changes=true; got %q", res.Content)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed when discard_changes=true; stat err=%v", err)
	}
}

func TestExitWorktree_RejectsBadAction(t *testing.T) {
	ctrl := &fakeWorktreeController{workdir: "/tmp"}
	ctrl.BeginWorktreeSession(WorktreeSession{Path: "/tmp/wt", OriginalWorkdir: "/tmp"})
	tool := NewExitWorktree(func() WorktreeController { return ctrl })

	res, _ := tool.Execute(context.Background(), ctrl.Logger(),
		json.RawMessage(`{"action":"burn"}`))
	if !res.IsError || !strings.Contains(res.Content, "action must be") {
		t.Errorf("expected bad-action error; got %q", res.Content)
	}
}

// --- AgentTool isolation helpers --------------------------------------

func TestCreateForSubagent_AndCleanup(t *testing.T) {
	repo := newFakeRepo(t)
	ctx := context.Background()

	sess, err := CreateForSubagent(ctx, repo, "alpha")
	if err != nil {
		t.Fatalf("CreateForSubagent: %v", err)
	}
	if !sess.CreatedBySubagent {
		t.Error("CreatedBySubagent flag should be set")
	}
	if !strings.HasPrefix(sess.Slug, "alpha-") {
		t.Errorf("slug should fold agent name; got %q", sess.Slug)
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Errorf("worktree path should exist; got err=%v", err)
	}

	removed, summary := CleanupSubagentWorktree(ctx, sess, false)
	if !removed {
		t.Errorf("clean worktree should be removed automatically; summary=%q", summary)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone; stat err=%v", err)
	}
}

func TestCleanupSubagentWorktree_PreservesDirty(t *testing.T) {
	repo := newFakeRepo(t)
	ctx := context.Background()

	sess, err := CreateForSubagent(ctx, repo, "beta")
	if err != nil {
		t.Fatalf("CreateForSubagent: %v", err)
	}
	// Dirty the worktree.
	if err := os.WriteFile(filepath.Join(sess.Path, "scratch"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	removed, summary := CleanupSubagentWorktree(ctx, sess, false)
	if removed {
		t.Error("dirty worktree should be preserved when removeAlways=false")
	}
	if !strings.Contains(summary, "uncommitted") {
		t.Errorf("summary should describe uncommitted changes; got %q", summary)
	}

	// Now force-remove for the second pass so test cleanup doesn't leave
	// the worktree in the temp dir's tree (TempDir cleanup handles it
	// either way, but be tidy).
	removed, _ = CleanupSubagentWorktree(ctx, sess, true)
	if !removed {
		t.Error("removeAlways=true should force removal")
	}
}

// Sanity check that the tool surfaces its canonical wire name + a
// non-empty schema/description so registry lookups stay coherent.
func TestWorktreeTools_Metadata(t *testing.T) {
	enter := NewEnterWorktree(nil)
	exit := NewExitWorktree(nil)
	if enter.Name() != string(tools.ENTER_WORKTREE) {
		t.Errorf("enter name drift: %q", enter.Name())
	}
	if exit.Name() != string(tools.EXIT_WORKTREE) {
		t.Errorf("exit name drift: %q", exit.Name())
	}
	if enter.Description() == "" || exit.Description() == "" {
		t.Error("descriptions must be non-empty")
	}
	var probe map[string]any
	if err := json.Unmarshal(enter.Schema(), &probe); err != nil {
		t.Errorf("enter schema invalid JSON: %v", err)
	}
	if err := json.Unmarshal(exit.Schema(), &probe); err != nil {
		t.Errorf("exit schema invalid JSON: %v", err)
	}
}
