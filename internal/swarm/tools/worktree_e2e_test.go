package tools

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// SWT-7 end-to-end: the whole isolated-coding loop through the REAL tools and
// the REAL ledger — two workers editing the same file, one clean merge, one
// conflict bounced back to its worker, resolved in that worker's own worktree,
// then merged clean. Both changes land on base and the ledger shows two
// completed tasks.
//
// This is the scenario the wave exists for; every earlier ticket is tested in
// isolation, and this is the one that proves they compose.
func TestSwarmWorktreeE2E_ConflictResolvedByWorker(t *testing.T) {
	sp := worktreeSpace(t)
	leader := leaderMC(sp)

	create := newTaskCreate(leader)
	assign := newTaskAssign(leader)
	verify := newTaskVerify(leader)
	merge := newWorktreeMerge(leader)

	// --- the leader plans two tasks on the same file -------------------
	taskA := mustTaskID(t, exec(t, create, `{"title":"add greeting","spec":"edit shared.txt","assignee":"worker-a"}`))
	taskB := mustTaskID(t, exec(t, create, `{"title":"add farewell","spec":"edit shared.txt","assignee":"worker-b"}`))
	for _, id := range []int64{taskA, taskB} {
		if res := exec(t, assign, fmt.Sprintf(`{"task_id":%d}`, id)); res.IsError {
			t.Fatalf("assign #%d: %s", id, res.Content)
		}
	}

	// --- both workers edit the SAME file, concurrently, in isolation ---
	// Neither can see or clobber the other: that is the correctness property.
	workerCommit(t, sp, "worker-a", "shared.txt", "hello from a\n")
	workerCommit(t, sp, "worker-b", "shared.txt", "goodbye from b\n")

	// Each reports done on its own task (the worker's only ledger write).
	for _, w := range []struct {
		name string
		id   int64
	}{{"worker-a", taskA}, {"worker-b", taskB}} {
		res := exec(t, newTaskDone(workerMC(sp, w.name)),
			fmt.Sprintf(`{"task_id":%d,"result":"committed on my branch"}`, w.id))
		if res.IsError {
			t.Fatalf("%s task_done: %s", w.name, res.Content)
		}
	}

	// --- leader integrates A: clean ------------------------------------
	if res := exec(t, merge, fmt.Sprintf(`{"member":"worker-a","task_id":%d}`, taskA)); res.IsError || !strings.Contains(res.Content, "Merged") {
		t.Fatalf("merging worker-a should be clean: %s", res.Content)
	}
	if res := exec(t, verify, fmt.Sprintf(`{"task_id":%d,"approve":true,"note":"merged clean"}`, taskA)); res.IsError {
		t.Fatalf("verify A: %s", res.Content)
	}

	// --- leader integrates B: conflict, bounced back to the worker -----
	res := exec(t, merge, fmt.Sprintf(`{"member":"worker-b","task_id":%d}`, taskB))
	if !strings.Contains(res.Content, "MERGE CONFLICT") {
		t.Fatalf("merging worker-b should conflict: %s", res.Content)
	}
	// The base is never left half-applied: A's work stands, tree is clean.
	if b, _ := os.ReadFile(filepath.Join(sp.Workdir, "shared.txt")); string(b) != "hello from a\n" {
		t.Fatalf("base should hold only A's work after the aborted merge, got %q", b)
	}
	if st := gitT(t, sp.Workdir, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(st) != "" {
		t.Fatalf("base must be clean after abort, got %q", st)
	}
	if res := exec(t, verify, fmt.Sprintf(`{"task_id":%d,"approve":false,"note":"conflict in shared.txt — merge main into your branch, resolve, recommit"}`, taskB)); res.IsError {
		t.Fatalf("reject B: %s", res.Content)
	}
	// verifying → running is a legal transition; the task is back with B.
	if got := taskStatus(t, sp.Store, taskB); got != store.StatusRunning {
		t.Errorf("rejected task should return to running, got %q", got)
	}

	// --- worker B resolves in ITS OWN worktree -------------------------
	// This is the protocol's conflict recipe, executed literally.
	wtB := memberWorktreePath(t, sp, "worker-b")
	if out, err := gitTry(t, wtB, "merge", "main"); err == nil {
		t.Fatalf("merging main into B's branch should conflict first: %s", out)
	}
	if err := os.WriteFile(filepath.Join(wtB, "shared.txt"), []byte("hello from a\ngoodbye from b\n"), 0o644); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gitT(t, wtB, "add", "shared.txt")
	gitT(t, wtB, "commit", "-q", "--no-edit")
	if res := exec(t, newTaskDone(workerMC(sp, "worker-b")), fmt.Sprintf(`{"task_id":%d,"result":"conflict resolved, recommitted"}`, taskB)); res.IsError {
		t.Fatalf("worker-b re-report: %s", res.Content)
	}

	// --- leader integrates B again: clean now --------------------------
	if res := exec(t, merge, fmt.Sprintf(`{"member":"worker-b","task_id":%d}`, taskB)); res.IsError || !strings.Contains(res.Content, "Merged") {
		t.Fatalf("second merge of worker-b should be clean: %s", res.Content)
	}
	if res := exec(t, verify, fmt.Sprintf(`{"task_id":%d,"approve":true,"note":"merged after resolve"}`, taskB)); res.IsError {
		t.Fatalf("verify B: %s", res.Content)
	}

	// --- both changes are on base, ledger shows two completed ----------
	got, err := os.ReadFile(filepath.Join(sp.Workdir, "shared.txt"))
	if err != nil {
		t.Fatalf("read merged file: %v", err)
	}
	for _, want := range []string{"hello from a", "goodbye from b"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("base is missing %q after both merges:\n%s", want, got)
		}
	}
	for _, id := range []int64{taskA, taskB} {
		if s := taskStatus(t, sp.Store, id); s != store.StatusCompleted {
			t.Errorf("task #%d status = %q, want completed", id, s)
		}
	}
	if st := gitT(t, sp.Workdir, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(st) != "" {
		t.Errorf("base checkout should end clean, got %q", st)
	}
}

// mustTaskID pulls the new task's id out of a task_create result
// ("Created task #7 ...").
func mustTaskID(t *testing.T, res pubtools.Result) int64 {
	t.Helper()
	if res.IsError {
		t.Fatalf("task_create failed: %s", res.Content)
	}
	m := regexp.MustCompile(`#(\d+)`).FindStringSubmatch(res.Content)
	if m == nil {
		t.Fatalf("no task id in task_create result: %s", res.Content)
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		t.Fatalf("parse task id %q: %v", m[1], err)
	}
	return id
}

func taskStatus(t *testing.T, st *store.Store, id int64) store.Status {
	t.Helper()
	task, err := st.GetTask(id)
	if err != nil {
		t.Fatalf("get task #%d: %v", id, err)
	}
	return task.Status
}

// gitTry runs git and RETURNS the error instead of failing — for the steps
// that are EXPECTED to conflict.
func gitTry(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
