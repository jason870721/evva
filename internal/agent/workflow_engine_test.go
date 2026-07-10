package agent

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/workflow"
)

// fakeWorkers stands in for the spawn path: it claims synchronously like
// the real dispatch and lets the test deliver worker reports by hand.
type fakeWorkers struct {
	mu       sync.Mutex
	launched []string          // task ids in dispatch order
	prompts  map[string]string // task id → rendered briefing
	failNext error             // injected launch failure (consumed once)
}

func (f *fakeWorkers) dispatch(t workflow.Task, prompt string, claim func(string) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	if err := claim("d-" + t.ID); err != nil {
		return err
	}
	if f.prompts == nil {
		f.prompts = map[string]string{}
	}
	f.launched = append(f.launched, t.ID)
	f.prompts[t.ID] = prompt
	return nil
}

func (f *fakeWorkers) launchedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.launched...)
}

type sigCollector struct {
	mu    sync.Mutex
	lines []string
}

func (c *sigCollector) add(line string) {
	c.mu.Lock()
	c.lines = append(c.lines, line)
	c.mu.Unlock()
}

func (c *sigCollector) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func engineKit(t *testing.T, maxWorkers int) (*workflow.Store, *fakeWorkers, *sigCollector, *workflowEngine) {
	t.Helper()
	store := workflow.NewStore()
	fw := &fakeWorkers{}
	sig := &sigCollector{}
	e := newWorkflowEngine(store, maxWorkers, fw.dispatch, nil)
	e.bindSignal(sig.add)
	return store, fw, sig, e
}

func mkAutoTask(t *testing.T, s *workflow.Store, subject string, deps ...string) workflow.Task {
	t.Helper()
	task, err := s.Create(workflow.Task{
		Subject: subject, Description: "briefing for " + subject,
		Verify:    workflow.VerifyAuto,
		Worker:    &workflow.WorkerSpec{AgentType: "general-purpose"},
		DependsOn: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// The headline acceptance: a 3-task verify:"auto" chain runs head-to-tail
// off one planning pass with ZERO root wakes until the settled summary.
func TestEngineAutoChainZeroWakes(t *testing.T) {
	store, fw, sig, e := engineKit(t, 4)
	a := mkAutoTask(t, store, "a")
	b := mkAutoTask(t, store, "b", a.ID)
	c := mkAutoTask(t, store, "c", b.ID)

	e.Sweep()
	if got := fw.launchedIDs(); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("launched %v, want [a]", got)
	}

	e.onWorkerDone(a.ID, "result A", nil)
	if got := fw.launchedIDs(); len(got) != 2 || got[1] != b.ID {
		t.Fatalf("cascade should launch b, launched %v", got)
	}
	if len(sig.all()) != 0 {
		t.Fatalf("auto completions must be silent, got %v", sig.all())
	}

	e.onWorkerDone(b.ID, "result B", nil)
	if got := fw.launchedIDs(); len(got) != 3 || got[2] != c.ID {
		t.Fatalf("cascade should launch c, launched %v", got)
	}
	// Dependency results flow into the next worker's briefing.
	if p := fw.prompts[c.ID]; !strings.Contains(p, "result B") || !strings.Contains(p, "briefing for c") {
		t.Errorf("c's briefing missing dep result or description:\n%s", p)
	}

	e.onWorkerDone(c.ID, "result C", nil)
	lines := sig.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "workflow settled: 3") {
		t.Fatalf("want exactly one settled summary, got %v", lines)
	}
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if got, _ := store.Get(id); got.Status != workflow.StatusCompleted {
			t.Errorf("#%s = %s, want completed", id, got.Status)
		}
	}
}

func TestEngineLeaderVerifySignalsPerCompletion(t *testing.T) {
	store, _, sig, e := engineKit(t, 4)
	task, err := store.Create(workflow.Task{
		Subject: "impl", Verify: workflow.VerifyLeader,
		Worker: &workflow.WorkerSpec{AgentType: "general-purpose"},
	})
	if err != nil {
		t.Fatal(err)
	}

	e.Sweep()
	e.onWorkerDone(task.ID, "did the thing", nil)

	got, _ := store.Get(task.ID)
	if got.Status != workflow.StatusVerifying || got.Result != "did the thing" {
		t.Fatalf("leader task after report: %+v", got)
	}
	lines := sig.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "wf_task_verify") || !strings.Contains(lines[0], "did the thing") {
		t.Fatalf("leader signal: %v", lines)
	}
}

