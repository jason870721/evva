package bus

import (
	"errors"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// fakeRoster is a fixed Membership view: ActiveMembers returns exactly `active`,
// so a name absent from it stands in for a frozen/inactive member.
type fakeRoster struct{ active []string }

func (f fakeRoster) ActiveMembers() []string { return f.active }

func newTestBus(t *testing.T, active ...string) (*Bus, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, fakeRoster{active: active}), st
}

// recv pulls one UUID off a mailbox, failing if none arrives promptly.
func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a mailbox UUID")
		return ""
	}
}

// requireEmpty asserts a mailbox has no UUID waiting.
func requireEmpty(t *testing.T, name string, ch <-chan string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("mailbox %q should be empty, got %q", name, v)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestSendExternalDedup (RP-9): a keyed external event delivers + signals once; a
// retry under the same key returns the original id, dup=true, and does NOT
// re-signal (no second wake). A keyless send is plain delivery.
func TestSendExternalDedup(t *testing.T) {
	b, st := newTestBus(t, "leader")
	b.Register("leader")
	ch := b.Inbox("leader")

	id, dup, err := b.SendExternal(store.Message{Sender: "webhook", Recipient: "leader", Body: "evt"}, "k1")
	if err != nil || dup || id == "" {
		t.Fatalf("first = id:%q dup:%v err:%v, want fresh delivery", id, dup, err)
	}
	if got := recv(t, ch); got != id {
		t.Fatalf("signal = %q, want %q", got, id)
	}
	if _, err := st.GetMessage(id); err != nil {
		t.Fatalf("event row not durable: %v", err)
	}

	// Retry same key → same id, dup, no new signal.
	id2, dup2, err := b.SendExternal(store.Message{Sender: "webhook", Recipient: "leader", Body: "retry"}, "k1")
	if err != nil || !dup2 || id2 != id {
		t.Fatalf("retry = id:%q dup:%v err:%v, want same id + dup", id2, dup2, err)
	}
	requireEmpty(t, "leader", ch)

	// Keyless still delivers + signals.
	id3, dup3, err := b.SendExternal(store.Message{Sender: "webhook", Recipient: "leader", Body: "no key"}, "")
	if err != nil || dup3 || id3 == "" {
		t.Fatalf("keyless = id:%q dup:%v err:%v", id3, dup3, err)
	}
	if got := recv(t, ch); got != id3 {
		t.Fatalf("keyless signal = %q, want %q", got, id3)
	}
}

// AC #1 (persist-before-signal, §6.2) + #2 (single delivery, correct fields):
// the row is readable the instant its UUID arrives, with sender/recipient/body
// intact.
func TestSendPersistsBeforeSignalAndDelivers(t *testing.T) {
	b, st := newTestBus(t, "a", "b")
	b.Register("b")

	uuid, err := b.Send(store.Message{Sender: "a", Recipient: "b", Subject: "hi", Body: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	got := recv(t, b.Inbox("b"))
	if got != uuid {
		t.Fatalf("inbox UUID = %q, want %q", got, uuid)
	}

	// The row must already exist — if signal could outrun PutMessage this would
	// be ErrMessageNotFound.
	m, err := st.GetMessage(got)
	if err != nil {
		t.Fatalf("row %s missing when UUID arrived (persist-before-signal violated): %v", got, err)
	}
	if m.Sender != "a" || m.Recipient != "b" || m.Body != "hello" {
		t.Fatalf("row = %+v, want sender=a recipient=b body=hello", m)
	}
}

// AC #3: a broadcast reaches every active peer (one distinct row each) and
// neither the sender (no self-wake) nor a frozen member (absent from
// ActiveMembers, even though it owns a mailbox).
func TestBroadcastReachesActivePeersOnly(t *testing.T) {
	b, st := newTestBus(t, "leader", "a", "b") // "c" is frozen: not active
	for _, n := range []string{"leader", "a", "b", "c"} {
		b.Register(n)
	}

	if _, err := b.Send(store.Message{Sender: "leader", Recipient: store.RecipientAll, Body: "team"}); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	ua := recv(t, b.Inbox("a"))
	ub := recv(t, b.Inbox("b"))
	if ua == ub {
		t.Fatalf("broadcast must mint a distinct UUID per recipient, got %q twice", ua)
	}
	requireEmpty(t, "leader (sender)", b.Inbox("leader"))
	requireEmpty(t, "c (frozen)", b.Inbox("c"))

	for recipient, u := range map[string]string{"a": ua, "b": ub} {
		m, err := st.GetMessage(u)
		if err != nil {
			t.Fatalf("get %s: %v", u, err)
		}
		if m.Recipient != recipient || m.Sender != "leader" || m.Body != "team" {
			t.Fatalf("row = %+v, want sender=leader recipient=%s body=team", m, recipient)
		}
	}
}

// AC #4: Requeue re-pushes a list (store.UnreadFor's created_at order) onto a
// mailbox, preserving order.
func TestRequeuePreservesOrder(t *testing.T) {
	b, _ := newTestBus(t)
	b.Register("x")

	want := []string{"u1", "u2", "u3"}
	b.Requeue("x", want)

	in := b.Inbox("x")
	for i, w := range want {
		if got := recv(t, in); got != w {
			t.Fatalf("requeue[%d] = %q, want %q", i, got, w)
		}
	}
}

// AC #5: a full mailbox never deadlocks Send, and every row is still persisted
// (the overflow is recoverable via the DB; only the chan hint is dropped).
func TestSendNonBlockingUnderFullBuffer(t *testing.T) {
	b, st := newTestBus(t)
	b.Register("x")

	const overflow = 5
	total := inboxBuffer + overflow

	ids := make([]string, 0, total)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			id, err := b.Send(store.Message{Sender: "s", Recipient: "x", Body: "m"})
			if err != nil {
				t.Errorf("send %d: %v", i, err)
				return
			}
			ids = append(ids, id)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Send blocked on a full mailbox buffer (deadlock)")
	}

	if len(ids) != total {
		t.Fatalf("persisted %d ids, want %d", len(ids), total)
	}
	for _, id := range ids {
		if _, err := st.GetMessage(id); err != nil {
			t.Fatalf("row %s not persisted under buffer pressure: %v", id, err)
		}
	}
	if got := len(b.Inbox("x")); got != inboxBuffer {
		t.Fatalf("mailbox holds %d, want buffer cap %d (rest recoverable via DB)", got, inboxBuffer)
	}
}

// AC #6: two spaces' buses with identical agent names never cross-deliver — the
// row lands only in the sending space's store and mailbox.
func TestTwoSpacesDoNotCrossDeliver(t *testing.T) {
	busA, _ := newTestBus(t, "leader")
	busB, stB := newTestBus(t, "leader")
	busA.Register("leader")
	busB.Register("leader")

	uuid, err := busA.Send(store.Message{Sender: "x", Recipient: "leader", Body: "A-only"})
	if err != nil {
		t.Fatalf("send on A: %v", err)
	}

	if got := recv(t, busA.Inbox("leader")); got != uuid {
		t.Fatalf("space A inbox = %q, want %q", got, uuid)
	}
	requireEmpty(t, "space B leader", busB.Inbox("leader"))
	if _, err := stB.GetMessage(uuid); !errors.Is(err, store.ErrMessageNotFound) {
		t.Fatalf("space B store should not have A's row; got err = %v", err)
	}
}

// Register is idempotent: re-registering an active member keeps its mailbox and
// any UUIDs already queued on it rather than minting a fresh empty channel.
func TestRegisterIdempotentKeepsQueuedMail(t *testing.T) {
	b, _ := newTestBus(t)
	b.Register("x")

	uuid, err := b.Send(store.Message{Sender: "s", Recipient: "x", Body: "queued"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	b.Register("x") // must not drop the queued UUID

	if got := recv(t, b.Inbox("x")); got != uuid {
		t.Fatalf("after re-register, inbox = %q, want preserved %q", got, uuid)
	}
}

// Deregister drops the mailbox (Inbox -> nil) but Send to a deregistered name
// still persists the row (no panic), recoverable on re-register.
func TestDeregisterDropsMailboxButPersists(t *testing.T) {
	b, st := newTestBus(t)
	b.Register("x")
	b.Deregister("x")

	if b.Inbox("x") != nil {
		t.Fatal("Inbox of a deregistered name should be nil")
	}

	uuid, err := b.Send(store.Message{Sender: "s", Recipient: "x", Body: "durable"})
	if err != nil {
		t.Fatalf("send to deregistered recipient: %v", err)
	}
	if _, err := st.GetMessage(uuid); err != nil {
		t.Fatalf("row should persist despite no mailbox: %v", err)
	}
}
