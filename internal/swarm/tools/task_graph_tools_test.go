package tools

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// mailsTo lists durable mail for one recipient.
func mailsTo(t *testing.T, sp *swarm.SwarmSpace, recipient string) []store.Message {
	t.Helper()
	all, err := sp.Store.ListMessages(0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var out []store.Message
	for _, m := range all {
		if m.Recipient == recipient {
			out = append(out, m)
		}
	}
	return out
}

func mustStatus(t *testing.T, sp *swarm.SwarmSpace, id int64, want store.Status) {
	t.Helper()
	got, err := sp.Store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask(#%d): %v", id, err)
	}
	if got.Status != want {
		t.Fatalf("task #%d = %s, want %s", id, got.Status, want)
	}
}

// TestTaskCreateWithDeps: a dep'd create lands blocked, says so (naming the
// blockers), and refuses a manual assign with the same ids.
func TestTaskCreateWithDeps(t *testing.T) {
	sp := liteSpace(t, "a", "b")
	create := newTaskCreate(leaderMC(sp))

	res := exec(t, create, `{"title":"one","assignee":"a"}`)
	if res.IsError || !strings.Contains(res.Content, "task_assign") {
		t.Fatalf("depless create = %+v", res)
	}
	res = exec(t, create, `{"title":"two","assignee":"b","depends_on":[1]}`)
	if res.IsError {
		t.Fatalf("dep create errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "blocked on #1") || !strings.Contains(res.Content, "do not task_assign") {
		t.Fatalf("dep create message = %q, want blocked-on note", res.Content)
	}
	mustStatus(t, sp, 2, store.StatusBlocked)

	assign := newTaskAssign(leaderMC(sp))
	res = exec(t, assign, `{"task_id":2}`)
	if !res.IsError || !strings.Contains(res.Content, "blocked by #1") {
		t.Fatalf("assign-on-blocked = %+v, want error naming #1", res)
	}
	mustStatus(t, sp, 2, store.StatusBlocked)

	// Bad dep id is a correctable model error.
	res = exec(t, create, `{"title":"x","assignee":"a","depends_on":[99]}`)
	if !res.IsError || !strings.Contains(res.Content, "#99") {
		t.Fatalf("bad dep = %+v, want error naming #99", res)
	}
}

// TestTaskCreateBornReadyDispatches: deps all complete at create → the same
// tool call dispatches it and says who started.
func TestTaskCreateBornReadyDispatches(t *testing.T) {
	sp := liteSpace(t, "a", "b")
	create := newTaskCreate(leaderMC(sp))
	exec(t, create, `{"title":"one","assignee":"a"}`)
	for _, s := range []store.Status{store.StatusRunning, store.StatusVerifying, store.StatusCompleted} {
		if err := sp.Store.TransitionTask(1, s, store.Actor{Name: "leader", Role: store.RoleLeader}, ""); err != nil {
			t.Fatalf("drive #1 ->%s: %v", s, err)
		}
	}

	res := exec(t, create, `{"title":"two","assignee":"b","depends_on":[1]}`)
	if res.IsError || !strings.Contains(res.Content, "Auto-dispatched: #2→b") {
		t.Fatalf("born-ready create = %+v, want auto-dispatch note", res)
	}
	mustStatus(t, sp, 2, store.StatusRunning)
	if got := mailsTo(t, sp, "b"); len(got) != 1 || !strings.Contains(got[0].Subject, "(auto-dispatched)") {
		t.Fatalf("assignment mail = %+v, want one auto-marked", got)
	}
}

// TestTaskVerifyCascades: approving the head of a chain dispatches the next
// task inside the same tool call and reports it.
func TestTaskVerifyCascades(t *testing.T) {
	sp := liteSpace(t, "a", "b")
	create := newTaskCreate(leaderMC(sp))
	exec(t, create, `{"title":"one","assignee":"a"}`)
	exec(t, create, `{"title":"two","assignee":"b","depends_on":[1]}`)

	lead := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(1, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}
	if err := sp.Store.CompleteWork(1, store.Actor{Name: "a", Role: store.RoleWorker}, "done"); err != nil {
		t.Fatal(err)
	}

	verify := newTaskVerify(leaderMC(sp))
	res := exec(t, verify, `{"task_id":1,"approve":true}`)
	if res.IsError || !strings.Contains(res.Content, "Auto-dispatched: #2→b") {
		t.Fatalf("verify result = %+v, want cascade note", res)
	}
	mustStatus(t, sp, 2, store.StatusRunning)
	if got := mailsTo(t, sp, "b"); len(got) != 1 {
		t.Fatalf("mails to b = %d, want 1", len(got))
	}
}

// TestTaskDoneLeaderPolicy: the assignee's task_done records the result, moves
// to verifying, and mails the leader exactly once; a foreign worker is
// refused untouched.
func TestTaskDoneLeaderPolicy(t *testing.T) {
	sp := liteSpace(t, "a", "b")
	create := newTaskCreate(leaderMC(sp))
	exec(t, create, `{"title":"one","assignee":"worker-a"}`)
	lead := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(1, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}

	// Foreign worker: refused before any write.
	intruder := newTaskDone(workerMC(sp, "worker-b"))
	res := exec(t, intruder, `{"task_id":1,"result":"mine!"}`)
	if !res.IsError || !strings.Contains(res.Content, "assigned to worker-a") {
		t.Fatalf("foreign task_done = %+v, want ownership error", res)
	}
	mustStatus(t, sp, 1, store.StatusRunning)

	done := newTaskDone(workerMC(sp, "worker-a"))
	res = exec(t, done, `{"task_id":1,"result":"shipped in pkg/x"}`)
	if res.IsError || !strings.Contains(res.Content, "leader notified") {
		t.Fatalf("task_done = %+v", res)
	}
	mustStatus(t, sp, 1, store.StatusVerifying)
	got, _ := sp.Store.GetTask(1)
	if got.Result != "shipped in pkg/x" {
		t.Fatalf("result = %q", got.Result)
	}
	// liteSpace has no roster → the recipient falls back to the literal
	// "leader"; a real space resolves Roster.LeaderName().
	mails := mailsTo(t, sp, "leader")
	if len(mails) != 1 || !strings.Contains(mails[0].Body, "shipped in pkg/x") {
		t.Fatalf("leader mail = %+v, want one carrying the result", mails)
	}

	// Done twice: the second call finds it in verifying and is refused.
	res = exec(t, done, `{"task_id":1,"result":"again"}`)
	if !res.IsError {
		t.Fatalf("double task_done should error, got %+v", res)
	}
}

// TestTaskDoneAutoPolicy: verify:'auto' completes on task_done with NO leader
// mail and cascades the dependent in the same call.
func TestTaskDoneAutoPolicy(t *testing.T) {
	sp := liteSpace(t, "a", "b")
	create := newTaskCreate(leaderMC(sp))
	res := exec(t, create, `{"title":"one","assignee":"worker-a","verify":"auto"}`)
	if res.IsError {
		t.Fatalf("create auto: %s", res.Content)
	}
	exec(t, create, `{"title":"two","assignee":"worker-b","depends_on":[1]}`)

	lead := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(1, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}
	done := newTaskDone(workerMC(sp, "worker-a"))
	res = exec(t, done, `{"task_id":1,"result":"regenerated fixtures"}`)
	if res.IsError {
		t.Fatalf("auto task_done errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "auto-completed") || !strings.Contains(res.Content, "Auto-dispatched: #2→worker-b") {
		t.Fatalf("auto task_done = %q, want auto-complete + cascade", res.Content)
	}
	mustStatus(t, sp, 1, store.StatusCompleted)
	mustStatus(t, sp, 2, store.StatusRunning)
	if mails := mailsTo(t, sp, "leader"); len(mails) != 0 {
		t.Fatalf("leader mails on auto = %d, want 0 (silence is the feature)", len(mails))
	}
	if got, _ := sp.Store.GetTask(1); got.VerifyNote != "auto-verified (policy: auto)" {
		t.Fatalf("verify note = %q", got.VerifyNote)
	}
}

// TestFormatTaskGraphMarkers: deps and the auto policy render on the board
// line.
func TestFormatTaskGraphMarkers(t *testing.T) {
	line := formatTask(store.Task{
		ID: 7, Title: "join", Status: store.StatusBlocked, Assignee: "qa",
		DependsOn: []int64{2, 3}, VerifyPolicy: store.VerifyAuto,
	}, 0)
	if !strings.Contains(line, "deps: #2, #3") || !strings.Contains(line, "[verify:auto]") {
		t.Fatalf("formatTask = %q, want dep + policy markers", line)
	}
}
