package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func mkWorker() *WorkerSpec { return &WorkerSpec{AgentType: "general-purpose"} }

// The full (from, to, actor) × (self-task | worker task) × (auto | leader |
// failed) product against the writer matrix. The expected sets below ARE the
// SDW PRD §4 table — any new edge must be deliberately added here.
func TestWriterMatrixProduct(t *testing.T) {
	statuses := []Status{StatusBlocked, StatusPending, StatusRunning, StatusVerifying, StatusCompleted}
	actors := []Actor{ActorRoot, ActorSystem}

	type variant struct {
		name string
		task Task
	}
	variants := []variant{
		{"self", Task{Verify: VerifyLeader}},
		{"worker-leader", Task{Verify: VerifyLeader, Worker: mkWorker()}},
		{"worker-auto", Task{Verify: VerifyAuto, Worker: mkWorker()}},
		{"worker-auto-failed", Task{Verify: VerifyAuto, Worker: mkWorker(), WorkerFailed: true}},
	}

	type key struct {
		variant string
		from    Status
		to      Status
		actor   Actor
	}
	legal := map[key]bool{
		// Self-tasks: every edge is root judgment except dependency unblock.
		{"self", StatusBlocked, StatusPending, ActorRoot}:     true,
		{"self", StatusBlocked, StatusPending, ActorSystem}:   true,
		{"self", StatusPending, StatusRunning, ActorRoot}:     true,
		{"self", StatusRunning, StatusVerifying, ActorRoot}:   true,
		{"self", StatusRunning, StatusCompleted, ActorRoot}:   true,
		{"self", StatusVerifying, StatusCompleted, ActorRoot}: true,
		{"self", StatusVerifying, StatusPending, ActorRoot}:   true,
		// Worker tasks: mechanical edges are the system's, judgment the root's.
		{"worker-leader", StatusBlocked, StatusPending, ActorRoot}:     true,
		{"worker-leader", StatusBlocked, StatusPending, ActorSystem}:   true,
		{"worker-leader", StatusPending, StatusRunning, ActorSystem}:   true,
		{"worker-leader", StatusRunning, StatusVerifying, ActorSystem}: true,
		{"worker-leader", StatusRunning, StatusPending, ActorSystem}:   true,
		{"worker-leader", StatusVerifying, StatusCompleted, ActorRoot}: true,
		{"worker-leader", StatusVerifying, StatusPending, ActorRoot}:   true,
		{"worker-leader", StatusVerifying, StatusPending, ActorSystem}: true,
		// verify:"auto" additionally lets the system complete.
		{"worker-auto", StatusBlocked, StatusPending, ActorRoot}:       true,
		{"worker-auto", StatusBlocked, StatusPending, ActorSystem}:     true,
		{"worker-auto", StatusPending, StatusRunning, ActorSystem}:     true,
		{"worker-auto", StatusRunning, StatusVerifying, ActorSystem}:   true,
		{"worker-auto", StatusRunning, StatusPending, ActorSystem}:     true,
		{"worker-auto", StatusVerifying, StatusCompleted, ActorRoot}:   true,
		{"worker-auto", StatusVerifying, StatusCompleted, ActorSystem}: true,
		{"worker-auto", StatusVerifying, StatusPending, ActorRoot}:     true,
		{"worker-auto", StatusVerifying, StatusPending, ActorSystem}:   true,
		// ...but never a failed worker's result.
		{"worker-auto-failed", StatusBlocked, StatusPending, ActorRoot}:     true,
		{"worker-auto-failed", StatusBlocked, StatusPending, ActorSystem}:   true,
		{"worker-auto-failed", StatusPending, StatusRunning, ActorSystem}:   true,
		{"worker-auto-failed", StatusRunning, StatusVerifying, ActorSystem}: true,
		{"worker-auto-failed", StatusRunning, StatusPending, ActorSystem}:   true,
		{"worker-auto-failed", StatusVerifying, StatusCompleted, ActorRoot}: true,
		{"worker-auto-failed", StatusVerifying, StatusPending, ActorRoot}:   true,
		{"worker-auto-failed", StatusVerifying, StatusPending, ActorSystem}: true,
	}

	for _, v := range variants {
		for _, from := range statuses {
			for _, to := range statuses {
				if from == to {
					continue
				}
				for _, actor := range actors {
					task := v.task
					task.Status = from
					err := transitionErr(&task, to, actor)
					want := legal[key{v.name, from, to, actor}]
					if want && err != nil {
						t.Errorf("%s %s→%s by %s: want legal, got %v", v.name, from, to, actor, err)
					}
					if !want && err == nil {
						t.Errorf("%s %s→%s by %s: want denied, got legal", v.name, from, to, actor)
					}
				}
			}
		}
	}
}

