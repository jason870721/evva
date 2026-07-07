package agent

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/workflow"
)

// profileHasWorkflowBoard reports whether the resolved profile mounts the
// wf_task_* board — the single condition the agent wiring keys off (the
// profile builder already folded in the flag + LongRunning exclusion).
func profileHasWorkflowBoard(p Profile) bool {
	return slices.Contains(p.ActiveTools, tools.WF_TASK_CREATE)
}

// wireWorkflow assembles the dynamic-workflow runtime on the root agent:
// board persistence + session scoping, the dispatch engine bound to the
// quiet spawn path, and the session workflow daemon carrying the engine's
// wake lines. Called from New after the subagent spawner is installed.
func (a *Agent) wireWorkflow() {
	store := a.toolState.WorkflowStore()
	store.SetPersistence(filepath.Join(a.cfg.AppHome, "workflows"))
	if err := store.SetSession(a.ID); err != nil {
		a.logger.Warn("workflow: session log unavailable — board is memory-only", "err", err)
	}
	engine := newWorkflowEngine(store, a.cfg.GetWorkflowMaxWorkers(), a.dispatchWorkflowWorker, a.logger)
	wd := newWorkflowDaemon(a.toolState.DaemonState(), store, engine, a.ID)
	engine.bindSignal(wd.EmitLine)
	a.toolState.DaemonState().Register(wd)
	a.toolState.SetWorkflowDispatcher(engine)
	a.workflowEngine = engine
}

// rescopeWorkflow re-points the board at the agent's (new) session id.
// recover=true additionally re-queues running tasks whose worker daemons
// no longer exist and sweeps — the restart-resume recovery; /clear passes
// false (a fresh board has nothing to recover).
func (a *Agent) rescopeWorkflow(recover bool) {
	if a.workflowEngine == nil {
		return
	}
	store := a.toolState.WorkflowStore()
	if err := store.SetSession(a.ID); err != nil {
		a.logger.Warn("workflow: session log unavailable — board is memory-only", "err", err)
	}
	if !recover {
		return
	}
	state := a.toolState.DaemonState()
	reset := store.ResetLostRunning(func(owner string) bool {
		_, ok := state.Get(owner)
		return ok
	})
	if len(reset) > 0 {
		a.logger.Info("workflow: re-queued tasks after resume", "count", len(reset))
	}
	a.workflowEngine.Sweep()
}

// dispatchWorkflowWorker is the engine's production dispatch binding: one
// quiet async subagent per task, claimed on the board between daemon
// registration and worker start (a worker can never run unclaimed). The
// terminal report routes back into the engine and the worker daemon is
// evicted — the board, not the catalog, is the record.
func (a *Agent) dispatchWorkflowWorker(t workflow.Task, prompt string, claim func(daemonID string) error) error {
	req := meta.SpawnRequest{
		Name:      "wf-" + t.ID,
		Kind:      t.Worker.AgentType,
		Desc:      fmt.Sprintf("workflow #%s: %s", t.ID, t.Subject),
		Prompt:    prompt,
		Level:     t.Worker.Level,
		AsyncMode: true,
		Isolation: t.Worker.Isolation,
	}
	var daemonID string
	_, err := a.spawnSubagent(a.rootCtx, req, spawnHooks{
		quiet: true,
		preRun: func(id string) error {
			daemonID = id
			return claim(id)
		},
		onDone: func(resp string, runErr error) {
			a.workflowEngine.onWorkerDone(t.ID, resp, runErr)
			if daemonID != "" {
				a.toolState.DaemonState().Evict(daemonID)
			}
		},
	})
	return err
}

// workflowDispatchFn launches one ephemeral worker for a board task. The
// implementation must invoke claim(daemonID) after the worker's daemon is
// registered and BEFORE the worker starts running — claiming transitions
// the task pending → running with the owner stamped, so a worker can never
// run against an unclaimed task. A claim error aborts the launch.
//
// Injected so the engine core is testable without an LLM; the production
// binding is Agent.bindWorkflowDispatch (the quiet async spawn path).
type workflowDispatchFn func(t workflow.Task, prompt string, claim func(daemonID string) error) error

