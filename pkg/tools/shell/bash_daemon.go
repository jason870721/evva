package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/common/proc"
	"github.com/johnny1110/evva/pkg/sandbox"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// bashOutputCap is the per-daemon captured-output ceiling. The bg goroutine
// trims to the trailing window plus a "[N bytes truncated]" header before
// returning it via Output(), matching the sync Bash path's 64 KiB contract.
const bashOutputCap = 64 * 1024

// DaemonHost is the narrow surface BashTool needs to spawn a bash daemon.
// Satisfied by *toolset.ToolState in production: it exposes DaemonState()
// (the catalog daemons register into), RootCtx() (the agent-lifetime ctx
// daemon goroutines bind to so they outlive the LLM call), and AgentID()
// (copied into snapshots so the TUI can label rows by owner).
type DaemonHost interface {
	DaemonState() *daemon.DaemonState
	RootCtx() context.Context
	AgentID() string
}

// bashDaemon implements daemon.Daemon for a `bash -c <command>` process
// running detached in its own process group.
//
// Lifecycle:
//
//	newBashDaemon → state.Register → go d.run()
//	  ├── process exits naturally → setTerminal(Completed/Failed)
//	  └── Kill() called          → ctx cancel → SIGKILL → setTerminal(Killed)
//	→ state.Emit(NewLifecycleSignal) wakes the agent loop
//	→ drain folds the lifecycle into <system-reminder>
//	→ Evict removes the daemon from the catalog
type bashDaemon struct {
	mu sync.Mutex

	id          string
	command     string
	description string
	agentID     string
	startedAt   time.Time
	workdir     string
	// container is this daemon's sandbox, or nil to run on the host.
	// Resolved once at construction rather than per call: unlike the
	// synchronous tool, a daemon is created at the moment the command is
	// issued, so there is nothing to late-bind.
	container *sandbox.Container

	// Guarded by mu.
	status   daemon.DaemonStatus
	exitCode *int
	endedAt  time.Time
	output   bytes.Buffer

	ctx    context.Context
	cancel context.CancelFunc

	state  *daemon.DaemonState
	logger *slog.Logger
}

// newBashDaemon constructs a daemon bound to a child of parentCtx so
// Kill() can SIGKILL the process group. It does NOT spawn the goroutine —
// the caller registers the daemon with the state, then calls run() in a
// goroutine of its choosing.
func newBashDaemon(
	parentCtx context.Context,
	state *daemon.DaemonState,
	workdir, command, description, agentID string,
	container *sandbox.Container,
	logger *slog.Logger,
) *bashDaemon {
	ctx, cancel := context.WithCancel(parentCtx)
	// SBX-6: a sandboxed background command takes a distinct daemon kind so
	// its id prefix and every listing that renders one say plainly that this
	// process is not running on the host.
	kind := daemon.KindLocalBash
	if container != nil {
		kind = daemon.KindSandboxedBash
	}
	return &bashDaemon{
		id:          daemon.GenerateID(kind),
		command:     command,
		description: description,
		agentID:     agentID,
		startedAt:   time.Now(),
		workdir:     workdir,
		container:   container,
		status:      daemon.StatusRunning,
		ctx:         ctx,
		cancel:      cancel, // cancel func
		state:       state,
		logger:      logger,
	}
}

// buildCmd assembles the process this daemon drives, routing through the
// session's container when there is one. Mirrors BashTool.command; the two
// stay deliberately parallel so a change to sandbox routing is one shape
// applied twice rather than two divergent implementations.
func (d *bashDaemon) buildCmd(shell string) (*exec.Cmd, error) {
	if d.container == nil {
		cmd := exec.CommandContext(d.ctx, shell, "-c", d.command)
		cmd.Dir = d.workdir
		return cmd, nil
	}
	guestDir := ""
	if d.workdir != "" {
		mapped, ok := d.container.GuestPath(d.workdir)
		if !ok {
			return nil, fmt.Errorf("sandboxed session workdir %s is outside the container mount %s", d.workdir, d.container.HostRoot)
		}
		guestDir = mapped
	}
	cmd := exec.CommandContext(d.ctx, d.container.Runtime, d.container.ExecArgs(guestDir, shell, d.command)...)
	cmd.Dir = d.container.HostRoot
	return cmd, nil
}

// ID returns the daemon's wire-stable id.
func (d *bashDaemon) ID() string { return d.id }

// Snapshot implements daemon.Daemon. Lock-and-copy so observers don't race
// the goroutine writing into output.
func (d *bashDaemon) Snapshot() daemon.DaemonSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	meta := daemon.LocalBashMeta{
		Command:  d.command,
		ExitCode: d.exitCode,
		Output:   capOutput(d.output.String()),
	}
	return daemon.DaemonSnapshot{
		ID:          d.id,
		Kind:        daemon.KindLocalBash,
		Status:      d.status,
		Description: d.description,
		AgentID:     d.agentID,
		StartedAt:   d.startedAt,
		EndedAt:     d.endedAt,
		Metadata:    meta,
	}
}

