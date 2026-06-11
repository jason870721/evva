package store

import (
	"testing"
)

// RP-22: OldestUnread is the mailbox-backlog probe — per-recipient oldest
// unread AND unclaimed; claimed rows belong to an in-flight run (RP-14's
// territory) and read rows are done.
func TestOldestUnread(t *testing.T) {
	s := openTestStore(t)

	put := func(id, recipient string, createdAt int64) {
		t.Helper()
		if err := s.PutMessage(Message{ID: id, Sender: "x", Recipient: recipient, Body: "b", CreatedAt: createdAt}); err != nil {
			t.Fatalf("PutMessage(%s): %v", id, err)
		}
	}
	put("m1", "a", 100)
	put("m2", "a", 50) // older — must win for a
	put("m3", "b", 200)
	put("m4", "c", 10)

	if err := s.MarkRead("m4"); err != nil { // read rows drop out entirely
		t.Fatalf("MarkRead: %v", err)
	}

	got, err := s.OldestUnread()
	if err != nil {
		t.Fatalf("OldestUnread: %v", err)
	}
	if got["a"] != 50 || got["b"] != 200 {
		t.Errorf("oldest = %v, want a:50 b:200", got)
	}
	if _, ok := got["c"]; ok {
		t.Errorf("fully-read recipient must not appear: %v", got)
	}

	// Claiming a's batch moves it into an in-flight run — out of the probe.
	if _, err := s.ClaimUnread("a"); err != nil {
		t.Fatalf("ClaimUnread: %v", err)
	}
	got, err = s.OldestUnread()
	if err != nil {
		t.Fatalf("OldestUnread #2: %v", err)
	}
	if _, ok := got["a"]; ok {
		t.Errorf("claimed rows must be excluded: %v", got)
	}
	if got["b"] != 200 {
		t.Errorf("b should survive: %v", got)
	}
}