// workflowEngine is the solo dynamic workflow's dispatcher — the DWF
// execution model in-process: it executes structure the root agent already
// declared on the board (assignee specs, dependencies, verify policies)
// and holds no judgment. The store is the single source of truth; the
// engine keeps no task state of its own, so a sweep is always safe to
// repeat (crash recovery = replay the board, reset lost workers, sweep).
//
// One mutex serializes sweeps and completions: Dispatchable→claim would
// otherwise race two sweeps into double-dispatching a task.
type workflowEngine struct {
	store    *workflow.Store
	max      int // concurrent engine-dispatched workers (workflow_max_workers)
	logger   *slog.Logger
	dispatch workflowDispatchFn
	// signal delivers one wake line to the root agent's conversation
	// (bound to the workflow daemon's event emitter). Used for judgment
	// moments only: leader-verify results, worker failures, and the
	// settled summary — never for verify:"auto" completions (silence is
	// the feature; the board carries the record).
	signal func(line string)

	mu         sync.Mutex
	paused     bool
	silentDone int // auto-completions since the root last got a signal
}

func newWorkflowEngine(store *workflow.Store, maxWorkers int, dispatch workflowDispatchFn, logger *slog.Logger) *workflowEngine {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &workflowEngine{
		store:    store,
		max:      maxWorkers,
		logger:   logger,
		dispatch: dispatch,
		signal:   func(string) {},
	}
}

// bindSignal installs the wake-line sink. Called once during wiring,
// before any dispatch can happen.
func (e *workflowEngine) bindSignal(fn func(line string)) {
	if fn != nil {
		e.signal = fn
	}
}

// Sweep implements workflow.Dispatcher: dispatch every ready
// engine-managed task up to the concurrency cap. Idempotent — it serves
// the create/verify/force-unblock tool hooks, the completion cascade, and
// the resume recovery with one primitive (the DWF DispatchReady shape).
func (e *workflowEngine) Sweep() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sweepLocked()
}

func (e *workflowEngine) sweepLocked() {
	if e.paused {
		return
	}
	slots := e.max - e.runningWorkers()
	for _, t := range e.store.Dispatchable() {
		if slots <= 0 {
			return
		}
		prompt := workerPrompt(e.store, t)
		claim := func(daemonID string) error {
			_, err := e.store.Dispatch(t.ID, daemonID)
			return err
		}
		if err := e.dispatch(t, prompt, claim); err != nil {
			e.logger.Warn("workflow: dispatch failed", "task", t.ID, "err", err)
			e.quarantineLocked(t, err)
			continue
		}
		e.logger.Debug("workflow: dispatched", "task", t.ID, "agent_type", t.Worker.AgentType)
		slots--
	}
}

// quarantineLocked routes a task whose worker could not even launch into
// the standard failure lane (verifying + WorkerFailed) so the root judges
// it instead of the sweep retrying forever.
func (e *workflowEngine) quarantineLocked(t workflow.Task, cause error) {
	if _, err := e.store.Dispatch(t.ID, ""); err != nil {
		return // task moved underneath us (deleted/force-edited) — board wins
	}
	if _, err := e.store.CompleteWork(t.ID, "worker dispatch failed: "+cause.Error(), true); err != nil {
		return
	}
	e.signal(fmt.Sprintf(
		"workflow task #%s (%s): worker dispatch FAILED — %s. Fix the task (wf_task_update) and reject it to re-queue, or delete it.",
		t.ID, t.Subject, firstLine(cause.Error())))
}

// runningWorkers counts live engine-dispatched workers straight off the
// board — the store is truth, the engine holds no counter to drift.
func (e *workflowEngine) runningWorkers() int {
	n := 0
	for _, t := range e.store.List() {
		if t.Status == workflow.StatusRunning && t.EngineManaged() {
			n++
		}
	}
	return n
}

