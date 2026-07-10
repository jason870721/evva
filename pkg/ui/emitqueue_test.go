package ui_test

import (
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
)

func ev(i int) event.Event {
	return event.Event{Kind: event.KindStoreUpdate, AgentID: strconv.Itoa(i)}
}

// The reason EmitQueue exists: producers must never block on a stalled
// consumer. The sink here wedges on its first delivery — exactly what
// tea.Program.Send does while the Update loop is busy — and every Push
// must still return. Pre-fix, the inline-Send equivalent wedged the agent
// goroutine mid-dispatch and deadlocked the whole TUI.
func TestEmitQueuePushNeverBlocksOnStalledSink(t *testing.T) {
	block := make(chan struct{})
	q := ui.NewEmitQueue(func(event.Event) { <-block })
	q.Start()
	t.Cleanup(func() { q.Close(); close(block) })

	done := make(chan struct{})
	go func() {
		for i := range 256 {
			q.Push(ev(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Push blocked on a stalled sink — TUI deadlock regression")
	}
}

// Events pushed before Start (the wiring constructs the agent before the
// UI runs) flush first; later pushes follow. One pump ⇒ FIFO throughout.
func TestEmitQueueFIFOAcrossStart(t *testing.T) {
	var mu sync.Mutex
	var got []string
	all := make(chan struct{})
	q := ui.NewEmitQueue(func(e event.Event) {
		mu.Lock()
		got = append(got, e.AgentID)
		n := len(got)
		mu.Unlock()
		if n == 6 {
			close(all)
		}
	})
	for i := range 3 {
		q.Push(ev(i))
	}
	if q.Len() != 3 {
		t.Fatalf("Len = %d before Start, want 3 buffered", q.Len())
	}
	q.Start()
	for i := 3; i < 6; i++ {
		q.Push(ev(i))
	}
	select {
	case <-all:
	case <-time.After(2 * time.Second):
		t.Fatal("queued events were not delivered")
	}
	q.Close()
	mu.Lock()
	defer mu.Unlock()
	want := []string{"0", "1", "2", "3", "4", "5"}
	if !slices.Equal(got, want) {
		t.Fatalf("delivery order = %v, want %v", got, want)
	}
}

// Close drops what the pump has not yet taken and stops delivery; a
// wedged in-flight batch may finish, queued-behind events must not.
func TestEmitQueueCloseStopsDelivery(t *testing.T) {
	entered := make(chan struct{}, 1)
	block := make(chan struct{})
	var delivered atomic.Int32
	q := ui.NewEmitQueue(func(event.Event) {
		delivered.Add(1)
		entered <- struct{}{}
		<-block
	})
	q.Start()
	q.Push(ev(0))
	<-entered // sink is wedged on event 0
	q.Push(ev(1))
	q.Close()
	close(block) // unwedge; the pump must exit without delivering event 1
	time.Sleep(50 * time.Millisecond)
	if n := delivered.Load(); n != 1 {
		t.Fatalf("delivered %d events, want 1 (Close drops undelivered)", n)
	}
	q.Push(ev(2)) // after Close: silent no-op
	if q.Len() != 0 {
		t.Fatal("Push after Close must not queue")
	}
}
