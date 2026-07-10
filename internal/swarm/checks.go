// This file is the CHK check runner: machine evidence for task verification.
// The operator declares ONE check command in the manifest
// (settings.verify_checks — the same trust surface as permission_mode:
// bypass); whenever a task enters `verifying`, the service runs it in the
// work's directory, captures exit + output tail as evidence on the task row,
// and mails the result so the leader's verify wake starts with facts in
// hand. Composed with the DWF verify policies, verify:"checks" completes a
// green task with zero leader wakes and escalates a red one with the
// evidence attached — CI-gated leaderless chains.
//
// The trust boundary (PRD §4): no agent — leader included — can choose or
// edit the command text; agents hold exactly one lever, the per-task
// check:"off" opt-out at task_create. A task field never executes.

package swarm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/common/proc"
	"github.com/johnny1110/evva/pkg/event"
)

// KindTaskCheckDone is the engine event announcing one finished check run —
// the CHK sibling of KindTaskDispatched, flowing through the same pump.
const KindTaskCheckDone = event.Kind("task_check_done")

// Evidence output caps (PRD §5.2): 16 KiB total; when over, keep the head
// (module context, first error) and the tail (the failing test name and
// stack land at the end), marked.
const (
	checkTailCap  = 16 * 1024
	checkHeadKeep = 2 * 1024
	checkTailKeep = 14 * 1024
)

// checkKillGrace bounds how long Wait may sit on pipes held by killed
// descendants after a tree kill — the bash tool's WaitDelay discipline.
const checkKillGrace = 2 * time.Second

// checkRunner executes verify-time checks for one space: a single goroutine
// draining a deduplicated queue — one check at a time, one pending entry per
// task. The queue needs no size cap: refuse-duplicate bounds it by the
// number of tasks in `verifying`. Re-entry while a task's check is executing
// kills that run and queues a fresh one ("latest verifying-entry wins"); a
// killed or superseded run never delivers.
type checkRunner struct {
	sp   *SwarmSpace
	spec agentdef.CheckSpec

	mu      sync.Mutex
	queue   []int64
	queued  map[int64]bool
	running int64              // task id whose check is executing; 0 = none
	cancel  context.CancelFunc // cancels the executing check (re-entry, teardown)
	wake    chan struct{}      // buffered(1) — the poke discipline
	wg      sync.WaitGroup
}

func newCheckRunner(sp *SwarmSpace, spec agentdef.CheckSpec) *checkRunner {
	return &checkRunner{sp: sp, spec: spec, queued: make(map[int64]bool), wake: make(chan struct{}, 1)}
}

// ConfigureChecks installs and starts the space's check runner. NewSpace
// calls it when the manifest sets verify_checks; tests (this package and the
// tools package) call it directly on lite spaces. The runner lives until ctx
// (the space context) is cancelled; stopChecks drains it before the store
// closes.
func (sp *SwarmSpace) ConfigureChecks(ctx context.Context, spec agentdef.CheckSpec) {
	r := newCheckRunner(sp, spec)
	sp.mu.Lock()
	sp.settings.VerifyChecks = &spec
	sp.checks = r
	sp.mu.Unlock()
	r.start(ctx)
}

// ChecksConfigured reports whether this space runs verify-time checks — the
// task_create gate for verify:"checks".
func (sp *SwarmSpace) ChecksConfigured() bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.checks != nil
}

// CheckPending reports whether a task's check is queued or executing — the
// board's RUNNING chip state (evidence only lands when a run finishes).
func (sp *SwarmSpace) CheckPending(id int64) bool {
	sp.mu.Lock()
	r := sp.checks
	sp.mu.Unlock()
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queued[id] || r.running == id
}

// EnqueueCheck queues a check for a task that just entered `verifying`.
// Called from the two verifying-entry tool paths (task_done and
// task_update_status) after their transition commits. No-op when the space
// configures no checks, the task opted out (check:"off"), or its policy is
// auto (auto wins — the leader explicitly declared "mechanical, don't
// gate"). Reports whether a check was actually queued so tool results can
// say so.
func (sp *SwarmSpace) EnqueueCheck(id int64) bool {
	sp.mu.Lock()
	r := sp.checks
	sp.mu.Unlock()
	if r == nil {
		return false
	}
	t, err := sp.Store.GetTask(id)
	if err != nil {
		slog.Warn("swarm checks: enqueue read failed", "task", id, "err", err)
		return false
	}
	if t.CheckOff || t.VerifyPolicy == store.VerifyAuto || t.Status != store.StatusVerifying {
		return false
	}
	r.enqueue(id)
	return true
}

