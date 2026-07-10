package ui

import (
	"sync"

	"github.com/johnny1110/evva/pkg/event"
)

// EmitQueue decouples event producers from a UI's render loop: Push never
// blocks, and one pump goroutine delivers queued events to the sink in
// FIFO order once Start is called.
//
// It exists because the natural bubbletea sink — tea.Program.Send — is a
// handoff to an unbuffered channel: it blocks while the receive loop is
// busy (or not yet running), and producers routinely emit from inside
// critical sections the render path reads back. The dynamic-workflow
// engine is the canonical case: its dispatch sweep emits daemon/board
// changes while holding the engine mutex that the TUI's per-frame daemon
// snapshot needs — with an inline Send, producer and loop wait on each
// other and the whole TUI freezes. Routing Emit through an EmitQueue
// makes the producer side wait-free; only the pump ever blocks.
//
// The zero value is not usable; construct with NewEmitQueue.
type EmitQueue struct {
	sink func(event.Event)

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []event.Event
	started bool
	closed  bool
}

// NewEmitQueue returns a queue that delivers into sink. The sink runs on
// the pump goroutine and may block freely.
func NewEmitQueue(sink func(event.Event)) *EmitQueue {
	q := &EmitQueue{sink: sink}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push enqueues e and returns immediately. Safe from any goroutine,
// while holding any lock, before Start (events buffer until the pump
// runs), and after Close (dropped).
func (q *EmitQueue) Push(e event.Event) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.queue = append(q.queue, e)
	q.mu.Unlock()
	q.cond.Signal()
}

// Len reports how many events are queued and not yet handed to the sink.
func (q *EmitQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

// Start launches the pump. Events pushed before Start flush first, in
// order — the documented wiring constructs (and lets emit) the agent
// before the UI runs. Idempotent; a closed queue stays closed.
func (q *EmitQueue) Start() {
	q.mu.Lock()
	if q.started || q.closed {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.mu.Unlock()
	go q.pump()
}

// Close stops the pump and drops undelivered events — the UI they were
// destined for is gone. A batch already handed to the sink still finishes
// delivering (a dead tea.Program returns from Send immediately, so this
// does not wedge shutdown). Push after Close is a silent no-op.
func (q *EmitQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.queue = nil
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *EmitQueue) pump() {
	for {
		q.mu.Lock()
		for len(q.queue) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		batch := q.queue
		q.queue = nil
		q.mu.Unlock()
		for _, e := range batch {
			q.sink(e)
		}
	}
}