// Kill implements daemon.Daemon. Cancels d.ctx — exec.CommandContext picks
// it up, the Cancel hook below SIGKILLs the whole process group, and Wait
// returns with err=context.Canceled. The run goroutine then transitions
// the daemon to Killed and emits the terminal Lifecycle.
//
// Idempotent: cancelling an already-cancelled ctx is a no-op.
func (d *bashDaemon) Kill(_ context.Context) error {
	d.cancel()
	return nil
}

// Output implements daemon.Daemon. Returns a header + the captured tail
// formatted for the daemon_output tool. While the daemon is running the
// buffer may grow further between calls; once terminal the buffer is fixed.
func (d *bashDaemon) Output() string {
	snap := d.Snapshot()
	meta := snap.Metadata.(daemon.LocalBashMeta)
	header := fmt.Sprintf("daemon %s [%s/%s]", snap.ID, snap.Kind, snap.Status)
	if meta.ExitCode != nil {
		header = fmt.Sprintf("%s exit=%d", header, *meta.ExitCode)
	}
	body := meta.Output
	if body == "" {
		body = "(no output yet)"
	}
	return header + "\n---\n" + body
}

// run drives the bash process. Blocks until exit. Spawned in a goroutine
// by the caller after state.Register.
func (d *bashDaemon) run() {
	defer d.cancel() // release the ctx tree no matter how we exit

	shell, shErr := proc.Shell()
	if shErr != nil {
		d.finishSpawnFailure(shErr)
		return
	}

	w := &lockedWriter{buf: &d.output, mu: &d.mu}
	// Sandbox routing (SBX-2), identical in shape to the synchronous path:
	// only the command line changes; the kill-tree and WaitDelay teardown
	// below governs the exec subprocess exactly as it governed a direct shell.
	cmd, sbErr := d.buildCmd(shell)
	if sbErr != nil {
		d.finishSpawnFailure(sbErr)
		return
	}
	cmd.Stdout = w
	cmd.Stderr = w
	proc.Group(cmd)
	cmd.Cancel = func() error {
		// Kill the entire tree; best-effort, it may already be gone.
		// WaitDelay below picks up any pipe holders that survived.
		_ = proc.KillTree(cmd)
		return nil
	}
	cmd.WaitDelay = bashKillGrace

	runErr := cmd.Run()

	d.mu.Lock()
	status := daemon.StatusCompleted
	exitCode := 0
	switch {
	case errors.Is(d.ctx.Err(), context.Canceled):
		// daemon_stop or root-ctx cancellation — operator chose to terminate.
		status = daemon.StatusKilled
		exitCode = -1
	case runErr != nil:
		status = daemon.StatusFailed
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Spawn-level failure (binary missing, etc.) — record in output
			// so daemon_output surfaces a useful message instead of empty.
			exitCode = -1
			fmt.Fprintf(&d.output, "\nbg: spawn error: %v", runErr)
		}
	}
	d.status = status
	d.exitCode = &exitCode
	d.endedAt = time.Now()
	d.mu.Unlock()

	if d.logger != nil {
		d.logger.Debug("bash_daemon.exit", "id", d.id, "status", status, "exit", exitCode)
	}
	d.state.Emit(daemon.NewLifecycleSignal(d, status))
}

// finishSpawnFailure records a pre-spawn failure (e.g. no usable shell on
// this platform) so daemon_output surfaces the reason, then emits the
// terminal lifecycle exactly like a spawn-level cmd.Run error would.
func (d *bashDaemon) finishSpawnFailure(err error) {
	d.mu.Lock()
	exitCode := -1
	d.status = daemon.StatusFailed
	d.exitCode = &exitCode
	d.endedAt = time.Now()
	fmt.Fprintf(&d.output, "bg: spawn error: %v", err)
	d.mu.Unlock()

	if d.logger != nil {
		d.logger.Debug("bash_daemon.exit", "id", d.id, "status", daemon.StatusFailed, "exit", exitCode)
	}
	d.state.Emit(daemon.NewLifecycleSignal(d, daemon.StatusFailed))
}

// lockedWriter wraps a bytes.Buffer with a shared mutex so concurrent
// exec.Cmd writes + Snapshot reads don't race. The Cmd writes from its
// internal goroutine; Snapshot reads from the caller.
type lockedWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// capOutput trims s to the trailing bashOutputCap bytes with a one-line
// truncation header. Mirrors the sync Bash path's behaviour so a 64 KiB
// model-facing window is the implicit contract for both paths.
func capOutput(s string) string {
	if len(s) <= bashOutputCap {
		return s
	}
	trimmed := s[len(s)-bashOutputCap:]
	return fmt.Sprintf("[bg output capped — %d bytes truncated from head]\n%s",
		len(s)-bashOutputCap, trimmed)
}
