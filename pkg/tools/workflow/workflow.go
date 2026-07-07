// Package workflow owns the solo dynamic-workflow task board: a dependency
// graph of tasks the root agent plans at runtime and an append-only session
// log that makes the board restart-safe. The wf_task_* tools mutate it as
// the "root" actor; the in-process dispatch engine (internal/agent) mutates
// it as the "system" actor, executing structure the root already declared —
// the writer matrix in store.go is the load-bearing wall.
//
// The package is deliberately free of agent imports so downstream embedders
// can reuse the board with their own dispatch loop. See
// docs/roadmap/PRD/solo-dynamic-workflow.md for the design; the execution
// model is the swarm DWF wave's (docs/roadmap/PRD/swarm-dynamic-workflow.md)
// ported to solo.
package workflow

import "time"

// Domain is the observable.Change.Domain value every board change carries.
// Consumers switch on this string and type-assert Payload to []workflow.Task.
const Domain = "workflow"

// Status enumerates the board's task states. `blocked` is birth-only (a task
// created with incomplete dependencies) plus the force-unblock escape;
// `verifying` holds a worker's recorded result awaiting judgment. There is
// no suspended and no failed — a worker failure lands in verifying with
// WorkerFailed set, and abandonment is deletion or simply never verifying.
type Status string

const (
	StatusBlocked   Status = "blocked"
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusVerifying Status = "verifying"
	StatusCompleted Status = "completed"
)

// IsValid reports whether s is one of the canonical statuses.
func (s Status) IsValid() bool {
	switch s {
	case StatusBlocked, StatusPending, StatusRunning, StatusVerifying, StatusCompleted:
		return true
	}
	return false
}

// Verify is the per-task verification policy, fixed at creation. "leader"
// (the default) folds the worker's result into the root conversation for
// judgment; "auto" lets the engine complete the task mechanically and
// cascade dependents with zero root wakes. Stored as text so a future
// machine-evidence policy ("checks") slots in without a schema change.
type Verify string

const (
	VerifyLeader Verify = "leader"
	VerifyAuto   Verify = "auto"
)

// Actor identifies who is holding the pen for a transition. Root is the
// judgment writer (the wf_task_* tools); System is the engine performing
// strictly mechanical edges. There is no worker actor — solo workers have
// no board tools; their terminal report arrives through the engine.
type Actor string

const (
	ActorRoot   Actor = "root"
	ActorSystem Actor = "system"
)

// WorkerSpec is the spawn spec fixed at creation for an engine-managed
// task: which subagent kind to dispatch, whether it runs in an isolated
// git worktree, and the model capability tier (the AGENT tool's level).
// A task without a WorkerSpec is a self-task — a step the root agent does
// itself, tracked on the same graph.
type WorkerSpec struct {
	AgentType string `json:"agent_type"`
	Isolation string `json:"isolation,omitempty"`
	Level     int    `json:"level,omitempty"`
}

// Comment is one append-only audit line: force-unblocks, verdicts,
// worker-lost resets, and root notes all land here.
type Comment struct {
	By   Actor     `json:"by"`
	Note string    `json:"note"`
	At   time.Time `json:"at"`
}

// Task is one node of the board graph.
//
// DependsOn edges may only reference tasks that already exist and are
// immutable after creation, so the graph is acyclic by construction —
// no cycle detection exists anywhere (the DWF rule).
type Task struct {
	ID          string      `json:"id"`
	Subject     string      `json:"subject"`
	Description string      `json:"description,omitempty"`
	ActiveForm  string      `json:"active_form,omitempty"`
	Status      Status      `json:"status"`
	Verify      Verify      `json:"verify"`
	Worker      *WorkerSpec `json:"worker,omitempty"`
	DependsOn   []string    `json:"depends_on,omitempty"`
	Owner       string      `json:"owner,omitempty"` // daemon id of the running worker
	Result      string      `json:"result,omitempty"`
	// WorkerFailed marks a verifying task whose worker crashed or was
	// killed instead of reporting; the store refuses to auto-complete it
	// regardless of the verify policy.
	WorkerFailed bool      `json:"worker_failed,omitempty"`
	Comments     []Comment `json:"comments,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// EngineManaged reports whether the engine dispatches this task (it carries
// a worker spec). Self-tasks are root-managed on every edge.
func (t Task) EngineManaged() bool { return t.Worker != nil }
