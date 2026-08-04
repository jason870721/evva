package toolset

import (
	"strings"
	"sync"
	"testing"
)

// texts flattens a drained batch to its bodies so the table below can
// assert on delivery order without restating the struct.
func texts(entries []PendingPrompt) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Text)
	}
	return out
}

type enq struct {
	level SteerLevel
	text  string
}

// TestDrainOrdering is STE-1's acceptance criterion: every arrival order ×
// level combination drains interjects-first with arrival order preserved
// inside each level — and a batch with no interject comes back in exactly
// the order it went in, which is the "byte-identical when no interject
// occurs" half of the criterion.
func TestDrainOrdering(t *testing.T) {
	tests := []struct {
		name string
		in   []enq
		want []string
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "all queued keeps arrival order",
			in:   []enq{{SteerQueue, "a"}, {SteerQueue, "b"}, {SteerQueue, "c"}},
			want: []string{"a", "b", "c"},
		},
		{
			name: "all interject keeps arrival order",
			in:   []enq{{SteerInterject, "a"}, {SteerInterject, "b"}},
			want: []string{"a", "b"},
		},
		{
			name: "interject last still lands first",
			in:   []enq{{SteerQueue, "a"}, {SteerQueue, "b"}, {SteerInterject, "urgent"}},
			want: []string{"urgent", "a", "b"},
		},
		{
			name: "interject first stays first",
			in:   []enq{{SteerInterject, "urgent"}, {SteerQueue, "a"}},
			want: []string{"urgent", "a"},
		},
		{
			name: "interleaved partitions but preserves within-level order",
			in: []enq{
				{SteerQueue, "q1"}, {SteerInterject, "i1"},
				{SteerQueue, "q2"}, {SteerInterject, "i2"},
				{SteerQueue, "q3"},
			},
			want: []string{"i1", "i2", "q1", "q2", "q3"},
		},
		{
			name: "blank prompts are dropped, not ordered",
			in:   []enq{{SteerQueue, ""}, {SteerInterject, "i"}, {SteerQueue, "q"}},
			want: []string{"i", "q"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := NewUserPromptQueue()
			for _, e := range tc.in {
				q.EnqueueAt(e.level, e.text)
			}
			// Pending must agree with Drain — a review panel that showed a
			// different order than the model receives would be a lie.
			if got, want := texts(q.Pending()), tc.want; !equal(got, want) {
				t.Errorf("Pending() = %v, want %v", got, want)
			}
			got := texts(q.Drain())
			if !equal(got, tc.want) {
				t.Errorf("Drain() = %v, want %v", got, tc.want)
			}
			if q.Len() != 0 {
				t.Errorf("Len() after drain = %d, want 0", q.Len())
			}
			if q.Drain() != nil {
				t.Error("second Drain() should be nil")
			}
		})
	}
}

// TestEnqueueDefaultsToQueueLevel pins the compatibility promise: the
// pre-STE Enqueue signature still means "polite".
func TestEnqueueDefaultsToQueueLevel(t *testing.T) {
	q := NewUserPromptQueue()
	q.Enqueue("hello")
	got := q.Pending()
	if len(got) != 1 {
		t.Fatalf("Pending() len = %d, want 1", len(got))
	}
	if got[0].Level != SteerQueue {
		t.Errorf("level = %v, want SteerQueue", got[0].Level)
	}
	if got[0].Level.String() != "queue" {
		t.Errorf("String() = %q, want %q", got[0].Level.String(), "queue")
	}
	if SteerInterject.String() != "interject" {
		t.Errorf("SteerInterject.String() = %q", SteerInterject.String())
	}
}

// TestRevoke covers the revoke path the /queue panel drives, including
// the losing side of the race with a drain.
func TestRevoke(t *testing.T) {
	q := NewUserPromptQueue()
	id1 := q.EnqueueAt(SteerQueue, "keep")
	id2 := q.EnqueueAt(SteerQueue, "drop")
	if id1 == "" || id2 == "" || id1 == id2 {
		t.Fatalf("ids not unique: %q %q", id1, id2)
	}
	if !q.Revoke(id2) {
		t.Fatal("Revoke(id2) = false, want true")
	}
	if q.Revoke(id2) {
		t.Error("second Revoke(id2) = true, want false")
	}
	if got := texts(q.Drain()); !equal(got, []string{"keep"}) {
		t.Errorf("after revoke Drain() = %v", got)
	}
	// Revoking after the drain consumed the entry is the "too late" case.
	if q.Revoke(id1) {
		t.Error("Revoke after drain = true, want false")
	}
}

// TestEnqueueDropsBlank keeps the empty-turn guard that predates STE-1.
func TestEnqueueDropsBlank(t *testing.T) {
	q := NewUserPromptQueue()
	if id := q.EnqueueAt(SteerInterject, ""); id != "" {
		t.Errorf("EnqueueAt(blank) = %q, want empty id", id)
	}
	if q.Len() != 0 {
		t.Errorf("Len() = %d, want 0", q.Len())
	}
}

// TestConcurrentEnqueueDrain is a race-detector target: the UI goroutine
// enqueues while the loop goroutine drains.
func TestConcurrentEnqueueDrain(t *testing.T) {
	q := NewUserPromptQueue()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			q.EnqueueAt(SteerLevel(i%2), "m")
		}
	}()
	drained := 0
	go func() {
		defer wg.Done()
		for range 200 {
			drained += len(q.Drain())
			_ = q.Pending()
			_ = q.Len()
		}
	}()
	wg.Wait()
	if drained+q.Len() != 200 {
		t.Errorf("drained %d + pending %d != 200", drained, q.Len())
	}
}

// TestPendingIsACopy guards the review panel: mutating what it renders
// must not reach into the live queue.
func TestPendingIsACopy(t *testing.T) {
	q := NewUserPromptQueue()
	q.Enqueue("original")
	snap := q.Pending()
	snap[0].Text = "tampered"
	if got := texts(q.Drain()); !equal(got, []string{"original"}) {
		t.Errorf("Drain() = %v, want [original]", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestIDsAreOpaqueButStable documents the id shape the UI keys rows on.
func TestIDsAreOpaqueButStable(t *testing.T) {
	q := NewUserPromptQueue()
	id := q.EnqueueAt(SteerQueue, "x")
	if !strings.HasPrefix(id, "p") {
		t.Errorf("id = %q, want a p-prefixed token", id)
	}
	if got := q.Pending()[0].ID; got != id {
		t.Errorf("Pending id = %q, want %q", got, id)
	}
}
