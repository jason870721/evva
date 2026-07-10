package swarm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/common/proc"
)

// liteChecksSpace is liteGraphSpace plus a roster (leader "lead" + workers)
// and a live check runner — the smallest space a check can run in. Cleanup
// order (LIFO): cancel the runner ctx, drain the runner, close the store.
func liteChecksSpace(t *testing.T, spec agentdef.CheckSpec, workers ...string) *SwarmSpace {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	r := newRoster()
	if err := r.add("lead", agentdef.RoleLeader, "", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, w := range workers {
		if err := r.add(w, agentdef.RoleWorker, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}

	sp := &SwarmSpace{Workdir: dir, Store: st, Roster: r, metrics: newSpaceMetrics()}
	sp.Bus = bus.New(st, r)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(sp.stopChecks)
	t.Cleanup(cancel)
	sp.ConfigureChecks(ctx, spec)
	return sp
}

// verifyingTask drives a fresh task to `verifying` through leader edges.
func verifyingTask(t *testing.T, sp *SwarmSpace, assignee, policy string, checkOff bool) int64 {
	t.Helper()
	id, err := sp.Store.CreateTask(store.Task{
		Title: "t", Spec: "s", Assignee: assignee, CreatedBy: "lead",
		VerifyPolicy: policy, CheckOff: checkOff,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	lead := store.Actor{Name: "lead", Role: store.RoleLeader}
	for _, step := range []store.Status{store.StatusRunning, store.StatusVerifying} {
		if err := sp.Store.TransitionTask(id, step, lead, ""); err != nil {
			t.Fatalf("drive ->%s: %v", step, err)
		}
	}
	return id
}

// evidenceOf polls a task until its check evidence lands (waitFor is the
// shared helper in supervisor_test.go).
func evidenceOf(t *testing.T, sp *SwarmSpace, id int64) store.CheckEvidence {
	t.Helper()
	var ev *store.CheckEvidence
	waitFor(t, 15*time.Second, fmt.Sprintf("evidence on task #%d", id), func() bool {
		tk, err := sp.Store.GetTask(id)
		if err != nil {
			return false
		}
		ev = tk.Checks
		return ev != nil
	})
	return *ev
}

// TestCheckRunnerPass: a green advisory run — evidence on the row, one
// leader mail + one operator mail, PASS wording, task left in verifying
// (verify:"leader" keeps judgment with the leader), and the event line.
func TestCheckRunnerPass(t *testing.T) {
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: "echo all green", Timeout: time.Minute}, "w")
	sp.out = make(chan SpacedEvent, 16)
	id := verifyingTask(t, sp, "w", store.VerifyLeader, false)

	if !sp.EnqueueCheck(id) {
		t.Fatal("EnqueueCheck = false, want queued")
	}
	ev := evidenceOf(t, sp, id)
	if !ev.Pass || ev.Exit != 0 || ev.TimedOut || ev.Truncated {
		t.Fatalf("evidence = %+v, want a clean pass", ev)
	}
	if !strings.Contains(ev.Tail, "all green") {
		t.Fatalf("tail %q lacks command output", ev.Tail)
	}
	if ev.Workdir != sp.Workdir {
		t.Fatalf("workdir = %q, want the space workdir %q (pre-worktree seam)", ev.Workdir, sp.Workdir)
	}
	if tk, _ := sp.Store.GetTask(id); tk.Status != store.StatusVerifying {
		t.Fatalf("task = %s, want still verifying (advisory evidence, leader rules)", tk.Status)
	}

	waitFor(t, 5*time.Second, "evidence mail", func() bool { return len(mailsFor(t, sp, "user")) == 1 })
	lead := mailsFor(t, sp, "lead")
	if len(lead) != 1 {
		t.Fatalf("leader mails = %d, want 1", len(lead))
	}
	if !strings.Contains(lead[0].Subject, "PASS") || lead[0].Sender != EngineSender {
		t.Fatalf("leader mail = %q from %q, want a PASS from the engine", lead[0].Subject, lead[0].Sender)
	}
	if lead[0].RefTask == nil || *lead[0].RefTask != id {
		t.Fatalf("leader mail RefTask = %v, want %d", lead[0].RefTask, id)
	}

	select {
	case e := <-sp.out:
		if e.Event.Kind != KindTaskCheckDone || !strings.Contains(e.Event.Text.Text, "PASS") {
			t.Fatalf("event = %s %q, want task_check_done PASS", e.Event.Kind, e.Event.Text.Text)
		}
	default:
		t.Fatal("no task_check_done event emitted")
	}

	if run, failed, timeout := sp.CheckCounts(); run != 1 || failed != 0 || timeout != 0 {
		t.Fatalf("CheckCounts = %d/%d/%d, want 1/0/0", run, failed, timeout)
	}
}

// TestCheckRunnerFail: a red run records the exit code and tail, mails FAIL
// with the tail (the leader's rejection-note first draft), and counts.
func TestCheckRunnerFail(t *testing.T) {
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: "echo boom goes the suite; exit 3", Timeout: time.Minute}, "w")
	id := verifyingTask(t, sp, "w", store.VerifyLeader, false)

	sp.EnqueueCheck(id)
	ev := evidenceOf(t, sp, id)
	if ev.Pass || ev.Exit != 3 || ev.TimedOut {
		t.Fatalf("evidence = %+v, want exit 3", ev)
	}
	waitFor(t, 5*time.Second, "fail mail", func() bool { return len(mailsFor(t, sp, "lead")) == 1 })
	m := mailsFor(t, sp, "lead")[0]
	if !strings.Contains(m.Subject, "FAIL (exit 3)") {
		t.Fatalf("subject = %q, want FAIL (exit 3)", m.Subject)
	}
	if !strings.Contains(m.Body, "boom goes the suite") {
		t.Fatalf("body lacks the output tail: %q", m.Body)
	}
	if run, failed, _ := sp.CheckCounts(); run != 1 || failed != 1 {
		t.Fatalf("CheckCounts = %d run / %d failed, want 1/1", run, failed)
	}
}

