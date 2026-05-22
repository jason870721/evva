package monitor

import (
	"context"
	"crypto/rand"
	"sort"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/observable"
)

// MonitorStatus is the lifecycle state of one Monitor task. Distinct from
// shell.BgTaskStatus because monitor tasks can be long-lived (persistent
// loops) and don't carry a single exit code — they accumulate events.
//
// Transitions:
//
//	Monitoring → Stopped   (process exited cleanly OR task_stop called)
//	Monitoring → Failed    (process spawn failed)
type MonitorStatus string

const (
	Monitoring MonitorStatus = "monitoring"
	Stopped    MonitorStatus = "stopped"
	Failed     MonitorStatus = "failed"
)

// MonitorDomain is the observable.Change.Domain for monitor task changes.
const MonitorDomain = "monitors"

// MonitorTaskSnapshot is the public shape of one monitor entry. The
// event count is read at snapshot time; events themselves live in the
// per-agent MonitorEventQueue, not on the snapshot.
type MonitorTaskSnapshot struct {
	ID          string
	Command     string
	Description string
	Status      MonitorStatus
	EventCount  int
	StartedAt   time.Time
	StoppedAt   time.Time
	AgentID     string
}

// MonitorTask is the live record the store mutates. Cancel terminates
// the underlying process — task_stop calls it; the goroutine then
// transitions the snapshot to Stopped.
type MonitorTask struct {
	MonitorTaskSnapshot
	Cancel context.CancelFunc
}

// MonitorEvent is one stdout line streamed by a running monitor.
// Closing is true on the final event when the underlying process exits
// (or task_stop is called) — drain folds the closing event into the
// system-reminder and the TUI strip flips the chip to Stopped.
type MonitorEvent struct {
	MonitorID string
	Line      string
	At        time.Time
	Closing   bool
}

// MonitorEventQueue is the FIFO queue of streamed lines from every
// running monitor. The agent's drainMonitorEvents helper pulls events
// at iter start and folds them into a single <system-reminder> RoleUser
// message; events are never persisted beyond the drain.
type MonitorEventQueue struct {
	mu     sync.Mutex
	events []MonitorEvent
}

// NewMonitorEventQueue returns an empty queue.
func NewMonitorEventQueue() *MonitorEventQueue { return &MonitorEventQueue{} }

// Enqueue pushes one event. Callers (monitor goroutines) call this
// before signalling the agent so the drain at iter start sees the
// event regardless of how the pump's CAS races.
func (q *MonitorEventQueue) Enqueue(ev MonitorEvent) {
	q.mu.Lock()
	q.events = append(q.events, ev)
	q.mu.Unlock()
}

// Drain returns every queued event in arrival order and clears the
// queue. Returns nil when empty so the drain helper can short-circuit.
func (q *MonitorEventQueue) Drain() []MonitorEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return nil
	}
	out := q.events
	q.events = nil
	return out
}

// HasPending reports whether the queue carries any undrained events.
// Cheap mu-read used by the agent loop's end-of-turn re-check.
func (q *MonitorEventQueue) HasPending() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.events) > 0
}

// MonitorTaskStore is the agent-owned catalog of monitor tasks. Same
// shape as shell.BgTaskStore: embedded Observable for TUI fanout,
// internal mutex, lifecycle methods (Add / Complete / Stop) +
// query methods (Get / Snapshot).
//
// Unlike BgTaskStore there is no DrainCompleted — monitors don't fold
// their snapshot into the conversation when they stop. The per-monitor
// closing event (Closing:true) carries that signal; the snapshot just
// transitions to Stopped/Failed and stays in the store until the
// session ends.
type MonitorTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*MonitorTask
	*observable.Observable
}

// NewMonitorTaskStore returns an empty store.
func NewMonitorTaskStore() *MonitorTaskStore {
	return &MonitorTaskStore{
		tasks:      map[string]*MonitorTask{},
		Observable: &observable.Observable{},
	}
}

// Domain returns the observable store domain. Implements observable.Store.
func (s *MonitorTaskStore) Domain() string { return MonitorDomain }

