package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/observable"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// agentDaemon implements daemon.Daemon for a subagent (sync or async).
// The same struct serves both modes; sync subagents are registered,
// run, and evicted in one Spawn call, while async subagents stay in
// DaemonState until their terminal Lifecycle drain.
//
// Lifecycle:
//
//	newAgentDaemon → state.Register → d.run(prompt) (sync) OR go d.run(prompt) (async)
//	  ├── child finishes naturally → Report   → Emit(Lifecycle Completed)
//	  ├── child fails              → Crush    → Emit(Lifecycle Failed)
//	  └── Kill() called            → cancel ctx → child interrupted → Crush(Killed)
//
// Phase updates (thinking / executing / draining / texting) are pushed
// onto the observable stream as `Op: "phase:<status>"` Changes without
// going through the signal queue — they're TUI-only nudges, the drain
// doesn't care about them. The fine-grained phase lives in
// LocalAgentMeta.Phase and is captured into each subsequent Snapshot.
type agentDaemon struct {
	mu sync.Mutex

	id          string
	name        string
	agentType   string
	description string
	prompt      string
	async       bool
	parentID    string
	startedAt   time.Time

	// worktreePath / worktreeBranch are set right after construction (before
	// Register) when the subagent runs under isolation:"worktree". They are
	// surfaced via LocalAgentMeta so worktree_list can show which daemon owns
	// a live worktree. Immutable once Register is called.
	worktreePath   string
	worktreeBranch string

	// quiet suppresses the terminal Lifecycle emit for async subagents whose
	// completion is owned by another reporter (the workflow engine records
	// the result on the board and authors its own wake messages). The daemon
	// stays fully visible to the TUI strip, daemon_list, and daemon_stop.
	// Set before Register, immutable after.
	quiet bool

	// Guarded by mu.
	status  daemon.DaemonStatus
	phase   constant.AgentStatus
	summary string
	errMsg  string
	endedAt time.Time

	state   *daemon.DaemonState
	child   *Agent
	cancel  context.CancelFunc // cancels the per-child run ctx
	aborted atomic.Bool        // true if Kill() was called (distinguishes Killed from natural Failed)
}

// newAgentDaemon builds the daemon and prepares the cancellable child
// context. The caller is responsible for state.Register before calling
// run; sync callers run inline, async callers spawn a goroutine.
func newAgentDaemon(
	parentCtx context.Context,
	state *daemon.DaemonState,
	child *Agent,
	name, agentType, description, prompt string,
	async bool,
	parentID string,
) (*agentDaemon, context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	d := &agentDaemon{
		id:          child.ID,
		name:        name,
		agentType:   agentType,
		description: description,
		prompt:      prompt,
		async:       async,
		parentID:    parentID,
		startedAt:   time.Now(),
		status:      daemon.StatusRunning,
		phase:       constant.INIT,
		state:       state,
		child:       child,
		cancel:      cancel,
	}
	return d, ctx
}

// ID returns the daemon's id (matches child.ID).
func (d *agentDaemon) ID() string { return d.id }

// Snapshot implements daemon.Daemon.
func (d *agentDaemon) Snapshot() daemon.DaemonSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

func (d *agentDaemon) snapshotLocked() daemon.DaemonSnapshot {
	meta := daemon.LocalAgentMeta{
		AgentType:      d.agentType,
		Prompt:         d.prompt,
		Async:          d.async,
		Phase:          d.phase.String(),
		Summary:        d.summary,
		Err:            d.errMsg,
		WorktreePath:   d.worktreePath,
		WorktreeBranch: d.worktreeBranch,
	}
	return daemon.DaemonSnapshot{
		ID:          d.id,
		Kind:        daemon.KindLocalAgent,
		Status:      d.status,
		Description: d.descriptionLocked(),
		AgentID:     d.parentID,
		StartedAt:   d.startedAt,
		EndedAt:     d.endedAt,
		Metadata:    meta,
	}
}

// descriptionLocked prefers the human-set description, falling back to
// the agent name. Called under mu.
func (d *agentDaemon) descriptionLocked() string {
	if d.description != "" {
		return d.description
	}
	return d.name
}

// Kill implements daemon.Daemon. Cancels the child's run context. The
// child's state machine catches ctx.Done(), routes through interrupted,
// which calls d.Crush — that emits the terminal Lifecycle.
//
// aborted is set so Crush can map the Failed-shaped exit to a Killed
// lifecycle (the operator chose to terminate; surface that distinctly).
func (d *agentDaemon) Kill(_ context.Context) error {
	d.aborted.Store(true)
	d.cancel()
	return nil
}

