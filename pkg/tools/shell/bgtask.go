package shell

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/observable"
)

// BgTaskStatus is the lifecycle state of one detached Bash command.
//
// Transitions:
//
//	BgRunning → BgCompleted   (process exited with code 0)
//	BgRunning → BgFailed      (process exited with non-zero code)
//	BgRunning → BgKilled      (task_stop or root ctx cancelled)
//
// Terminal states are drained out of the store via DrainCompleted; their
// snapshots survive in the loop's iteration history as <system-reminder>
// blocks but are removed from the live store the moment they're folded in.
type BgTaskStatus string

const (
	BgRunning   BgTaskStatus = "running"
	BgCompleted BgTaskStatus = "completed"
	BgFailed    BgTaskStatus = "failed"
	BgKilled    BgTaskStatus = "killed"
)

// BgTaskDomain is the observable.Change.Domain value the store emits.
// Subscribers route renders by matching this on KindStoreUpdate.
const BgTaskDomain = "bg_tasks"

// BgTaskSnapshot is the public shape of one background task. The store
// hands out snapshots by value so observers don't race the goroutine
// holding the live struct.
type BgTaskSnapshot struct {
	ID          string
	Command     string
	Description string
	Status      BgTaskStatus
	ExitCode    int
	Output      string
	StartedAt   time.Time
	CompletedAt time.Time
	// AgentID is the agent that spawned this task — used by the TUI to
	// prefix subagent rows with their owner.
	AgentID string
}

// BgTask is the live record the store mutates. Cancel is the func that
// kills the underlying process (set by Bash before it spawns the
// goroutine); calling it from task_stop transitions the snapshot to
// BgKilled when the process exits.
type BgTask struct {
	BgTaskSnapshot
	// Cancel terminates the underlying process. nil for tasks that have
	// already exited.
	Cancel context.CancelFunc
}

// outputCap is the per-task captured-output ceiling. The bg goroutine
// trims to the trailing window plus a "[N bytes truncated]" header
// before storing the snapshot, mirroring the sync Bash path's behaviour.
const outputCap = 64 * 1024

// BgTaskStore is the agent-owned catalog of background tasks. Embedded
// Observable fans every Change to subscribers (the TUI strip + the
// agent's KindStoreUpdate bridge). The store is safe for concurrent use
// — every mutator takes mu.
//
// Lifecycle:
//   - Bash creates a snapshot via Add when spawning a bg task.
//   - The bg goroutine calls Complete / Fail when the process exits.
//   - task_stop calls Stop to flip Cancel and let the goroutine close out.
//   - The agent loop calls DrainCompleted at iter start to pull terminal
//     entries into the conversation; drained entries are removed.
type BgTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*BgTask
	*observable.Observable
}

// NewBgTaskStore returns an empty store. Construction is cheap; the
// embedded Observable allocates its observer slice lazily.
func NewBgTaskStore() *BgTaskStore {
	return &BgTaskStore{
		tasks:      map[string]*BgTask{},
		Observable: &observable.Observable{},
	}
}

// Domain returns the observable store domain. Implements observable.Store.
func (s *BgTaskStore) Domain() string { return BgTaskDomain }

// GenerateID returns a wire-stable "b" + 8 random base-36 characters,
// mirroring ref's generateTaskId for type=local_bash (b-prefixed IDs are
// recognisable in transcripts and don't collide with monitor IDs which
// use "m").
func GenerateID() string {
	const alpha = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	out := []byte{'b'}
	for _, b := range buf {
		out = append(out, alpha[int(b)%len(alpha)])
	}
	return string(out)
}

// Add registers a freshly-spawned task. Status MUST be BgRunning. Cancel
// is the func that kills the bg process (called by Stop). Add emits a
// "started" observable.Change.
func (s *BgTaskStore) Add(snap BgTaskSnapshot, cancel context.CancelFunc) {
	if snap.ID == "" {
		return
	}
	s.mu.Lock()
	s.tasks[snap.ID] = &BgTask{BgTaskSnapshot: snap, Cancel: cancel}
	s.mu.Unlock()
	s.Notify(observable.Change{
		Domain:  BgTaskDomain,
		Op:      "started",
		ID:      snap.ID,
		Payload: snap,
	})
}