// onWorkerDone is the worker's terminal report arriving through the spawn
// hook: record the result, apply the verify policy, cascade.
func (e *workflowEngine) onWorkerDone(taskID, result string, runErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	failed := runErr != nil
	if failed {
		result = strings.TrimSpace(result)
		if result != "" {
			result += "\n"
		}
		result += "[worker error: " + runErr.Error() + "]"
	}
	task, err := e.store.CompleteWork(taskID, result, failed)
	if err != nil {
		// The board moved underneath the worker (resume reset, operator
		// surgery). The board wins; the result is lost from the ledger but
		// present in the transcript.
		e.logger.Warn("workflow: worker report dropped", "task", taskID, "err", err)
		return
	}

	switch {
	case failed:
		e.signal(fmt.Sprintf(
			"workflow task #%s (%s): worker FAILED — %s. Partial result recorded on the board; judge with wf_task_verify (reject re-dispatches a fresh worker).",
			task.ID, task.Subject, firstLine(runErr.Error())))
	case task.Verify == workflow.VerifyAuto:
		if _, err := e.store.Transition(task.ID, workflow.StatusCompleted, workflow.ActorSystem, ""); err != nil {
			e.logger.Warn("workflow: auto-complete refused", "task", task.ID, "err", err)
			e.signal(fmt.Sprintf("workflow task #%s (%s) done by worker but auto-complete was refused (%s) — verify manually.",
				task.ID, task.Subject, firstLine(err.Error())))
		} else {
			e.silentDone++
			e.logger.Debug("workflow: auto-completed", "task", task.ID)
		}
	default: // verify:"leader"
		e.signal(fmt.Sprintf(
			"workflow task #%s (%s) done by worker — judge it with wf_task_verify (full result via wf_task_get):\n%s",
			task.ID, task.Subject, capString(result, 1500)))
	}

	e.sweepLocked()
	e.settledSignalLocked()
}

// settledSignalLocked emits one summary wake when auto-completed work has
// accumulated silently and the engine has nothing left to run or dispatch
// — the single checkpoint of a zero-wake chain.
func (e *workflowEngine) settledSignalLocked() {
	if e.silentDone == 0 {
		return
	}
	if e.runningWorkers() > 0 || len(e.store.Dispatchable()) > 0 {
		return
	}
	c := e.store.Counts()
	e.signal(fmt.Sprintf(
		"workflow settled: %d task(s) auto-completed since your last look. Board now: %d completed, %d verifying, %d pending, %d blocked — review with wf_task_list.",
		e.silentDone, c[workflow.StatusCompleted], c[workflow.StatusVerifying],
		c[workflow.StatusPending], c[workflow.StatusBlocked]))
	e.silentDone = 0
}

// SetPaused stops (or resumes) auto-dispatch. Running workers finish and
// their results still record — only new launches halt. The board tools
// keep working throughout.
func (e *workflowEngine) SetPaused(v bool) {
	e.mu.Lock()
	e.paused = v
	e.mu.Unlock()
	if !v {
		e.Sweep()
	}
}

// Paused reports the dispatch brake, for the daemon snapshot.
func (e *workflowEngine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// MaxWorkers is the configured concurrency cap, for the daemon snapshot.
func (e *workflowEngine) MaxWorkers() int { return e.max }

// workerPrompt renders a task into an ephemeral worker's full briefing.
// The worker has none of the root's conversation context: the description
// carries the instruction, and completed dependency results are appended
// so chains compose without shared state. Immutable once dispatched.
func workerPrompt(s *workflow.Store, t workflow.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a worker agent executing one task of a larger workflow. Work only within this task's scope.\n\n")
	fmt.Fprintf(&b, "# Task #%s: %s\n", t.ID, t.Subject)
	if t.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", t.Description)
	}
	deps := 0
	for _, depID := range t.DependsOn {
		dep, ok := s.Get(depID)
		if !ok || dep.Status != workflow.StatusCompleted || dep.Result == "" {
			continue
		}
		if deps == 0 {
			b.WriteString("\n# Completed dependency results\n")
		}
		deps++
		fmt.Fprintf(&b, "\n## #%s %s\n%s\n", dep.ID, dep.Subject, capString(dep.Result, 4000))
	}
	b.WriteString("\nWhen finished, report concisely: what you did, where (files/branches), and how you verified it. Your final message is recorded verbatim as this task's result and feeds dependent tasks.")
	return b.String()
}

func capString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[… truncated — full text on the board]"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