// Output implements daemon.Daemon.
func (d *agentDaemon) Output() string {
	snap := d.Snapshot()
	meta := snap.Metadata.(daemon.LocalAgentMeta)
	async := ""
	if meta.Async {
		async = " async"
	}
	header := fmt.Sprintf("daemon %s [%s/%s] type=%s%s phase=%s",
		snap.ID, snap.Kind, snap.Status, meta.AgentType, async, meta.Phase)
	body := meta.Summary
	if body == "" && meta.Err != "" {
		body = "error: " + meta.Err
	}
	if body == "" {
		body = "prompt: " + truncateSummary(meta.Prompt, 500)
	}
	return header + "\n---\n" + body
}

// Phase updates the fine-grained agent state (thinking, executing, ...).
// Pushes an observable Change but NOT a daemon Signal — drain doesn't
// care about phase nudges. Safe to call from the child's state machine.
func (d *agentDaemon) Phase(phase constant.AgentStatus) {
	d.mu.Lock()
	if daemon.IsTerminal(d.status) {
		d.mu.Unlock()
		return
	}
	d.phase = phase
	snap := d.snapshotLocked()
	d.mu.Unlock()
	d.state.Notify(observable.Change{
		Domain:  daemon.Domain,
		Op:      "phase:" + phase.String(),
		ID:      d.id,
		Payload: snap,
	})
}

// Report marks the child as completed successfully. For async subagents
// the terminal Lifecycle is emitted into the signal queue so the parent's
// drain folds the result into the conversation; for sync subagents the
// result is delivered through the tool return channel and we skip the
// emit to avoid duplicating the model-facing message.
func (d *agentDaemon) Report(summary string) {
	d.mu.Lock()
	if daemon.IsTerminal(d.status) {
		d.mu.Unlock()
		return
	}
	d.status = daemon.StatusCompleted
	d.phase = constant.READY_REPORT
	d.summary = summary
	d.endedAt = time.Now()
	async := d.async
	d.mu.Unlock()
	if async && !d.quiet {
		d.state.Emit(daemon.NewLifecycleSignal(d, daemon.StatusCompleted))
	}
}

// Crush marks the child as failed (or killed, if Kill was called first).
// For async subagents the terminal Lifecycle is emitted so the parent's
// drain folds the failure into the conversation; for sync subagents the
// error surfaces through the tool return and we skip the emit.
//
// terminalStatus lets the caller distinguish Failed (iter-limit, crash)
// from Killed (operator abort). If Kill() was previously called, the
// status is forced to Killed regardless of the caller's hint.
func (d *agentDaemon) Crush(summary string, err error, terminalStatus daemon.DaemonStatus) {
	if d.aborted.Load() {
		terminalStatus = daemon.StatusKilled
	}
	if terminalStatus == "" {
		terminalStatus = daemon.StatusFailed
	}
	msg := summary
	if err != nil {
		if msg != "" {
			msg += " — " + err.Error()
		} else {
			msg = err.Error()
		}
	}

	d.mu.Lock()
	if daemon.IsTerminal(d.status) {
		d.mu.Unlock()
		return
	}
	d.status = terminalStatus
	d.phase = constant.CRUSHED
	if summary != "" {
		d.summary = summary
	}
	d.errMsg = msg
	d.endedAt = time.Now()
	async := d.async
	d.mu.Unlock()
	if async && !d.quiet {
		d.state.Emit(daemon.NewLifecycleSignal(d, terminalStatus))
	}
}

// Aborted reports whether Kill() has been called. Used by state_machine
// helpers that want to surface "subagent loop interrupted" only when the
// interrupt was triggered by an operator stop (not a root-ctx cancel).
func (d *agentDaemon) Aborted() bool { return d.aborted.Load() }

// getOwnDaemon returns the agentDaemon entry the parent registered for
// this subagent, or nil if this agent is the root, has no parent state,
// or has not yet been registered. Used by state_machine helpers.
func (a *Agent) getOwnDaemon() *agentDaemon {
	if a.Parent == nil {
		return nil
	}
	state := a.Parent.ToolState().DaemonState()
	if state == nil {
		return nil
	}
	d, ok := state.Get(a.ID)
	if !ok {
		return nil
	}
	ad, _ := d.(*agentDaemon)
	return ad
}
