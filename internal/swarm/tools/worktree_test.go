package tools

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/skill"
)

// SWT-4: the leader's worktree_merge tool. Each case drives the real tool
// against a real git repo through a real space, so the model-facing text is
// asserted alongside the git-level outcome.

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// worktreeSpace is realSpace with worktree isolation on and a git repo at the
// workdir, so worker-a and worker-b each own a branch.
func worktreeSpace(t *testing.T) *swarm.SwarmSpace {
	t.Helper()
	cfg := stubCfg(t)
	resolved, err := filepath.EvalSymlinks(cfg.WorkDir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	cfg.WorkDir = resolved

	gitT(t, cfg.WorkDir, "init", "-q", "-b", "main")
	gitT(t, cfg.WorkDir, "config", "user.email", "test@example.com")
	gitT(t, cfg.WorkDir, "config", "user.name", "test")
	gitT(t, cfg.WorkDir, "config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(cfg.WorkDir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitT(t, cfg.WorkDir, "add", "README")
	gitT(t, cfg.WorkDir, "commit", "-q", "-m", "init")

	loaded := []agentdef.Loaded{
		{Def: agent.AgentDefinition{Name: "leader", SystemPrompt: "You are leader.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleLeader},
		{Def: agent.AgentDefinition{Name: "worker-a", SystemPrompt: "You are worker-a.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
		{Def: agent.AgentDefinition{Name: "worker-b", SystemPrompt: "You are worker-b.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
	}
	m := agentdef.Manifest{Name: "coders", Settings: agentdef.Settings{
		PermissionMode: "bypass", MaxIterations: 5, WorktreeIsolation: true,
	}}
	sp, err := swarm.NewSpace("wt", m, loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	t.Cleanup(sp.Shutdown)
	return sp
}

// workerCommit writes file=content in a member's worktree and commits it.
func workerCommit(t *testing.T, sp *swarm.SwarmSpace, member, file, content string) {
	t.Helper()
	dir := memberWorktreePath(t, sp, member)
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	gitT(t, dir, "add", file)
	gitT(t, dir, "commit", "-q", "-m", "work on "+file)
}

func memberWorktreePath(t *testing.T, sp *swarm.SwarmSpace, member string) string {
	t.Helper()
	return filepath.Join(sp.Workdir, ".evva", "worktrees", "swarm-"+member)
}

func TestWorktreeMerge_Clean(t *testing.T) {
	sp := worktreeSpace(t)
	workerCommit(t, sp, "worker-a", "a.txt", "from a\n")

	res := exec(t, newWorktreeMerge(leaderMC(sp)), `{"member":"worker-a","task_id":7}`)
	if res.IsError {
		t.Fatalf("clean merge should succeed: %s", res.Content)
	}
	for _, want := range []string{"Merged", "worktree-swarm-worker-a", "task #7", "1 commit(s)"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("result missing %q:\n%s", want, res.Content)
		}
	}
	// The work is on the base checkout.
	if b, err := os.ReadFile(filepath.Join(sp.Workdir, "a.txt")); err != nil || string(b) != "from a\n" {
		t.Errorf("merged file on base: content %q err %v", b, err)
	}
	// D4: the member's branch was fast-forwarded onto the new base tip, and
	// its worktree survives for the next task.
	wt := memberWorktreePath(t, sp, "worker-a")
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("the member's worktree must survive a merge: %v", err)
	}
	baseTip := strings.TrimSpace(gitT(t, sp.Workdir, "rev-parse", "main"))
	wtTip := strings.TrimSpace(gitT(t, wt, "rev-parse", "HEAD"))
	if baseTip != wtTip {
		t.Errorf("member branch should track the new base tip after merge: %q vs %q", wtTip, baseTip)
	}
}

func TestWorktreeMerge_Conflict(t *testing.T) {
	sp := worktreeSpace(t)
	workerCommit(t, sp, "worker-a", "README", "worker a version\n")
	// The base moves in an incompatible way (e.g. another member already
	// merged, or the operator committed).
	if err := os.WriteFile(filepath.Join(sp.Workdir, "README"), []byte("base version\n"), 0o644); err != nil {
		t.Fatalf("write base README: %v", err)
	}
	gitT(t, sp.Workdir, "commit", "-qam", "base moves")

	res := exec(t, newWorktreeMerge(leaderMC(sp)), `{"member":"worker-a"}`)
	// A conflict is an actionable outcome, not a tool failure — the leader
	// must be able to read it and act.
	if res.IsError {
		t.Fatalf("a conflict should not be a tool error: %s", res.Content)
	}
	for _, want := range []string{"MERGE CONFLICT", "README", "task_verify", "approve:false", "resolve in your own worktree"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("conflict result missing %q:\n%s", want, res.Content)
		}
	}
	// Aborted clean: base untouched, no merge in progress, worktree intact.
	if b, _ := os.ReadFile(filepath.Join(sp.Workdir, "README")); string(b) != "base version\n" {
		t.Errorf("base content must be untouched, got %q", b)
	}
	if _, err := os.Stat(filepath.Join(sp.Workdir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Errorf("merge should have been aborted; MERGE_HEAD stat err = %v", err)
	}
	if status := gitT(t, sp.Workdir, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(status) != "" {
		t.Errorf("base should be clean after abort, got %q", status)
	}
	if _, err := os.Stat(memberWorktreePath(t, sp, "worker-a")); err != nil {
		t.Errorf("worktree must stay intact so the worker can resolve: %v", err)
	}
}

func TestWorktreeMerge_NoOp(t *testing.T) {
	sp := worktreeSpace(t)
	// worker-a reported done without committing.
	res := exec(t, newWorktreeMerge(leaderMC(sp)), `{"member":"worker-a"}`)
	if res.IsError {
		t.Fatalf("a no-op should not be an error: %s", res.Content)
	}
	for _, want := range []string{"Nothing to integrate", "has not", "committed", "bounce the task back"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("no-op result missing %q:\n%s", want, res.Content)
		}
	}
}

func TestWorktreeMerge_DirtyBaseRefused(t *testing.T) {
	sp := worktreeSpace(t)
	workerCommit(t, sp, "worker-a", "a.txt", "from a\n")
	// The operator left uncommitted edits on the base checkout.
	if err := os.WriteFile(filepath.Join(sp.Workdir, "README"), []byte("operator edit\n"), 0o644); err != nil {
		t.Fatalf("dirty the base: %v", err)
	}

	res := exec(t, newWorktreeMerge(leaderMC(sp)), `{"member":"worker-a"}`)
	if !res.IsError {
		t.Fatalf("a dirty base should refuse: %s", res.Content)
	}
	if !strings.Contains(res.Content, "uncommitted changes") {
		t.Errorf("refusal should name the cause:\n%s", res.Content)
	}
}

func TestWorktreeMerge_UncommittedSourceRefused(t *testing.T) {
	sp := worktreeSpace(t)
	workerCommit(t, sp, "worker-a", "a.txt", "committed\n")
	// ... and then kept working.
	wt := memberWorktreePath(t, sp, "worker-a")
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write wip: %v", err)
	}
	gitT(t, wt, "add", "b.txt")

	res := exec(t, newWorktreeMerge(leaderMC(sp)), `{"member":"worker-a"}`)
	if !res.IsError {
		t.Fatalf("an unclean source should refuse: %s", res.Content)
	}
	if !strings.Contains(res.Content, "uncommitted") {
		t.Errorf("refusal should name the cause:\n%s", res.Content)
	}
}

func TestWorktreeMerge_Rejects(t *testing.T) {
	sp := worktreeSpace(t)
	tool := newWorktreeMerge(leaderMC(sp))

	if res := exec(t, tool, `{"member":"nobody"}`); !res.IsError {
		t.Error("unknown member should error")
	} else if !strings.Contains(res.Content, "no such member") || !strings.Contains(res.Content, "worker-a") {
		t.Errorf("unknown-member error should list the isolated roster:\n%s", res.Content)
	}
	if res := exec(t, tool, `{"member":""}`); !res.IsError {
		t.Error("empty member should error")
	}
	if res := exec(t, tool, `{`); !res.IsError {
		t.Error("malformed input should error")
	}
}

// A member on the shared workdir has no branch to merge — say so plainly
// rather than failing obscurely.
func TestWorktreeMerge_NonIsolatedMember(t *testing.T) {
	sp := realSpace(t) // isolation off entirely
	res := exec(t, newWorktreeMerge(leaderMC(sp)), `{"member":"worker-a"}`)
	if !res.IsError {
		t.Fatalf("merging a non-isolated member should error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "does not run under worktree isolation") {
		t.Errorf("error should explain why:\n%s", res.Content)
	}
}

// Two workers editing the same file concurrently is the scenario the wave
// exists for: the first merge lands, the second conflicts, and the base is
// never left half-applied.
func TestWorktreeMerge_TwoWorkersSameFile(t *testing.T) {
	sp := worktreeSpace(t)
	workerCommit(t, sp, "worker-a", "shared.txt", "a's version\n")
	workerCommit(t, sp, "worker-b", "shared.txt", "b's version\n")

	tool := newWorktreeMerge(leaderMC(sp))
	if res := exec(t, tool, `{"member":"worker-a"}`); res.IsError || !strings.Contains(res.Content, "Merged") {
		t.Fatalf("first merge should land cleanly: %s", res.Content)
	}
	res := exec(t, tool, `{"member":"worker-b"}`)
	if !strings.Contains(res.Content, "MERGE CONFLICT") {
		t.Fatalf("second merge should conflict: %s", res.Content)
	}
	// a's work survived; nothing half-applied.
	if b, _ := os.ReadFile(filepath.Join(sp.Workdir, "shared.txt")); string(b) != "a's version\n" {
		t.Errorf("base should still hold a's merged work, got %q", b)
	}
	if status := gitT(t, sp.Workdir, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(status) != "" {
		t.Errorf("base should be clean, got %q", status)
	}
}
