package swarm

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
)

type staticMembers []string

func (m staticMembers) ActiveMembers() []string { return m }

// liteGraphSpace is a real store + bus with no agents — DispatchReady touches
// nothing else (the tools package uses the same shape for its task tests).
func liteGraphSpace(t *testing.T, members ...string) *SwarmSpace {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &SwarmSpace{Store: st, Bus: bus.New(st, staticMembers(members))}
}

func graphLeader() store.Actor { return store.Actor{Name: "lead", Role: store.RoleLeader} }

func mailsFor(t *testing.T, sp *SwarmSpace, recipient string) []store.Message {
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

// TestDispatchReadyChain drives a 3-task chain: each completion makes exactly
// the next task dispatchable, the flip is idempotent, and every dispatch
// delivers one durable assignment mail marked auto.
func TestDispatchReadyChain(t *testing.T) {
	sp := liteGraphSpace(t, "a", "b", "c")

	t1, err := sp.Store.CreateTask(store.Task{Title: "one", Spec: "s1", Assignee: "a", CreatedBy: "lead"})
	if err != nil {
		t.Fatalf("t1: %v", err)
	}
	t2, err := sp.Store.CreateTask(store.Task{Title: "two", Spec: "s2", Assignee: "b", CreatedBy: "lead", DependsOn: []int64{t1}})
	if err != nil {
		t.Fatalf("t2: %v", err)
	}
	t3, err := sp.Store.CreateTask(store.Task{Title: "three", Spec: "", Assignee: "c", CreatedBy: "lead", DependsOn: []int64{t2}})
	if err != nil {
		t.Fatalf("t3: %v", err)
	}

	// Quiet graph: nothing ready, no mail.
	if ready, err := sp.DispatchReady(); err != nil || len(ready) != 0 {
		t.Fatalf("initial DispatchReady = %v, %v; want none", ready, err)
	}

	// Complete t1 → engine dispatches t2 with one auto-marked mail.
	for _, step := range []store.Status{store.StatusRunning, store.StatusVerifying, store.StatusCompleted} {
		if err := sp.Store.TransitionTask(t1, step, graphLeader(), ""); err != nil {
			t.Fatalf("drive t1 ->%s: %v", step, err)
		}
	}
	ready, err := sp.DispatchReady()
	if err != nil || len(ready) != 1 || ready[0].ID != t2 {
		t.Fatalf("DispatchReady after t1 = %+v, %v; want [t2]", ready, err)
	}
	if got, _ := sp.Store.GetTask(t2); got.Status != store.StatusRunning {
		t.Fatalf("t2 = %s, want running", got.Status)
	}
	mails := mailsFor(t, sp, "b")
	if len(mails) != 1 {
		t.Fatalf("mails for b = %d, want 1", len(mails))
	}
	m := mails[0]
	if m.Sender != EngineSender {
		t.Fatalf("sender = %q, want %q", m.Sender, EngineSender)
	}
	if !strings.Contains(m.Subject, "(auto-dispatched)") {
		t.Fatalf("subject %q lacks the auto marker", m.Subject)
	}
	if !strings.Contains(m.Body, "task #2") || !strings.Contains(m.Body, "s2") {
		t.Fatalf("body %q lacks title/spec", m.Body)
	}
	if m.RefTask == nil || *m.RefTask != t2 {
		t.Fatalf("RefTask = %v, want %d", m.RefTask, t2)
	}

	// Idempotent: a second engine turn dispatches nothing and mails nobody.
	if again, err := sp.DispatchReady(); err != nil || len(again) != 0 {
		t.Fatalf("second DispatchReady = %v, %v; want none", again, err)
	}
	if n := len(mailsFor(t, sp, "b")); n != 1 {
		t.Fatalf("mails for b after re-sweep = %d, want still 1", n)
	}

	// Worker reports done, leader verifies → engine dispatches t3.
	if err := sp.Store.CompleteWork(t2, store.Actor{Name: "b", Role: store.RoleWorker}, "done"); err != nil {
		t.Fatalf("t2 done: %v", err)
	}
	if err := sp.Store.TransitionTask(t2, store.StatusCompleted, graphLeader(), "lgtm"); err != nil {
		t.Fatalf("t2 verify: %v", err)
	}
	if ready, err := sp.DispatchReady(); err != nil || len(ready) != 1 || ready[0].ID != t3 {
		t.Fatalf("DispatchReady after t2 = %+v, %v; want [t3]", ready, err)
	}
	if n := len(mailsFor(t, sp, "c")); n != 1 {
		t.Fatalf("mails for c = %d, want 1", n)
	}
}

// TestAssignmentMailShapes: manual and auto dispatch share one body; only the
// marker differs.
func TestAssignmentMailShapes(t *testing.T) {
	task := store.Task{ID: 7, Title: "port the thing", Spec: "acceptance: green", Assignee: "qa"}

	manual := AssignmentMail(task, "lead", false)
	if manual.Sender != "lead" || manual.Recipient != "qa" {
		t.Fatalf("manual routing = %s->%s", manual.Sender, manual.Recipient)
	}
	if strings.Contains(manual.Subject, "auto") || strings.Contains(manual.Body, "Auto-dispatched") {
		t.Fatalf("manual mail carries the auto marker: %q / %q", manual.Subject, manual.Body)
	}
	if manual.RefTask == nil || *manual.RefTask != 7 {
		t.Fatalf("manual RefTask = %v, want 7", manual.RefTask)
	}

	auto := AssignmentMail(task, EngineSender, true)
	if !strings.Contains(auto.Subject, "(auto-dispatched)") {
		t.Fatalf("auto subject %q lacks marker", auto.Subject)
	}
	if !strings.Contains(auto.Body, "ask the leader") {
		t.Fatalf("auto body %q lacks the ambiguity guidance", auto.Body)
	}
	if !strings.HasPrefix(auto.Body, "You are assigned task #7: port the thing") {
		t.Fatalf("auto body diverged from the shared shape: %q", auto.Body)
	}
}