// Complete transitions the task to a terminal state (BgCompleted /
// BgFailed / BgKilled) with the captured output, exit code, and finish
// time. The Cancel func is cleared so task_stop on a finished task is a
// clean no-op. Emits an Op matching the terminal status so subscribers
// can render distinct outcomes.
func (s *BgTaskStore) Complete(id string, status BgTaskStatus, exitCode int, output string) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	t.Status = status
	t.ExitCode = exitCode
	t.Output = capOutput(output)
	t.CompletedAt = time.Now()
	t.Cancel = nil
	snap := t.BgTaskSnapshot
	s.mu.Unlock()
	s.Notify(observable.Change{
		Domain:  BgTaskDomain,
		Op:      string(status),
		ID:      id,
		Payload: snap,
	})
}

// Stop signals the named task to terminate. Returns ok=true when the
// task was running and a cancel was invoked, ok=false when the task is
// unknown or already terminal (task_stop surfaces this as a no-op).
func (s *BgTaskStore) Stop(id string) (BgTaskSnapshot, bool) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok || t.Status != BgRunning || t.Cancel == nil {
		var snap BgTaskSnapshot
		if ok {
			snap = t.BgTaskSnapshot
		}
		s.mu.Unlock()
		return snap, false
	}
	cancel := t.Cancel
	snap := t.BgTaskSnapshot
	s.mu.Unlock()
	cancel() // outside the lock; the bg goroutine will call Complete with BgKilled
	return snap, true
}

// Get returns a snapshot of one task. ok=false when unknown.
func (s *BgTaskStore) Get(id string) (BgTaskSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return BgTaskSnapshot{}, false
	}
	return t.BgTaskSnapshot, true
}

// Snapshot returns every task in started-at order. Used by the TUI
// strip; safe to call from any goroutine.
func (s *BgTaskStore) Snapshot() []BgTaskSnapshot {
	s.mu.RLock()
	out := make([]BgTaskSnapshot, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t.BgTaskSnapshot)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// HasPending reports whether any task is in a terminal state but has
// not been drained yet. The agent loop calls this before returning from
// a terminal turn — pending entries force one more iteration so the
// model sees the result before the loop releases the run flag.
func (s *BgTaskStore) HasPending() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tasks {
		if t.Status != BgRunning {
			return true
		}
	}
	return false
}

// DrainCompleted pulls every terminal task out of the store, returning
// snapshots in completion order. The store emits a "removed" Change for
// each drained task so the TUI strip can render the "task-xxx completed"
// transcript line before the chip disappears.
//
// Running tasks stay untouched — the next drain picks them up when they
// finish.
func (s *BgTaskStore) DrainCompleted() []BgTaskSnapshot {
	s.mu.Lock()
	drained := make([]BgTaskSnapshot, 0)
	for id, t := range s.tasks {
		if t.Status == BgRunning {
			continue
		}
		drained = append(drained, t.BgTaskSnapshot)
		delete(s.tasks, id)
	}
	s.mu.Unlock()
	sort.Slice(drained, func(i, j int) bool { return drained[i].CompletedAt.Before(drained[j].CompletedAt) })
	for _, snap := range drained {
		s.Notify(observable.Change{
			Domain:  BgTaskDomain,
			Op:      "removed",
			ID:      snap.ID,
			Payload: snap,
		})
	}
	return drained
}

// capOutput trims s to the trailing outputCap bytes with a one-line
// truncation header. Mirrors the sync Bash path's behaviour so a 64 KiB
// model-facing window is the implicit contract for both paths.
func capOutput(s string) string {
	if len(s) <= outputCap {
		return s
	}
	trimmed := s[len(s)-outputCap:]
	return fmt.Sprintf("[bg output capped — %d bytes truncated from head]\n%s", len(s)-outputCap, trimmed)
}

// BgTaskHost is the narrow surface every bg-tasks-aware tool (Bash with
// run_in_background, task_list, task_output, task_stop) reads. The host
// is implemented by *toolset.ToolState; the tool type-asserts on it at
// the start of Execute, same pattern the SKILL tool uses for its
// registry.
type BgTaskHost interface {
	// BgTaskStore returns the agent's background-task catalog. Never nil
	// for the host that the agent installs.
	BgTaskStore() *BgTaskStore
	// RootCtx returns the agent-lifetime context. Bg goroutines bind to
	// this rather than the per-call ctx so they survive the LLM call
	// that spawned them.
	RootCtx() context.Context
	// AgentID returns the spawning agent's id; copied into the snapshot
	// so the TUI can label rows by owner.
	AgentID() string
	// NotifyBgResult fires the agent's signal pump with this terminal
	// snapshot. Non-blocking; drops the signal if the chan buffer is
	// full (drain on next iter is the fallback).
	NotifyBgResult(snap BgTaskSnapshot)
}
