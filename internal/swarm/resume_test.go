package swarm

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// TestRestartResume is the centerpiece: a space populated with a transcript, an
// unread message, a running task, and a frozen member is torn down and rebuilt
// from the same on-disk state — and comes back where it died, nothing lost.
//
// It reuses the same *config.Config across both lives so the second NewSpace
// sees the first's .vero/ db and the SDK session store (kill -9 + restart, in
// effect).
func TestRestartResume(t *testing.T) {
	cfg := stubConfig(t)

	// --- first life ---------------------------------------------------------
	sp1, err := NewSpace("s1", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #1: %v", err)
	}

	const codeword = "the codeword is hunter2"
	if _, err := sp1.agents["leader"].Run(context.Background(), "remember: "+codeword); err != nil {
		t.Fatalf("leader run: %v", err)
	}

	// Unread mail for an idle worker — no supervisor is running, so it stays
	// unread and must survive the restart.
	uuid, err := sp1.Bus.Send(store.Message{Sender: "leader", Recipient: "worker-a", Body: "do task X"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// A task left mid-flight (running).
	tid, err := sp1.Store.CreateTask(store.Task{Title: "build", CreatedBy: "leader", Assignee: "worker-a"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	leader := store.Actor{Name: "leader", Role: store.RoleLeader}
	if err := sp1.Store.TransitionTask(tid, store.StatusRunning, leader, ""); err != nil {
		t.Fatalf("transition: %v", err)
	}

	// Freeze a member (persists runtime.json).
	sup1 := NewSupervisor(sp1)
	if err := sup1.Freeze("worker-b"); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	sp1.Shutdown() // simulate the process dying

	// --- restart ------------------------------------------------------------
	sp2, err := NewSpace("s1", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #2: %v", err)
	}
	sp2.Reload()
	defer sp2.Shutdown()

	// AC#1 — the unread message is re-queued onto the rebuilt mailbox.
	select {
	case got := <-sp2.Bus.Inbox("worker-a"):
		if got != uuid {
			t.Errorf("re-queued uuid = %q, want %q", got, uuid)
		}
	default:
		t.Error("worker-a's unread message was not re-queued after restart")
	}

	// AC#2 — the leader's transcript is resumed (the codeword turn is back).
	msgs := sp2.agents["leader"].Controller().Messages()
	if len(msgs) == 0 {
		t.Fatal("leader transcript not resumed (empty)")
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, codeword) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("resumed transcript missing the pre-restart context (%q)", codeword)
	}

	// AC#3 — the running task is still running, same row.
	task, err := sp2.Store.GetTask(tid)
	if err != nil {
		t.Fatalf("get task after restart: %v", err)
	}
	if task.Status != store.StatusRunning {
		t.Errorf("task #%d status = %q, want running", tid, task.Status)
	}

	// AC#4 — the frozen member comes back frozen; the others active.
	for _, mv := range sp2.Roster.Snapshot() {
		switch mv.Name {
		case "worker-b":
			if mv.Membership != MembershipFrozen {
				t.Errorf("worker-b membership = %q, want frozen", mv.Membership)
			}
		default:
			if mv.Membership != MembershipActive {
				t.Errorf("%s membership = %q, want active", mv.Name, mv.Membership)
			}
		}
	}
}

// TestReloadFreshSpaceIsNoop: Reload on a never-run space (no sessions, no mail,
// no runtime.json) leaves everyone active and idle — the first-boot path.
func TestReloadFreshSpaceIsNoop(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("fresh", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	sp.Reload() // must not panic or change anything

	for _, mv := range sp.Roster.Snapshot() {
		if mv.Membership != MembershipActive || mv.Run != RunIdle {
			t.Errorf("%s = %s/%s, want active/idle after no-op reload", mv.Name, mv.Membership, mv.Run)
		}
	}
}

// TestReloadIdempotent: the DB is the source of truth, so however many times
// Reload runs, the message stays unread exactly once until it's actually
// processed. (The mailbox carries only hints — a duplicate hint is harmless
// because the scheduler re-reads UnreadFor and MarkRead is idempotent.)
func TestReloadIdempotent(t *testing.T) {
	cfg := stubConfig(t)
	sp1, err := NewSpace("s", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #1: %v", err)
	}
	if _, err := sp1.Bus.Send(store.Message{Sender: "leader", Recipient: "worker-a", Body: "hi"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sp1.Shutdown()

	sp2, err := NewSpace("s", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #2: %v", err)
	}
	defer sp2.Shutdown()

	sp2.Reload()
	sp2.Reload()

	unread, err := sp2.Store.UnreadFor("worker-a")
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if len(unread) != 1 {
		t.Errorf("worker-a has %d unread after double Reload, want 1 (DB is idempotent truth)", len(unread))
	}
}
