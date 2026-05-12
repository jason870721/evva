package task

import "sync"

// Status enumerates the lifecycle states a task can be in.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusDeleted    Status = "deleted"
)

// Task is the in-memory record the task tools operate on.
type Task struct {
	ID          string
	Title       string
	Description string
	Status      Status
}

// Store is the per-agent backing store for the task tools. All six task tools
// (Create, Get, List, Update, Output, Stop) share one Store via constructor
// injection, so they cooperate without any global state.
//
// Safe for concurrent access — the agent loop and TUI may read simultaneously.
// The fields are still unexported; once the tool methods land, the public
// surface will be the Store's own methods (Add/Update/etc).
type Store struct {
	mu    sync.Mutex
	tasks map[string]*Task
}

func NewStore() *Store {
	return &Store{tasks: make(map[string]*Task)}
}