func TestEngineWorkerFailureForcesJudgment(t *testing.T) {
	store, _, sig, e := engineKit(t, 4)
	task := mkAutoTask(t, store, "fragile")

	e.Sweep()
	e.onWorkerDone(task.ID, "partial work", errors.New("boom exploded"))

	got, _ := store.Get(task.ID)
	if got.Status != workflow.StatusVerifying || !got.WorkerFailed {
		t.Fatalf("failed worker task: %+v", got)
	}
	if !strings.Contains(got.Result, "partial work") || !strings.Contains(got.Result, "boom exploded") {
		t.Errorf("failure result should keep partial text + error: %q", got.Result)
	}
	lines := sig.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "FAILED") {
		t.Fatalf("failure signal: %v", lines)
	}
	// Even verify:"auto" never auto-completes a failure (matrix-enforced).
	if _, err := store.Transition(task.ID, workflow.StatusCompleted, workflow.ActorSystem, ""); err == nil {
		t.Error("auto-complete of a failed worker's task must be refused")
	}
}

func TestEngineCapQueuesExcessTasks(t *testing.T) {
	store, fw, _, e := engineKit(t, 1)
	a := mkAutoTask(t, store, "a")
	b := mkAutoTask(t, store, "b")

	e.Sweep()
	if got := fw.launchedIDs(); len(got) != 1 {
		t.Fatalf("cap 1: launched %v", got)
	}
	if got, _ := store.Get(b.ID); got.Status != workflow.StatusPending {
		t.Fatalf("b should queue as pending, got %s", got.Status)
	}
	e.onWorkerDone(a.ID, "done", nil)
	if got := fw.launchedIDs(); len(got) != 2 || got[1] != b.ID {
		t.Fatalf("slot freed should launch b, launched %v", got)
	}
}

func TestEnginePauseStopsDispatch(t *testing.T) {
	store, fw, _, e := engineKit(t, 4)
	e.SetPaused(true)
	mkAutoTask(t, store, "a")

	e.Sweep()
	if got := fw.launchedIDs(); len(got) != 0 {
		t.Fatalf("paused engine launched %v", got)
	}
	e.SetPaused(false) // resume sweeps automatically
	if got := fw.launchedIDs(); len(got) != 1 {
		t.Fatalf("unpause should sweep, launched %v", got)
	}
}

// The dispatch fn runs with e.mu held and, in production, blocks on UI
// event delivery (spawn → daemon Register → sink). The TUI meanwhile reads
// engine state through workflowDaemon.Snapshot on every rendered frame. A
// snapshot that waits on e.mu closes the loop — agent waits on the TUI,
// the TUI waits on the agent — which froze the whole TUI on the first
// wf_task_create with a worker spec (session edefa044). Snapshot and
// Paused must return while a sweep is wedged inside dispatch.
func TestWorkflowDaemonSnapshotNeverWaitsOnSweep(t *testing.T) {
	store := workflow.NewStore()
	entered := make(chan struct{})
	release := make(chan struct{})
	e := newWorkflowEngine(store, 1, func(task workflow.Task, _ string, claim func(string) error) error {
		close(entered)
		<-release
		return claim("d-" + task.ID)
	}, nil)
	wd := newWorkflowDaemon(daemon.NewState(nil), store, e, "root")
	mkAutoTask(t, store, "w")

	sweepDone := make(chan struct{})
	go func() {
		e.Sweep()
		close(sweepDone)
	}()
	<-entered // the sweep is now wedged inside dispatch, holding e.mu

	snapDone := make(chan struct{})
	go func() {
		snap := wd.Snapshot()
		if snap.Kind != daemon.KindLocalWorkflow {
			t.Errorf("snapshot kind = %s, want %s", snap.Kind, daemon.KindLocalWorkflow)
		}
		_ = e.Paused()
		close(snapDone)
	}()
	select {
	case <-snapDone:
	case <-time.After(2 * time.Second):
		t.Fatal("workflowDaemon.Snapshot blocked behind a running sweep — TUI deadlock regression")
	}
	close(release)
	<-sweepDone
}

func TestEngineDispatchFailureQuarantines(t *testing.T) {
	store, fw, sig, e := engineKit(t, 4)
	task := mkAutoTask(t, store, "a")
	fw.failNext = errors.New("unknown subagent_type")

	e.Sweep()
	got, _ := store.Get(task.ID)
	if got.Status != workflow.StatusVerifying || !got.WorkerFailed {
		t.Fatalf("quarantined task: %+v", got)
	}
	lines := sig.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "dispatch FAILED") {
		t.Fatalf("quarantine signal: %v", lines)
	}
	// The sweep must not retry it forever.
	e.Sweep()
	if got := fw.launchedIDs(); len(got) != 0 {
		t.Fatalf("quarantined task relaunched: %v", got)
	}
}
