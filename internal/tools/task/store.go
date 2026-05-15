package task

import (
	"fmt"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/observable"
)

// Domain is the observable.Change.Domain value every task-store change
// carries. Consumers switch on this string and type-assert Payload to
// task.Summary.
const Domain = "task"

// Status enumerates the lifecycle states a task can be in.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusDeleted    Status = "deleted"
)

// IsValid reports whether s is one of the canonical statuses. Callers feed
// model-supplied strings through this before assigning.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusCompleted, StatusDeleted:
		return true
	}
	return false
}

// Task is the in-memory record the task tools operate on.
//
// Fields mirror the LLM's task schema (see internal/tools/task/tool.go):
// Subject + Description carry the human title and body; ActiveForm is the
// spinner-friendly present-continuous variant ("Running tests").
// Blocks/BlockedBy carry the dependency graph; Metadata is opaque to the
// task subsystem itself.
type Task struct {
	ID          string
	Subject     string
	Description string
	ActiveForm  string
	Status      Status
	Owner       string
	Blocks      []string
	BlockedBy   []string
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Summary is the typed payload carried in observable.Change.Payload for
// every task-domain change. Consumers receive this in their observer
// callback after type-asserting on Domain == "task".
type Summary struct {
	Status  string
	Subject string
}

// TaskGroup is the per-agent backing store for the task tools. All six task
// tools (Create, Get, List, Update, Output, Stop) share one TaskGroup via
// constructor injection, so they cooperate without any global state.
//
// Safe for concurrent access. Mutations call Notify after releasing the
// lock so observers may freely call back into the store.
type TaskGroup struct {
	observable.Observable

	mu      sync.Mutex
	tasks   map[string]*Task
	order   []string // insertion order — drives stable List output
	counter int
}

// NewTaskGroup returns an empty TaskGroup ready for use.
func NewTaskGroup() *TaskGroup {
	return &TaskGroup{tasks: make(map[string]*Task)}
}

// Domain identifies this store on the change stream. Required by the
// observable.Store interface.
func (s *TaskGroup) Domain() string { return Domain }

// Create inserts a new task. The store assigns the ID (monotonic per-store,
// "t1", "t2", …) and timestamps; the caller supplies the other fields.
// Returns the inserted task with its assigned ID populated.
func (s *TaskGroup) Create(in Task) Task {
	s.mu.Lock()
	s.counter++
	now := time.Now()
	in.ID = fmt.Sprintf("t%d", s.counter)
	in.Status = StatusPending
	in.CreatedAt = now
	in.UpdatedAt = now
	t := in
	s.tasks[t.ID] = &t
	s.order = append(s.order, t.ID)
	s.mu.Unlock()

	s.Notify(observable.Change{
		Domain:  Domain,
		Op:      "created",
		ID:      t.ID,
		Payload: Summary{Status: string(t.Status), Subject: t.Subject},
	})
	return t
}

// Get returns a copy of the task with the given ID. Not-found returns the
// zero Task and ok=false; callers should treat that as a recoverable error
// (surface to the model as IsError, not abort the loop).
func (s *TaskGroup) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// List returns every task in insertion order, including deleted ones (so
// callers can audit the lifecycle). Callers that only want active tasks
// should filter by Status != StatusDeleted.
func (s *TaskGroup) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.order))
	for _, id := range s.order {
		if t, ok := s.tasks[id]; ok {
			out = append(out, *t)
		}
	}
	return out
}

// UpdatePatch carries optional field updates. Pointer fields preserve the
// "unset means leave alone" semantic so a partial update doesn't clobber
// fields the caller didn't mention.
type UpdatePatch struct {
	Status       *Status
	Subject      *string
	Description  *string
	ActiveForm   *string
	Owner        *string
	AddBlocks    []string
	AddBlockedBy []string
	Metadata     map[string]any // merged in (nil-value keys delete)
}

// Update applies the patch to the task with the given ID. Returns the
// post-update task or ok=false if no task with that ID exists. An unknown
// status value returns an error (and the task is left unchanged).
func (s *TaskGroup) Update(id string, p UpdatePatch) (Task, bool, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, false, nil
	}

	if p.Status != nil {
		if !p.Status.IsValid() {
			s.mu.Unlock()
			return Task{}, true, fmt.Errorf("invalid status %q", *p.Status)
		}
		t.Status = *p.Status
	}
	if p.Subject != nil {
		t.Subject = *p.Subject
	}
	if p.Description != nil {
		t.Description = *p.Description
	}
	if p.ActiveForm != nil {
		t.ActiveForm = *p.ActiveForm
	}
	if p.Owner != nil {
		t.Owner = *p.Owner
	}
	if len(p.AddBlocks) > 0 {
		t.Blocks = mergeStrings(t.Blocks, p.AddBlocks)
	}
	if len(p.AddBlockedBy) > 0 {
		t.BlockedBy = mergeStrings(t.BlockedBy, p.AddBlockedBy)
	}
	if p.Metadata != nil {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any, len(p.Metadata))
		}
		for k, v := range p.Metadata {
			if v == nil {
				delete(t.Metadata, k)
			} else {
				t.Metadata[k] = v
			}
		}
	}
	t.UpdatedAt = time.Now()
	snapshot := *t
	s.mu.Unlock()

	s.Notify(observable.Change{
		Domain:  Domain,
		Op:      "updated",
		ID:      snapshot.ID,
		Payload: Summary{Status: string(snapshot.Status), Subject: snapshot.Subject},
	})
	return snapshot, true, nil
}

// mergeStrings appends elements of b to a, skipping duplicates and empty
// strings. The result preserves a's order, then b's order for new entries.
func mergeStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	out := append([]string(nil), a...)
	for _, s := range b {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
