package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// checksSpace is liteSpace with a live check runner (CHK).
func checksSpace(t *testing.T, command string, active ...string) *swarm.SwarmSpace {
	t.Helper()
	sp := liteSpace(t, active...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sp.ConfigureChecks(ctx, agentdef.CheckSpec{Command: command, Timeout: time.Minute})
	return sp
}

// TestTaskCreateChecksValidation (CHK-4): verify:"checks" demands a
// configured space; check takes only on/off; the contradictory combination
// is refused; check:"off" persists on the row.
func TestTaskCreateChecksValidation(t *testing.T) {
	t.Run("checks_policy_needs_config", func(t *testing.T) {
		sp := liteSpace(t, "a")
		res := exec(t, newTaskCreate(leaderMC(sp)), `{"title":"x","assignee":"a","verify":"checks"}`)
		if !res.IsError || !strings.Contains(res.Content, "settings.verify_checks") {
			t.Fatalf("unconfigured verify:checks = %+v, want a correctable error naming the knob", res)
		}
	})

	sp := checksSpace(t, "true", "a")
	create := newTaskCreate(leaderMC(sp))

	t.Run("checks_policy_accepted", func(t *testing.T) {
		res := exec(t, create, `{"title":"x","assignee":"a","verify":"checks"}`)
		if res.IsError {
			t.Fatalf("configured verify:checks errored: %s", res.Content)
		}
		got, _ := sp.Store.GetTask(1)
		if got.VerifyPolicy != store.VerifyChecks {
			t.Fatalf("policy = %q, want checks", got.VerifyPolicy)
		}
	})

	t.Run("check_off_persists", func(t *testing.T) {
		res := exec(t, create, `{"title":"docs","assignee":"a","check":"off"}`)
		if res.IsError {
			t.Fatalf("check:off create errored: %s", res.Content)
		}
		got, _ := sp.Store.GetTask(2)
		if !got.CheckOff {
			t.Fatal("check:\"off\" did not persist")
		}
	})

	t.Run("bad_check_value", func(t *testing.T) {
		res := exec(t, create, `{"title":"x","assignee":"a","check":"maybe"}`)
		if !res.IsError || !strings.Contains(res.Content, `"on" or "off"`) {
			t.Fatalf("bad check value = %+v, want enum error", res)
		}
	})

	t.Run("contradiction_refused", func(t *testing.T) {
		res := exec(t, create, `{"title":"x","assignee":"a","verify":"checks","check":"off"}`)
		if !res.IsError || !strings.Contains(res.Content, "contradictory") {
			t.Fatalf("checks+off = %+v, want contradiction error", res)
		}
	})
}

// TestTaskDoneQueuesCheck (CHK-3): the worker's task_done on a checks-policy
// task queues the run and says so — and the green run auto-completes with no
// leader mail (silence is the feature).
func TestTaskDoneQueuesCheck(t *testing.T) {
	sp := checksSpace(t, "true", "a")
	exec(t, newTaskCreate(leaderMC(sp)), `{"title":"x","assignee":"a","verify":"checks"}`)
	lead := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(1, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}

	res := exec(t, newTaskDone(workerMC(sp, "a")), `{"task_id":1,"result":"done, see diff"}`)
	if res.IsError || !strings.Contains(res.Content, "check is running") {
		t.Fatalf("task_done = %+v, want the check-running note", res)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if got, _ := sp.Store.GetTask(1); got.Status == store.StatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			got, _ := sp.Store.GetTask(1)
			t.Fatalf("task never auto-completed: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(mailsTo(t, sp, "leader")); n != 0 {
		t.Fatalf("leader mails = %d, want 0 on a green checks task", n)
	}
}

// TestTaskDoneChecksDegradesWithoutRunner: a verify:"checks" task on a space
// whose checks were since unconfigured falls back to the leader-mail flow —
// never stranded.
func TestTaskDoneChecksDegradesWithoutRunner(t *testing.T) {
	sp := liteSpace(t, "a")
	// Seed the row directly (task_create would refuse checks here — that gate
	// is exactly why this state only arises from a config change).
	id, err := sp.Store.CreateTask(store.Task{Title: "x", Spec: "s", Assignee: "a", CreatedBy: "leader", VerifyPolicy: store.VerifyChecks})
	if err != nil {
		t.Fatal(err)
	}
	lead := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(id, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}

	res := exec(t, newTaskDone(workerMC(sp, "a")), `{"task_id":1,"result":"done"}`)
	if res.IsError || !strings.Contains(res.Content, "leader notified") {
		t.Fatalf("degraded task_done = %+v, want the leader-mail flow", res)
	}
	if n := len(mailsTo(t, sp, "leader")); n != 1 {
		t.Fatalf("leader mails = %d, want 1 (degraded to leader flow)", n)
	}
	mustStatus(t, sp, id, store.StatusVerifying)
}

// TestTaskUpdateStatusQueuesCheck (CHK-3): the leader's manual move to
// verifying is the other verifying-entry — it queues the check and says so.
func TestTaskUpdateStatusQueuesCheck(t *testing.T) {
	sp := checksSpace(t, "true", "a")
	exec(t, newTaskCreate(leaderMC(sp)), `{"title":"x","assignee":"a"}`)
	lead := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp.Store.TransitionTask(1, store.StatusRunning, lead, ""); err != nil {
		t.Fatal(err)
	}

	res := exec(t, newTaskUpdateStatus(leaderMC(sp)), `{"task_id":1,"status":"verifying"}`)
	if res.IsError || !strings.Contains(res.Content, "Check queued") {
		t.Fatalf("update->verifying = %+v, want the check-queued note", res)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if got, _ := sp.Store.GetTask(1); got.Checks != nil {
			if !got.Checks.Pass {
				t.Fatalf("evidence = %+v, want pass", got.Checks)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("evidence never landed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// verify:"leader" task: evidence is advisory, the row stays verifying.
	mustStatus(t, sp, 1, store.StatusVerifying)
}

// TestFormatTaskChecksLine: the board renders the policy marker, the opt-out
// marker, and the one-line evidence summary.
func TestFormatTaskChecksLine(t *testing.T) {
	tk := store.Task{ID: 9, Title: "t", Status: store.StatusVerifying, Assignee: "a",
		VerifyPolicy: store.VerifyChecks,
		Checks:       &store.CheckEvidence{Exit: 1, DurationMs: 123000, Pass: false}}
	out := formatTask(tk, 0)
	if !strings.Contains(out, "[verify:checks]") {
		t.Fatalf("missing policy marker: %q", out)
	}
	if !strings.Contains(out, "checks: FAIL (exit 1) in 2m3s") {
		t.Fatalf("missing evidence summary: %q", out)
	}

	off := store.Task{ID: 10, Title: "docs", Status: store.StatusPending, Assignee: "a", CheckOff: true}
	if out := formatTask(off, 0); !strings.Contains(out, "[check:off]") {
		t.Fatalf("missing opt-out marker: %q", out)
	}
}