// stopChecks cancels any in-flight check and waits for the runner goroutine.
// Shutdown calls it after cancelling the space context and BEFORE closing
// the store, so a mid-delivery evidence write can never hit a closed DB.
func (sp *SwarmSpace) stopChecks() {
	sp.mu.Lock()
	r := sp.checks
	sp.mu.Unlock()
	if r != nil {
		r.wg.Wait()
	}
}

func (r *checkRunner) start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.loop(ctx)
	}()
}

func (r *checkRunner) enqueue(id int64) {
	r.mu.Lock()
	if r.queued[id] {
		r.mu.Unlock()
		return // already pending — that run will read fresh state anyway
	}
	if r.running == id && r.cancel != nil {
		// Latest verifying-entry wins: kill the stale in-flight run (its
		// KillTree fires via cmd.Cancel) and queue a fresh one.
		r.cancel()
	}
	r.queued[id] = true
	r.queue = append(r.queue, id)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *checkRunner) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		}
		for ctx.Err() == nil {
			r.mu.Lock()
			if len(r.queue) == 0 {
				r.mu.Unlock()
				break
			}
			id := r.queue[0]
			r.queue = r.queue[1:]
			delete(r.queued, id)
			r.mu.Unlock()
			r.runOne(ctx, id)
		}
	}
}

// runOne executes one task's check and delivers the evidence. The fresh
// pre-read skips stale queue entries (the leader may have ruled while the
// check waited — don't burn a 5-minute suite on a settled task); the
// post-read of queued[id] skips delivery for a run that re-entry superseded.
func (r *checkRunner) runOne(parent context.Context, id int64) {
	t, err := r.sp.Store.GetTask(id)
	if err != nil || t.Status != store.StatusVerifying {
		return
	}

	runCtx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.running, r.cancel = id, cancel
	r.mu.Unlock()

	ev := r.execute(runCtx, t)

	r.mu.Lock()
	superseded := r.queued[id]
	r.running, r.cancel = 0, nil
	r.mu.Unlock()
	cancel()

	if parent.Err() != nil || superseded {
		return // teardown, or a fresh verifying-entry owns the task now
	}
	r.sp.deliverCheck(t, ev)
}

// execute runs the configured command with the bash tool's exact process
// discipline: resolved POSIX shell, process group, tree kill on
// timeout/cancel, bounded Wait.
func (r *checkRunner) execute(ctx context.Context, t store.Task) store.CheckEvidence {
	started := time.Now()
	ev := store.CheckEvidence{
		Command:   r.spec.Command,
		StartedAt: started.UnixMilli(),
		Workdir:   r.sp.checkWorkdirFor(t.Assignee),
		Exit:      -1,
	}

	shell, err := proc.Shell()
	if err != nil {
		ev.Tail = "check runner: " + err.Error()
		return ev
	}

	cctx, cancel := context.WithTimeout(ctx, r.spec.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, shell, "-c", r.spec.Command)
	cmd.Dir = ev.Workdir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	proc.Group(cmd)
	cmd.Cancel = func() error {
		_ = proc.KillTree(cmd)
		return nil
	}
	cmd.WaitDelay = checkKillGrace

	runErr := cmd.Run()
	ev.DurationMs = time.Since(started).Milliseconds()
	out := buf.String()

	switch {
	case cctx.Err() == context.DeadlineExceeded:
		ev.TimedOut = true
	case runErr == nil:
		ev.Exit = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			ev.Exit = exitErr.ExitCode()
		} else {
			out += "\ncheck runner: " + runErr.Error()
		}
	}
	ev.Tail, ev.Truncated = capCheckOutput(out)
	ev.Pass = ev.Exit == 0 && !ev.TimedOut
	return ev
}

// checkWorkdirFor resolves where a member's check runs. Today every member
// shares the space workdir; when the worktree-isolation wave lands, this is
// the seam that returns the assignee's own checkout instead. Evidence names
// the directory either way — pre-worktrees a teammate mid-edit can fail a
// check that isn't the assignee's fault, and the evidence must let the
// leader see that.
func (sp *SwarmSpace) checkWorkdirFor(assignee string) string {
	return sp.Workdir
}

// capCheckOutput bounds evidence output to ~16 KiB (head + tail, marked).
func capCheckOutput(out string) (string, bool) {
	if len(out) <= checkTailCap {
		return out, false
	}
	return out[:checkHeadKeep] + "\n… (output truncated) …\n" + out[len(out)-checkTailKeep:], true
}

