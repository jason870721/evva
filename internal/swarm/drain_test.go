package swarm

import (
	"context"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// TestInboxDrainerClaimsThenSettles: the swarm drainer pulls a delivered message
// off the mailbox, formats it, and CLAIMS it (claimed_at set, read_at still nil —
// drain B no longer eagerly marks read). A second poll is empty (a claimed
// message is not re-folded), and the supervisor's SettleClaimed — called on a
// clean run end — is what finally stamps read_at. This split is what lets a
// folded-then-aborted message be recovered instead of silently lost (RP-1).
func TestInboxDrainerClaimsThenSettles(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("s", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	d := newInboxDrainer("worker-a", sp.Bus, sp.Store)

	// Empty inbox → non-blocking no-op.
	if _, ok := d.Drain(context.Background()); ok {
		t.Fatal("drain of an empty mailbox should return ok=false")
	}

	uuid, err := sp.Bus.Send(store.Message{Sender: "leader", Recipient: "worker-a", Subject: "halt", Body: "stop now"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, ok := d.Drain(context.Background())
	if !ok {
		t.Fatal("drain should have returned the delivered message")
	}
	if !strings.Contains(msg, "leader") || !strings.Contains(msg, "stop now") || !strings.Contains(msg, "halt") {
		t.Errorf("formatted message = %q, want sender/subject/body", msg)
	}

	got, err := sp.Store.GetMessage(uuid)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if got.ClaimedAt == nil {
		t.Error("drained message was not claimed")
	}
	if got.ReadAt != nil {
		t.Error("drain B must only claim, not mark read (settle happens on clean run end)")
	}

	// Nothing left to drain — a claimed message is not re-folded.
	if _, ok := d.Drain(context.Background()); ok {
		t.Error("second drain should be empty")
	}

	// The supervisor settles claims on a clean run end → now it's read.
	if err := sp.Store.SettleClaimed("worker-a"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if got, _ := sp.Store.GetMessage(uuid); got.ReadAt == nil {
		t.Error("SettleClaimed should stamp read_at on the claimed message")
	}
}

// TestInboxDrainerSkipsAlreadyRead: a hint for a message already consumed by
// drain A (marked read) is skipped, so no message is folded twice.
func TestInboxDrainerSkipsAlreadyRead(t *testing.T) {
	cfg := stubConfig(t)
	sp, err := NewSpace("s", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	uuid, err := sp.Bus.Send(store.Message{Sender: "leader", Recipient: "worker-a", Body: "already handled"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := sp.Store.MarkRead(uuid); err != nil { // drain A got here first
		t.Fatalf("mark read: %v", err)
	}

	d := newInboxDrainer("worker-a", sp.Bus, sp.Store)
	if _, ok := d.Drain(context.Background()); ok {
		t.Error("drainer should skip a message that is already read")
	}
}