// TestCheckRunnerTimeout: a hung check is tree-killed at the timeout — the
// child does not survive (proc.Alive probe), the runner returns promptly,
// and the evidence says TIMEOUT.
func TestCheckRunnerTimeout(t *testing.T) {
	// The command prints its own pid ($$ stays the shell's pid through exec)
	// so the test can probe that the KillTree took the child down with it.
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: "echo pid=$$; exec sleep 30", Timeout: 300 * time.Millisecond}, "w")
	id := verifyingTask(t, sp, "w", store.VerifyLeader, false)

	start := time.Now()
	sp.EnqueueCheck(id)
	ev := evidenceOf(t, sp, id)
	if !ev.TimedOut || ev.Pass {
		t.Fatalf("evidence = %+v, want a timeout", ev)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("timeout delivery took %s — the tree kill did not release Wait", took)
	}
	if !strings.Contains(ev.Outcome(), "TIMEOUT") {
		t.Fatalf("outcome = %q, want TIMEOUT", ev.Outcome())
	}

	// Orphan probe (unix only: Git Bash reports MSYS pids, not Windows pids,
	// so the number in the tail wouldn't name the real process there).
	if runtime.GOOS != "windows" {
		if i := strings.Index(ev.Tail, "pid="); i >= 0 {
			if pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ev.Tail[i:], "pid="))); err == nil {
				waitFor(t, 5*time.Second, "child process death", func() bool { return !proc.Alive(pid) })
			}
		} else {
			t.Fatalf("tail %q never captured the pid", ev.Tail)
		}
	}
	if _, _, timeout := sp.CheckCounts(); timeout != 1 {
		t.Fatalf("checksTimeout = %d, want 1", timeout)
	}
}

