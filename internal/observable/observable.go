// Package observable is the framework primitive that lets backing stores
// (task list, subagent panel, future "notes" / "todos" / ...) publish state
// changes through a single uniform stream.
//
// A store gains pub/sub by embedding Observable and satisfying Store. The
// owning container (toolset.ToolState) registers the store once, then any
// number of subscribers (the agent's event sink, persistence, tests) consume
// every store's changes through one Subscribe call.
//
// Payload is intentionally untyped at this layer. Each store publishes a
// domain-typed snapshot (task.Summary, meta.SubagentSnapshot, ...) in
// Change.Payload; consumers switch on Domain and type-assert. This trades a
// small amount of compile-time safety at the boundary for the ability to
// add a new domain without touching the event or agent packages at all.
package observable

import (
	"sync"
	"time"
)

// Change is a single state-change notification a Store emits to its
// observers.
//
// Domain identifies the emitting store ("task", "subagent", ...). Op names
// the verb ("created", "updated", "removed", "phase", ...). ID is the
// affected entity. Payload carries a domain-typed snapshot the consumer
// type-asserts on.
type Change struct {
	Domain  string
	Op      string
	ID      string
	Payload any
	Time    time.Time
}

// Observer(觀察者) is the callback shape Subscribe accepts. Observers run on the
// goroutine that called Notify and must not block — slow consumers buffer
// internally.
type Observer func(Change)

// Store is the contract every observable backing store satisfies. The
// Observable mixin below provides Subscribe and a Notify helper, so a
// store typically only needs to declare Domain().
type Store interface {
	Domain() string
	Subscribe(Observer) // Store 必須可被觀察
}

// Observable(被觀察者) is the embeddable pub/sub primitive. Zero value is ready to
// use; safe for concurrent Subscribe and Notify from any goroutine.
type Observable struct {
	mu        sync.RWMutex
	observers []Observer // func
}

// Subscribe appends fn to the observer list. nil fns are ignored. (被訂閱)
func (o *Observable) Subscribe(fn Observer) {
	if fn == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	o.observers = append(o.observers, fn)
}

// Notify fans c out to every subscriber. Time is filled in when zero. The
// observer slice is snapshotted under read-lock so a Subscribe racing with
// Notify is safe and observers receive a stable view.
func (o *Observable) Notify(c Change) {
	if c.Time.IsZero() {
		c.Time = time.Now()
	}
	o.mu.RLock()
	snapshotObservers := append([]Observer(nil), o.observers...)
	o.mu.RUnlock()
	// send change to observers
	for _, fn := range snapshotObservers {
		fn(c) // func(Change)
	}
}
