package service

import (
	"testing"
	"time"
)

// TestSendUserMessage_DeliveredAndDrained proves the flat-comms core: an
// operator message rides the ordinary bus + drain path, so an idle member is
// woken, processes it, and the message is marked read — with no new
// orchestration and the task ledger untouched (non-disruptive by construction).
func TestSendUserMessage_DeliveredAndDrained(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	if err := svc.SendUserMessage(id, "worker", "", "please report status"); err != nil {
		t.Fatalf("SendUserMessage: %v", err)
	}

	// The supervisor wakes worker; its drain reads the message and marks it
	// read after the run. Poll for that.
	deadline := time.Now().Add(5 * time.Second)
	var read bool
	for time.Now().Before(deadline) {
		msgs, _ := svc.Messages(id)
		for _, m := range msgs {
			if m.Sender == "user" && m.Recipient == "worker" && m.ReadAt != nil {
				read = true
			}
		}
		if read {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !read {
		t.Fatal("operator message to worker was never delivered + marked read")
	}

	// The ledger is untouched — a user message is not a task.
	if page, _ := svc.Tasks(id); len(page.Tasks) != 0 {
		t.Errorf("user message created %d tasks, want 0 (ledger is leader-only)", len(page.Tasks))
	}
}

// TestSendUserMessage_Errors covers the guard rails.
func TestSendUserMessage_Errors(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	if err := svc.SendUserMessage("nope", "worker", "", "hi"); err == nil {
		t.Error("unknown space should error")
	}
	if err := svc.SendUserMessage(id, "ghost", "", "hi"); err == nil {
		t.Error("unknown member should error")
	}
	if err := svc.SendUserMessage(id, "worker", "", "  "); err == nil {
		t.Error("empty body should error")
	}
}