// TestCheckRunnerTruncation: output over the 16 KiB cap keeps head + tail
// with the marker between.
func TestCheckRunnerTruncation(t *testing.T) {
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: "seq 1 8000", Timeout: time.Minute}, "w")
	id := verifyingTask(t, sp, "w", store.VerifyLeader, false)

	sp.EnqueueCheck(id)
	ev := evidenceOf(t, sp, id)
	if !ev.Truncated {
		t.Fatalf("evidence not truncated: %d bytes", len(ev.Tail))
	}
	if len(ev.Tail) > checkTailCap+64 {
		t.Fatalf("tail = %d bytes, want ≤ ~%d", len(ev.Tail), checkTailCap)
	}
	if !strings.Contains(ev.Tail, "… (output truncated) …") {
		t.Fatal("truncation marker missing")
	}
	if !strings.HasPrefix(ev.Tail, "1\n2\n") || !strings.Contains(ev.Tail, "8000") {
		t.Fatalf("head/tail content lost: %q … %q", ev.Tail[:16], ev.Tail[len(ev.Tail)-16:])
	}
}

// TestCheckRunnerSupersede: re-entry while a task's check is executing kills
// that run and delivers only the fresh one — "latest verifying-entry wins".
// Two executions happen (the marker file says so); exactly one delivers.
func TestCheckRunnerSupersede(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "runs.txt")
	cmd := fmt.Sprintf("echo run >> %q; sleep 1", filepath.ToSlash(marker))
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: cmd, Timeout: time.Minute}, "w")
	id := verifyingTask(t, sp, "w", store.VerifyLeader, false)

	sp.EnqueueCheck(id)
	waitFor(t, 5*time.Second, "first run start", func() bool {
		b, err := os.ReadFile(marker)
		return err == nil && strings.Count(string(b), "run") == 1
	})
	sp.EnqueueCheck(id) // cancels the in-flight run, queues a fresh one

	ev := evidenceOf(t, sp, id)
	if !ev.Pass {
		t.Fatalf("final evidence = %+v, want the fresh full run", ev)
	}
	b, _ := os.ReadFile(marker)
	if got := strings.Count(string(b), "run"); got != 2 {
		t.Fatalf("executions = %d, want 2 (killed + fresh)", got)
	}
	// Exactly one delivery: the superseded run mailed nobody.
	waitFor(t, 5*time.Second, "single delivery", func() bool { return len(mailsFor(t, sp, "user")) == 1 })
	time.Sleep(150 * time.Millisecond) // a late double-delivery would land here
	if n := len(mailsFor(t, sp, "user")); n != 1 {
		t.Fatalf("deliveries = %d, want exactly 1", n)
	}
	if run, _, _ := sp.CheckCounts(); run != 1 {
		t.Fatalf("checksRun = %d, want 1 (superseded runs never count)", run)
	}
}

// TestCheckRunnerQueueDedup: a task already queued is not queued twice.
func TestCheckRunnerQueueDedup(t *testing.T) {
	r := newCheckRunner(nil, agentdef.CheckSpec{Command: "true", Timeout: time.Minute})
	r.enqueue(7)
	r.enqueue(7)
	r.enqueue(9)
	if len(r.queue) != 2 || r.queue[0] != 7 || r.queue[1] != 9 {
		t.Fatalf("queue = %v, want [7 9]", r.queue)
	}
}

// TestEnqueueCheckSkips: the no-run cases — no runner configured, auto
// policy (auto wins, D4), check:"off", and a task not in verifying.
func TestEnqueueCheckSkips(t *testing.T) {
	t.Run("no_runner", func(t *testing.T) {
		sp := liteGraphSpace(t, "w")
		id, _ := sp.Store.CreateTask(store.Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "lead"})
		if sp.EnqueueCheck(id) {
			t.Fatal("EnqueueCheck queued on a space with no checks configured")
		}
	})

	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: "echo hi", Timeout: time.Minute}, "w")
	t.Run("auto_policy", func(t *testing.T) {
		id := verifyingTask(t, sp, "w", store.VerifyAuto, false)
		if sp.EnqueueCheck(id) {
			t.Fatal("EnqueueCheck queued an auto-policy task (auto wins)")
		}
	})
	t.Run("check_off", func(t *testing.T) {
		id := verifyingTask(t, sp, "w", store.VerifyLeader, true)
		if sp.EnqueueCheck(id) {
			t.Fatal("EnqueueCheck queued a check:\"off\" task")
		}
	})
	t.Run("not_verifying", func(t *testing.T) {
		id, _ := sp.Store.CreateTask(store.Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "lead"})
		if sp.EnqueueCheck(id) {
			t.Fatal("EnqueueCheck queued a pending task")
		}
	})
}

