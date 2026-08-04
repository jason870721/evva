package bus

import (
	"sync"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// urgentRecorder captures the (recipient, sender) pairs the bus nudged.
type urgentRecorder struct {
	mu    sync.Mutex
	pairs [][2]string
}

func (r *urgentRecorder) hook(recipient, sender string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pairs = append(r.pairs, [2]string{recipient, sender})
}

func (r *urgentRecorder) snapshot() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.pairs...)
}

// TestUrgentHookFiresOnlyForInterject is STE-5's safety property: an
// unrecognised urgency must never be MORE disruptive than the default, so
// anything but the exact word leaves the recipient's work alone.
func TestUrgentHookFiresOnlyForInterject(t *testing.T) {
	for _, tc := range []struct {
		urgency string
		want    bool
	}{
		{"", false},
		{store.UrgencyNormal, false},
		{store.UrgencyInterject, true},
		{"URGENT", false},
		{"high", false},
		{"Interject", false}, // canonicalisation is the tool's job, not the bus's
	} {
		t.Run("urgency="+tc.urgency, func(t *testing.T) {
			b, _ := newTestBus(t, "worker")
			b.Register("worker")
			rec := &urgentRecorder{}
			b.SetUrgentHook(rec.hook)

			if _, err := b.Send(store.Message{
				Sender: "lead", Recipient: "worker", Body: "stop", Urgency: tc.urgency,
			}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			got := len(rec.snapshot()) == 1
			if got != tc.want {
				t.Errorf("hook fired = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUrgentHookCarriesSender: the recipient's honesty note names who cut in,
// so the sender has to survive the trip.
func TestUrgentHookCarriesSender(t *testing.T) {
	b, _ := newTestBus(t, "worker")
	b.Register("worker")
	rec := &urgentRecorder{}
	b.SetUrgentHook(rec.hook)

	if _, err := b.Send(store.Message{
		Sender: "lead", Recipient: "worker", Body: "stop", Urgency: store.UrgencyInterject,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	pairs := rec.snapshot()
	if len(pairs) != 1 || pairs[0] != [2]string{"worker", "lead"} {
		t.Errorf("pairs = %v, want [[worker lead]]", pairs)
	}
}

// TestUrgentBroadcastNudgesEveryPeer — a "stop everything" broadcast has to
// reach every peer's in-flight work, not just the first.
func TestUrgentBroadcastNudgesEveryPeer(t *testing.T) {
	b, _ := newTestBus(t, "lead", "w1", "w2")
	for _, n := range []string{"lead", "w1", "w2"} {
		b.Register(n)
	}
	rec := &urgentRecorder{}
	b.SetUrgentHook(rec.hook)

	if _, err := b.Send(store.Message{
		Sender: "lead", Recipient: store.RecipientAll, Body: "halt", Urgency: store.UrgencyInterject,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	pairs := rec.snapshot()
	if len(pairs) != 2 {
		t.Fatalf("nudged %d peers, want 2 (the sender is skipped): %v", len(pairs), pairs)
	}
	for _, p := range pairs {
		if p[0] == "lead" {
			t.Error("a broadcast must not interject its own sender")
		}
	}
}

// TestUrgencyRoundTripsThroughTheStore: the column has to survive the write,
// or the /messages view and the event log would disagree with what happened.
func TestUrgencyRoundTripsThroughTheStore(t *testing.T) {
	b, st := newTestBus(t, "worker")
	b.Register("worker")

	id, err := b.Send(store.Message{
		Sender: "lead", Recipient: "worker", Body: "stop", Urgency: store.UrgencyInterject,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := st.GetMessage(id)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Urgency != store.UrgencyInterject {
		t.Errorf("stored urgency = %q, want %q", got.Urgency, store.UrgencyInterject)
	}
}

// TestNoHookInstalledIsSafe — the space installs one, but a bus built bare
// (tests, a lite space) must still deliver.
func TestNoHookInstalledIsSafe(t *testing.T) {
	b, _ := newTestBus(t, "worker")
	b.Register("worker")
	id, err := b.Send(store.Message{
		Sender: "lead", Recipient: "worker", Body: "stop", Urgency: store.UrgencyInterject,
	})
	if err != nil {
		t.Fatalf("Send with no hook: %v", err)
	}
	if id == "" {
		t.Error("message not delivered")
	}
}