// deliverCheck lands one finished run, in the PRD's order: persist the
// evidence (the rescan-visible source of truth), settle or mail, then the
// event line. On a verify:"checks" task a green run completes it with the
// same narrowly-scoped system transition the auto policy uses — the store's
// writer matrix re-checks the just-persisted evidence — and cascades the
// dependency dispatch; every other outcome delivers evidence mail instead
// (a red checks-task stays in `verifying` and the mail IS the escalation).
func (sp *SwarmSpace) deliverCheck(t store.Task, ev store.CheckEvidence) {
	if err := sp.Store.SetTaskChecks(t.ID, ev); err != nil {
		slog.Warn("swarm checks: persist evidence failed", "task", t.ID, "err", err)
		return
	}
	sp.metrics.countCheck(ev.Pass, ev.TimedOut)

	settled := false
	if t.VerifyPolicy == store.VerifyChecks && ev.Pass {
		sys := store.Actor{Name: EngineSender, Role: store.RoleSystem}
		note := fmt.Sprintf("auto-verified (policy: checks — %s)", ev.Outcome())
		if err := sp.Store.TransitionTask(t.ID, store.StatusCompleted, sys, note); err != nil {
			// Raced a leader ruling (or the evidence gate refused): fall back
			// to advisory mail so the run is never silently lost.
			slog.Warn("swarm checks: auto-complete failed", "task", t.ID, "err", err)
		} else {
			settled = true
			if _, err := sp.DispatchReady(); err != nil {
				slog.Warn("swarm checks: dispatch after auto-complete failed", "task", t.ID, "err", err)
			}
		}
	}
	if !settled {
		// Green on an auto-completing task mails nobody — the DWF auto
		// precedent: silence is the feature; the row, chip, and event line
		// carry the record.
		sp.mailCheckResult(t, ev)
	}

	line := fmt.Sprintf("task #%d checks %s", t.ID, ev.Outcome())
	if settled {
		line += " — auto-completed (verify: checks)"
	}
	sp.emitEngineEvent(KindTaskCheckDone, t.Assignee, line)
}

// mailCheckResult delivers the evidence as durable mail to the leader and
// the operator — the notifyOps shape (scheduler.go), space-owned because the
// runner belongs to the space, not the supervisor. A failing tail rides
// along: it is the leader's rejection note's first draft.
func (sp *SwarmSpace) mailCheckResult(t store.Task, ev store.CheckEvidence) {
	subject := fmt.Sprintf("checks: task #%d %s", t.ID, ev.Outcome())
	if ev.Pass {
		subject = "✅ " + subject
	} else {
		subject = "❌ " + subject
	}

	body := fmt.Sprintf("Verify-time check for task #%d %q (assignee: %s):\n\n  command: %s\n  ran in:  %s\n  outcome: %s",
		t.ID, t.Title, t.Assignee, ev.Command, ev.Workdir, ev.Outcome())
	switch {
	case ev.Pass && t.VerifyPolicy == store.VerifyLeader:
		body += "\n\nThe machine evidence is green. Inspect the result and settle it with task_verify {approve: true|false} — the check informs your judgment, it does not replace it."
	case !ev.Pass:
		if tail := ev.Tail; tail != "" {
			body += "\n\n--- output tail ---\n" + tail
		}
		if t.VerifyPolicy == store.VerifyChecks {
			body += "\n\nThe task stays in verifying — the engine only auto-completes green runs. Inspect, then task_verify {approve: false} with rework instructions (the tail above is your first draft), or approve to overrule a flaky/known-red check."
		} else {
			body += "\n\nInspect and settle with task_verify; reject notes can start from the tail above."
		}
	}

	refID := t.ID
	leader := ""
	if sp.Roster != nil {
		leader = sp.Roster.LeaderName()
	}
	if leader != "" {
		if _, err := sp.Bus.Send(store.Message{Sender: EngineSender, Recipient: leader, Subject: subject, Body: body, RefTask: &refID}); err != nil {
			slog.Warn("swarm checks: evidence mail to leader failed", "task", t.ID, "err", err)
		}
	}
	if _, err := sp.Bus.Send(store.Message{Sender: EngineSender, Recipient: "user", Subject: subject, Body: body, RefTask: &refID}); err != nil {
		slog.Warn("swarm checks: evidence mail to operator failed", "task", t.ID, "err", err)
	}
}