// TestVerifyChecksChainLeaderless (CHK-5): a 3-task verify:"checks" chain on
// a green command completes end-to-end with ZERO leader involvement — each
// task_done's check passes, the system settles it, and the engine dispatches
// the next.
func TestVerifyChecksChainLeaderless(t *testing.T) {
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: "true", Timeout: time.Minute}, "a", "b", "c")

	mk := func(assignee string, deps ...int64) int64 {
		id, err := sp.Store.CreateTask(store.Task{
			Title: "step " + assignee, Spec: "s", Assignee: assignee, CreatedBy: "lead",
			VerifyPolicy: store.VerifyChecks, DependsOn: deps,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return id
	}
	t1 := mk("a")
	t2 := mk("b", t1)
	t3 := mk("c", t2)

	// Kick t1 the manual way (depless = leader-managed dispatch).
	lead := store.Actor{Name: "lead", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(t1, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}

	// Each worker reports done the moment its task runs; the check + engine
	// do the rest. This mirrors the task_done tool: CompleteWork + enqueue.
	done := map[int64]string{t1: "a", t2: "b", t3: "c"}
	for _, id := range []int64{t1, t2, t3} {
		waitFor(t, 15*time.Second, fmt.Sprintf("task #%d running", id), func() bool {
			tk, _ := sp.Store.GetTask(id)
			return tk.Status == store.StatusRunning
		})
		if err := sp.Store.CompleteWork(id, store.Actor{Name: done[id], Role: store.RoleWorker}, "done"); err != nil {
			t.Fatalf("task_done #%d: %v", id, err)
		}
		sp.EnqueueCheck(id)
	}

	waitFor(t, 15*time.Second, "chain completion", func() bool {
		tk, _ := sp.Store.GetTask(t3)
		return tk.Status == store.StatusCompleted
	})
	for _, id := range []int64{t1, t2} {
		if tk, _ := sp.Store.GetTask(id); tk.Status != store.StatusCompleted {
			t.Fatalf("task #%d = %s, want completed", id, tk.Status)
		}
	}
	// Zero leader wakes: green auto-completion mails nobody.
	if n := len(mailsFor(t, sp, "lead")); n != 0 {
		t.Fatalf("leader mails = %d, want 0 (silence is the feature)", n)
	}
	if got, _ := sp.Store.GetTask(t1); got.VerifyNote == "" || !strings.Contains(got.VerifyNote, "policy: checks") {
		t.Fatalf("verify note = %q, want the auto-verified provenance", got.VerifyNote)
	}
}

// TestVerifyChecksRedEscalates (CHK-5): a red check on a verify:"checks"
// task leaves it in `verifying`, mails the leader the evidence, and the
// reject → rework → done loop re-runs the check.
func TestVerifyChecksRedEscalates(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "green")
	// Red until the gate file exists — the rework flips it.
	cmd := fmt.Sprintf("test -f %q", filepath.ToSlash(gate))
	sp := liteChecksSpace(t, agentdef.CheckSpec{Command: cmd, Timeout: time.Minute}, "w")

	id, err := sp.Store.CreateTask(store.Task{
		Title: "gated", Spec: "s", Assignee: "w", CreatedBy: "lead", VerifyPolicy: store.VerifyChecks,
	})
	if err != nil {
		t.Fatal(err)
	}
	lead := store.Actor{Name: "lead", Role: store.RoleLeader}
	worker := store.Actor{Name: "w", Role: store.RoleWorker}
	if err := sp.Store.TransitionTask(id, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}
	if err := sp.Store.CompleteWork(id, worker, "first attempt"); err != nil {
		t.Fatal(err)
	}
	sp.EnqueueCheck(id)

	// Red: stays verifying, evidence mailed.
	waitFor(t, 15*time.Second, "red evidence", func() bool {
		tk, _ := sp.Store.GetTask(id)
		return tk.Checks != nil && !tk.Checks.Pass
	})
	if tk, _ := sp.Store.GetTask(id); tk.Status != store.StatusVerifying {
		t.Fatalf("task = %s, want verifying (red never auto-rejects)", tk.Status)
	}
	waitFor(t, 5*time.Second, "escalation mail", func() bool { return len(mailsFor(t, sp, "lead")) == 1 })
	if m := mailsFor(t, sp, "lead")[0]; !strings.Contains(m.Body, "stays in verifying") {
		t.Fatalf("escalation body = %q, want the stays-in-verifying guidance", m.Body)
	}

	// Leader rejects; worker reworks (creates the gate) and reports again.
	if err := sp.Store.TransitionTask(id, store.StatusRunning, lead, "make the check pass"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sp.Store.CompleteWork(id, worker, "second attempt"); err != nil {
		t.Fatal(err)
	}
	sp.EnqueueCheck(id)

	waitFor(t, 15*time.Second, "green completion", func() bool {
		tk, _ := sp.Store.GetTask(id)
		return tk.Status == store.StatusCompleted
	})
	if tk, _ := sp.Store.GetTask(id); tk.Checks == nil || !tk.Checks.Pass {
		t.Fatalf("final evidence = %+v, want the green re-run", tk.Checks)
	}
}

// TestNewSpaceWiresChecks: the manifest knob constructs the runner, every
// member's composed prompt teaches the check, and Shutdown drains cleanly.
func TestNewSpaceWiresChecks(t *testing.T) {
	m := testManifest()
	m.Settings.VerifyChecks = &agentdef.CheckSpec{Command: "go test ./...", Timeout: time.Minute}
	sp, err := NewSpace("chk", m, testLoaded(), nil, stubConfig(t))
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	if !sp.ChecksConfigured() {
		t.Fatal("ChecksConfigured = false on a verify_checks manifest")
	}
	def, ok := sp.reg.Get("leader")
	if !ok {
		t.Fatal("leader def missing from the space registry")
	}
	if !strings.Contains(def.SystemPrompt, "Machine checks at verify time") || !strings.Contains(def.SystemPrompt, "go test ./...") {
		t.Fatal("leader prompt lacks the checks protocol section")
	}
	sp.Shutdown() // must drain the runner before the store closes — no hang, no panic

	// And the off case stays off.
	sp2, err := NewSpace("chk2", testManifest(), testLoaded(), nil, stubConfig(t))
	if err != nil {
		t.Fatalf("NewSpace(off): %v", err)
	}
	defer sp2.Shutdown()
	if sp2.ChecksConfigured() {
		t.Fatal("ChecksConfigured = true without the knob")
	}
}

// TestCheckTeardownMidRun (CHK-3): tearing the space down mid-check kills
// the run, delivers nothing, and leaves the task in `verifying` with no
// evidence — the documented restart semantics (no resume machinery).
func TestCheckTeardownMidRun(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newRoster()
	_ = r.add("lead", agentdef.RoleLeader, "", "", nil)
	_ = r.add("w", agentdef.RoleWorker, "", "", nil)
	sp := &SwarmSpace{Workdir: dir, Store: st, Roster: r, metrics: newSpaceMetrics()}
	sp.Bus = bus.New(st, r)
	ctx, cancel := context.WithCancel(context.Background())
	sp.ConfigureChecks(ctx, agentdef.CheckSpec{Command: "sleep 30", Timeout: time.Minute})

	id := verifyingTask(t, sp, "w", store.VerifyLeader, false)
	sp.EnqueueCheck(id)
	waitFor(t, 5*time.Second, "check start", func() bool { return sp.CheckPending(id) })
	time.Sleep(100 * time.Millisecond) // let the exec spawn

	// Teardown: cancel (tree-kills the child), drain, close — Shutdown's order.
	start := time.Now()
	cancel()
	sp.stopChecks()
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("teardown took %s — the runner did not drain promptly", took)
	}
	tk, err := sp.Store.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != store.StatusVerifying || tk.Checks != nil {
		t.Fatalf("after teardown: status=%s checks=%+v, want verifying with NULL evidence", tk.Status, tk.Checks)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store close after drain: %v", err)
	}
}