func TestCreateValidationAndBirthStatus(t *testing.T) {
	s := NewStore()

	if _, err := s.Create(Task{Subject: "  "}); err == nil {
		t.Error("empty subject should be rejected")
	}
	if _, err := s.Create(Task{Subject: "x", Verify: "checks"}); err == nil {
		t.Error("unknown verify policy should be rejected")
	}
	if _, err := s.Create(Task{Subject: "x", Verify: VerifyAuto}); err == nil {
		t.Error(`verify:"auto" without a worker should be rejected`)
	}
	if _, err := s.Create(Task{Subject: "x", Worker: &WorkerSpec{}}); err == nil {
		t.Error("worker without agent_type should be rejected")
	}
	if _, err := s.Create(Task{Subject: "x", DependsOn: []string{"99"}}); !errors.Is(err, ErrDepNotFound) {
		t.Errorf("missing dep: want ErrDepNotFound, got %v", err)
	}

	a, err := s.Create(Task{Subject: "a", Worker: mkWorker()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusPending || a.Verify != VerifyLeader {
		t.Errorf("depless create: got %s/%s, want pending/leader", a.Status, a.Verify)
	}

	b, err := s.Create(Task{Subject: "b", DependsOn: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != StatusBlocked {
		t.Errorf("dep on incomplete task: got %s, want blocked", b.Status)
	}

	// A dep on an already-completed task is satisfied at birth.
	mustTransition(t, s, a.ID, StatusRunning, ActorSystem)
	if _, err := s.CompleteWork(a.ID, "done", false); err != nil {
		t.Fatal(err)
	}
	mustTransition(t, s, a.ID, StatusCompleted, ActorRoot)
	c, err := s.Create(Task{Subject: "c", DependsOn: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusPending {
		t.Errorf("dep on completed task: got %s, want pending", c.Status)
	}
}

func mustTransition(t *testing.T, s *Store, id string, to Status, actor Actor) Task {
	t.Helper()
	task, err := s.Transition(id, to, actor, "")
	if err != nil {
		t.Fatalf("transition %s to %s by %s: %v", id, to, actor, err)
	}
	return task
}

func TestUnblockCascadeAndDispatchable(t *testing.T) {
	s := NewStore()
	a, _ := s.Create(Task{Subject: "a", Worker: mkWorker()})
	b, _ := s.Create(Task{Subject: "b", Worker: mkWorker(), DependsOn: []string{a.ID}})
	cSelf, _ := s.Create(Task{Subject: "c-self", DependsOn: []string{a.ID}})
	d, _ := s.Create(Task{Subject: "d", Worker: mkWorker(), DependsOn: []string{a.ID, b.ID}})

	if got := s.Dispatchable(); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("dispatchable = %v, want [a]", ids(got))
	}

	// Complete a → b and c-self unblock; d still waits on b.
	if _, err := s.Dispatch(a.ID, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork(a.ID, "built", false); err != nil {
		t.Fatal(err)
	}
	mustTransition(t, s, a.ID, StatusCompleted, ActorRoot)
	flipped := s.UnblockDependents(a.ID)
	if len(flipped) != 2 {
		t.Fatalf("unblocked %v, want [b c-self]", ids(flipped))
	}
	if got, _ := s.Get(d.ID); got.Status != StatusBlocked {
		t.Errorf("d should stay blocked, got %s", got.Status)
	}
	// Idempotent: a second sweep flips nothing.
	if again := s.UnblockDependents(a.ID); len(again) != 0 {
		t.Errorf("second unblock flipped %v, want none", ids(again))
	}
	// Only the engine-managed b is dispatchable; c-self is the root's.
	if got := s.Dispatchable(); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("dispatchable = %v, want [b]", ids(got))
	}
	if got, _ := s.Get(cSelf.ID); got.Status != StatusPending {
		t.Errorf("c-self = %s, want pending", got.Status)
	}
}

func ids(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestDispatchOwnerAndRejectRework(t *testing.T) {
	s := NewStore()
	a, _ := s.Create(Task{Subject: "a", Worker: mkWorker()})

	got, err := s.Dispatch(a.ID, "daemon-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "daemon-1" || got.Status != StatusRunning {
		t.Fatalf("dispatch: got %s/%q", got.Status, got.Owner)
	}
	if _, err := s.CompleteWork(a.ID, "attempt 1", true); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get(a.ID)
	if !v.WorkerFailed || v.Result != "attempt 1" {
		t.Fatalf("complete-work(failed): %+v", v)
	}
	// Failed work must not auto-complete even under verify:"auto" — covered
	// in the matrix test; here the reject path clears the failure marker.
	rejected, err := s.Transition(a.ID, StatusPending, ActorRoot, "rework: split the file")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Owner != "" || rejected.WorkerFailed {
		t.Errorf("reject should clear owner + failure marker: %+v", rejected)
	}
	if len(rejected.Comments) != 1 || rejected.Comments[0].By != ActorRoot {
		t.Errorf("reject note should append a root comment: %+v", rejected.Comments)
	}
}

func TestDeleteGuards(t *testing.T) {
	s := NewStore()
	a, _ := s.Create(Task{Subject: "a", Worker: mkWorker()})
	b, _ := s.Create(Task{Subject: "b", DependsOn: []string{a.ID}})

	if err := s.Delete(a.ID); err == nil || !strings.Contains(err.Error(), b.ID) {
		t.Errorf("delete depended-on task: want refusal naming %s, got %v", b.ID, err)
	}
	if err := s.Delete(b.ID); err != nil {
		t.Errorf("delete leaf task: %v", err)
	}
	if _, err := s.Dispatch(a.ID, "w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); err == nil {
		t.Error("delete running task should be refused")
	}
	if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestUpdateGuards(t *testing.T) {
	s := NewStore()
	a, _ := s.Create(Task{Subject: "a"})
	mustTransition(t, s, a.ID, StatusRunning, ActorRoot)
	mustTransition(t, s, a.ID, StatusCompleted, ActorRoot)

	if _, err := s.Update(a.ID, Patch{Subject: strp("new")}); err == nil {
		t.Error("editing a completed task should be refused")
	}
	if _, err := s.Update(a.ID, Patch{Note: "post-mortem"}); err != nil {
		t.Errorf("note on completed task should be allowed: %v", err)
	}
	b, _ := s.Create(Task{Subject: "b"})
	got, err := s.Update(b.ID, Patch{Description: strp("details"), ActiveForm: strp("doing b")})
	if err != nil || got.Description != "details" || got.ActiveForm != "doing b" {
		t.Errorf("update: %v %+v", err, got)
	}
	if _, err := s.Update(b.ID, Patch{Subject: strp("  ")}); err == nil {
		t.Error("emptying subject should be refused")
	}
}

func strp(s string) *string { return &s }

func TestPersistenceReplayRoundTrip(t *testing.T) {
	dir := t.TempDir()

	s := NewStore()
	s.SetPersistence(dir)
	if err := s.SetSession("sess-1"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Create(Task{Subject: "a", Worker: mkWorker(), Verify: VerifyAuto})
	b, _ := s.Create(Task{Subject: "b", Worker: mkWorker(), DependsOn: []string{a.ID}})
	tmp, _ := s.Create(Task{Subject: "oops"})
	if err := s.Delete(tmp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(a.ID, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork(a.ID, "result A", false); err != nil {
		t.Fatal(err)
	}
	mustTransition(t, s, a.ID, StatusCompleted, ActorSystem) // verify:"auto"
	s.UnblockDependents(a.ID)
	s.Close()

	// Fresh store, same session → identical board; id counter continues.
	r := NewStore()
	r.SetPersistence(dir)
	if err := r.SetSession("sess-1"); err != nil {
		t.Fatal(err)
	}
	tasks := r.List()
	if len(tasks) != 2 {
		t.Fatalf("replayed %d tasks, want 2 (deleted one gone): %v", len(tasks), ids(tasks))
	}
	ra, _ := r.Get(a.ID)
	if ra.Status != StatusCompleted || ra.Result != "result A" || ra.Verify != VerifyAuto {
		t.Errorf("replayed a: %+v", ra)
	}
	rb, _ := r.Get(b.ID)
	if rb.Status != StatusPending || len(rb.DependsOn) != 1 {
		t.Errorf("replayed b: %+v", rb)
	}
	next, _ := r.Create(Task{Subject: "next"})
	if next.ID != "4" { // a=1, b=2, oops=3 → counter resumes at 4
		t.Errorf("id counter after replay = %s, want 4", next.ID)
	}

	// A different session id starts an empty board.
	f := NewStore()
	f.SetPersistence(dir)
	if err := f.SetSession("sess-2"); err != nil {
		t.Fatal(err)
	}
	if got := f.List(); len(got) != 0 {
		t.Errorf("fresh session should be empty, got %v", ids(got))
	}
}

func TestReplayToleratesCRLFAndGarbage(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	s.SetPersistence(dir)
	if err := s.SetSession("sess"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Create(Task{Subject: "a"})
	s.Close()

	// Rewrite the log with CRLF endings, blank lines, and a garbage line —
	// the shape a Windows-side editor or a torn write leaves behind.
	path := filepath.Join(dir, "sess.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mangled := strings.ReplaceAll(string(raw), "\n", "\r\n") + "\r\n{not json}\r\n\r\n"
	if err := os.WriteFile(path, []byte(mangled), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewStore()
	r.SetPersistence(dir)
	if err := r.SetSession("sess"); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get(a.ID)
	if !ok || got.Subject != "a" {
		t.Fatalf("CRLF replay lost the task: %+v ok=%v", got, ok)
	}
}

func TestResetLostRunning(t *testing.T) {
	s := NewStore()
	worker, _ := s.Create(Task{Subject: "w", Worker: mkWorker()})
	self, _ := s.Create(Task{Subject: "self"})
	alive, _ := s.Create(Task{Subject: "alive", Worker: mkWorker()})

	if _, err := s.Dispatch(worker.ID, "dead-daemon"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(alive.ID, "live-daemon"); err != nil {
		t.Fatal(err)
	}
	mustTransition(t, s, self.ID, StatusRunning, ActorRoot)

	reset := s.ResetLostRunning(func(owner string) bool { return owner == "live-daemon" })
	if len(reset) != 1 || reset[0].ID != worker.ID {
		t.Fatalf("reset %v, want [worker]", ids(reset))
	}
	if got, _ := s.Get(worker.ID); got.Status != StatusPending || got.Owner != "" {
		t.Errorf("lost worker task: %+v", got)
	}
	if got, _ := s.Get(self.ID); got.Status != StatusRunning {
		t.Errorf("running self-task must survive resume, got %s", got.Status)
	}
	if got, _ := s.Get(alive.ID); got.Status != StatusRunning || got.Owner != "live-daemon" {
		t.Errorf("live worker task must survive, got %+v", got)
	}
}

func TestConcurrentAccessSmoke(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 25 {
				id, err := s.Create(Task{Subject: "t", Worker: mkWorker()})
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := s.Dispatch(id.ID, "w"); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.CompleteWork(id.ID, "r", false); err != nil {
					t.Error(err)
					return
				}
				s.List()
				s.Counts()
			}
		})
	}
	wg.Wait()
	if got := len(s.List()); got != 200 {
		t.Errorf("tasks = %d, want 200", got)
	}
}