// GenerateID returns "m" + 8 random base-36 chars. Distinct prefix from
// shell.GenerateID's "b" so a transcript can disambiguate at a glance.
func GenerateID() string {
	const alpha = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	out := []byte{'m'}
	for _, b := range buf {
		out = append(out, alpha[int(b)%len(alpha)])
	}
	return string(out)
}

// Add registers a freshly-spawned monitor. cancel kills the underlying
// process on task_stop or root-ctx cancel. Emits a "started" Change so
// the strip renders the chip immediately.
func (s *MonitorTaskStore) Add(snap MonitorTaskSnapshot, cancel context.CancelFunc) {
	if snap.ID == "" {
		return
	}
	s.mu.Lock()
	s.tasks[snap.ID] = &MonitorTask{MonitorTaskSnapshot: snap, Cancel: cancel}
	s.mu.Unlock()
	s.Notify(observable.Change{
		Domain:  MonitorDomain,
		Op:      "started",
		ID:      snap.ID,
		Payload: snap,
	})
}

// IncEventCount bumps the event counter for a running monitor. Called
// by the monitor goroutine each time it streams a line. Emits an
// "event" Change for the strip to flicker.
func (s *MonitorTaskStore) IncEventCount(id string) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	t.EventCount++
	snap := t.MonitorTaskSnapshot
	s.mu.Unlock()
	s.Notify(observable.Change{
		Domain:  MonitorDomain,
		Op:      "event",
		ID:      id,
		Payload: snap,
	})
}

// Complete transitions the monitor to a terminal state (Stopped /
// Failed). Clears Cancel so task_stop on a finished monitor is a clean
// no-op. Emits a Change matching the terminal Op for renderers.
func (s *MonitorTaskStore) Complete(id string, status MonitorStatus) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	t.Status = status
	t.StoppedAt = time.Now()
	t.Cancel = nil
	snap := t.MonitorTaskSnapshot
	s.mu.Unlock()
	s.Notify(observable.Change{
		Domain:  MonitorDomain,
		Op:      string(status),
		ID:      id,
		Payload: snap,
	})
}

// Stop signals the named monitor to terminate. ok=false when unknown
// or already terminal.
func (s *MonitorTaskStore) Stop(id string) (MonitorTaskSnapshot, bool) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok || t.Status != Monitoring || t.Cancel == nil {
		var snap MonitorTaskSnapshot
		if ok {
			snap = t.MonitorTaskSnapshot
		}
		s.mu.Unlock()
		return snap, false
	}
	cancel := t.Cancel
	snap := t.MonitorTaskSnapshot
	s.mu.Unlock()
	cancel()
	return snap, true
}

// Get returns one task snapshot. ok=false when unknown.
func (s *MonitorTaskStore) Get(id string) (MonitorTaskSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return MonitorTaskSnapshot{}, false
	}
	return t.MonitorTaskSnapshot, true
}

// Snapshot returns every monitor in started-at order. Used by the TUI
// strip.
func (s *MonitorTaskStore) Snapshot() []MonitorTaskSnapshot {
	s.mu.RLock()
	out := make([]MonitorTaskSnapshot, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t.MonitorTaskSnapshot)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// MonitorHost is the narrow surface a MonitorTool reads from its host
// (*toolset.ToolState in production). Mirrors shell.BgTaskHost so the
// two implementations stay parallel.
type MonitorHost interface {
	// MonitorTaskStore returns the agent's monitor catalog.
	MonitorTaskStore() *MonitorTaskStore
	// MonitorEventQueue returns the per-agent queue every monitor
	// streams events into.
	MonitorEventQueue() *MonitorEventQueue
	// RootCtx returns the agent-lifetime context; monitor goroutines
	// bind here, not the per-call ctx.
	RootCtx() context.Context
	// AgentID is the spawning agent's id.
	AgentID() string
	// NotifyMonitorEvent fires the agent's signal pump for one streamed
	// event. Non-blocking; the queue is the durable backstop.
	NotifyMonitorEvent(ev MonitorEvent)
}
