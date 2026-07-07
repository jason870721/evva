package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/workflow"
)

// workflowDaemon is the dispatch engine's presence in the daemon catalog —
// the reserved KindLocalWorkflow finally implemented. One per session when
// the dynamic workflow is enabled. It is the engine's voice: wake lines
// (leader-verify results, failures, the settled summary) are emitted as
// Event signals through it and rendered by the existing drain; the model
// and TUI see live board counts via its snapshot.
//
// Kill() is the operator's brake: it pauses auto-dispatch. Running workers
// finish and record, the board tools keep working — only new launches stop.
type workflowDaemon struct {
	id        string
	state     *daemon.DaemonState
	store     *workflow.Store
	engine    *workflowEngine
	ownerID   string // the root agent's id, for TUI row labeling
	startedAt time.Time

	mu      sync.Mutex
	status  daemon.DaemonStatus
	endedAt time.Time
}

func newWorkflowDaemon(state *daemon.DaemonState, store *workflow.Store, engine *workflowEngine, agentID string) *workflowDaemon {
	return &workflowDaemon{
		id:        daemon.GenerateID(daemon.KindLocalWorkflow),
		state:     state,
		store:     store,
		engine:    engine,
		ownerID:   agentID,
		startedAt: time.Now(),
		status:    daemon.StatusRunning,
	}
}

func (d *workflowDaemon) ID() string { return d.id }

func (d *workflowDaemon) Snapshot() daemon.DaemonSnapshot {
	d.mu.Lock()
	status := d.status
	endedAt := d.endedAt
	d.mu.Unlock()

	c := d.store.Counts()
	meta := daemon.LocalWorkflowMeta{
		Total:      c[workflow.StatusBlocked] + c[workflow.StatusPending] + c[workflow.StatusRunning] + c[workflow.StatusVerifying] + c[workflow.StatusCompleted],
		Blocked:    c[workflow.StatusBlocked],
		Pending:    c[workflow.StatusPending],
		Running:    c[workflow.StatusRunning],
		Verifying:  c[workflow.StatusVerifying],
		Completed:  c[workflow.StatusCompleted],
		MaxWorkers: d.engine.MaxWorkers(),
		Paused:     d.engine.Paused(),
	}
	return daemon.DaemonSnapshot{
		ID:          d.id,
		Kind:        daemon.KindLocalWorkflow,
		Status:      status,
		Description: "dynamic workflow engine",
		AgentID:     d.ownerID,
		StartedAt:   d.startedAt,
		EndedAt:     endedAt,
		Metadata:    meta,
	}
}

// Kill implements daemon.Daemon — pause dispatch and mark the daemon
// killed so the drain tells the root the engine is off.
func (d *workflowDaemon) Kill(_ context.Context) error {
	d.engine.SetPaused(true)
	d.mu.Lock()
	if daemon.IsTerminal(d.status) {
		d.mu.Unlock()
		return nil
	}
	d.status = daemon.StatusKilled
	d.endedAt = time.Now()
	d.mu.Unlock()
	d.state.Emit(daemon.NewLifecycleSignal(d, daemon.StatusKilled))
	return nil
}

// Output implements daemon.Daemon — a board digest for daemon_output.
func (d *workflowDaemon) Output() string {
	snap := d.Snapshot()
	m := snap.Metadata.(daemon.LocalWorkflowMeta)
	paused := ""
	if m.Paused {
		paused = " (dispatch PAUSED)"
	}
	return fmt.Sprintf(
		"daemon %s [%s/%s]%s\n---\nboard: %d tasks — %d blocked, %d pending, %d running, %d verifying, %d completed (cap %d workers)",
		snap.ID, snap.Kind, snap.Status, paused,
		m.Total, m.Blocked, m.Pending, m.Running, m.Verifying, m.Completed, m.MaxWorkers)
}

// EmitLine delivers one engine wake line into the root's conversation via
// the standard daemon-event drain.
func (d *workflowDaemon) EmitLine(line string) {
	d.state.Emit(daemon.NewEventSignal(d, line, false))
}
