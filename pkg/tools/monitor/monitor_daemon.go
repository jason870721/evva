package monitor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// monitorRingCap is the upper bound on RecentLines retained for daemon_output.
// Events themselves have already been drained into the conversation by the
// time daemon_output reads, so this is just an at-a-glance recent tail.
const monitorRingCap = 200

// DaemonHost is the narrow surface MonitorTool needs to spawn a monitor
// daemon. Satisfied by *toolset.ToolState in production.
type DaemonHost interface {
	DaemonState() *daemon.DaemonState
	RootCtx() context.Context
	AgentID() string
}

// monitorDaemon implements daemon.Daemon for a long-running shell command
// whose stdout lines stream back to the agent loop as daemon.Event signals.
//
// Lifecycle:
//
//	newMonitorDaemon → state.Register → go d.run()
//	  ├── each stdout line → Emit(Event{Line, Closing:false})
//	  ├── process exit OR Kill() → Emit(Event{Closing:true}) → setTerminal → Emit(Lifecycle)
type monitorDaemon struct {
	mu sync.Mutex

	id          string
	command     string
	description string
	persistent  bool
	agentID     string
	startedAt   time.Time

	// Guarded by mu.
	status     daemon.DaemonStatus
	endedAt    time.Time
	eventCount int
	ring       []string // bounded; head trimmed when len > monitorRingCap

	ctx    context.Context
	cancel context.CancelFunc

	state  *daemon.DaemonState
	logger *slog.Logger
}

// newMonitorDaemon builds the daemon and wires its lifetime ctx.
// Bounded monitors (persistent=false) get a ctx with timeout; persistent
// monitors live as long as parentCtx (the agent's root ctx).
func newMonitorDaemon(
	parentCtx context.Context,
	state *daemon.DaemonState,
	command, description, agentID string,
	persistent bool,
	timeout time.Duration,
	logger *slog.Logger,
) *monitorDaemon {
	var ctx context.Context
	var cancel context.CancelFunc
	if persistent {
		ctx, cancel = context.WithCancel(parentCtx)
	} else {
		ctx, cancel = context.WithTimeout(parentCtx, timeout)
	}
	return &monitorDaemon{
		id:          daemon.GenerateID(daemon.KindMonitor),
		command:     command,
		description: description,
		persistent:  persistent,
		agentID:     agentID,
		startedAt:   time.Now(),
		status:      daemon.StatusRunning,
		ctx:         ctx,
		cancel:      cancel,
		state:       state,
		logger:      logger,
	}
}

// ID returns the daemon's wire-stable id.
func (d *monitorDaemon) ID() string { return d.id }

// Snapshot implements daemon.Daemon.
func (d *monitorDaemon) Snapshot() daemon.DaemonSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Defensive copy of ring so the caller can't mutate our internal buffer.
	recent := append([]string(nil), d.ring...)
	meta := daemon.MonitorMeta{
		Command:     d.command,
		EventCount:  d.eventCount,
		Persistent:  d.persistent,
		RecentLines: recent,
	}
	return daemon.DaemonSnapshot{
		ID:          d.id,
		Kind:        daemon.KindMonitor,
		Status:      d.status,
		Description: d.description,
		AgentID:     d.agentID,
		StartedAt:   d.startedAt,
		EndedAt:     d.endedAt,
		Metadata:    meta,
	}
}

// Kill implements daemon.Daemon. Cancels the daemon's ctx; the run
// goroutine sees the cancel, the Cancel hook SIGKILLs the process group,
// scanner returns, run transitions to Killed and Emits the lifecycle.
func (d *monitorDaemon) Kill(_ context.Context) error {
	d.cancel()
	return nil
}

// Output implements daemon.Daemon. Returns a header + recent event lines.
func (d *monitorDaemon) Output() string {
	snap := d.Snapshot()
	meta := snap.Metadata.(daemon.MonitorMeta)
	header := fmt.Sprintf("daemon %s [%s/%s] events=%d", snap.ID, snap.Kind, snap.Status, meta.EventCount)
	if len(meta.RecentLines) == 0 {
		return header + "\n---\n(no events captured)"
	}
	body := fmt.Sprintf("recent %d line(s):\n", len(meta.RecentLines))
	for _, line := range meta.RecentLines {
		body += line + "\n"
	}
	return header + "\n---\n" + body
}

// pushEvent records one line in the ring buffer and increments the event
// counter. Returns the snapshot the Emit caller should use (avoids a
// second lock acquire).
func (d *monitorDaemon) pushEvent(line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.eventCount++
	d.ring = append(d.ring, line)
	if len(d.ring) > monitorRingCap {
		// Drop oldest; ring grows linearly in steady state but never above
		// monitorRingCap. We re-slice without copying — Go's runtime keeps
		// the underlying array alive only as long as the slice references it.
		d.ring = d.ring[len(d.ring)-monitorRingCap:]
	}
}

// setTerminal flips the daemon to a terminal status. Idempotent.
func (d *monitorDaemon) setTerminal(status daemon.DaemonStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if daemon.IsTerminal(d.status) {
		return
	}
	d.status = status
	d.endedAt = time.Now()
}

// run drives the monitor process. Blocks until the shell command exits or
// ctx is cancelled. Spawned in a goroutine by the caller after state.Register.
func (d *monitorDaemon) run() {
	defer d.cancel()

	cmd := exec.CommandContext(d.ctx, "/bin/sh", "-c", d.command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
	cmd.WaitDelay = monitorKillGrace

	stdout, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		if d.logger != nil {
			d.logger.Warn("monitor_daemon.pipe.err", "id", d.id, "err", pipeErr)
		}
		d.setTerminal(daemon.StatusFailed)
		d.state.Emit(daemon.NewEventSignal(d, "", true))
		d.state.Emit(daemon.NewLifecycleSignal(d, daemon.StatusFailed))
		return
	}
	// Merge stderr into stdout so the model sees both as monitor events.
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		if d.logger != nil {
			d.logger.Warn("monitor_daemon.start.err", "id", d.id, "err", err)
		}
		d.setTerminal(daemon.StatusFailed)
		d.state.Emit(daemon.NewEventSignal(d, "", true))
		d.state.Emit(daemon.NewLifecycleSignal(d, daemon.StatusFailed))
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 4*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		d.pushEvent(line)
		d.state.Emit(daemon.NewEventSignal(d, line, false))
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		if d.logger != nil {
			d.logger.Warn("monitor_daemon.scan.err", "id", d.id, "err", err)
		}
	}

	waitErr := cmd.Wait()
	status := daemon.StatusCompleted
	switch {
	case errors.Is(d.ctx.Err(), context.Canceled), errors.Is(d.ctx.Err(), context.DeadlineExceeded):
		// daemon_stop, root-ctx cancel, or timeout — externally terminated.
		status = daemon.StatusKilled
	case waitErr != nil:
		// Non-zero exit OR non-exit wait failure both flip the monitor to
		// Failed. Mirrors bash_daemon's classification so a wrong command
		// surfaces as red across both tools instead of looking like a clean
		// completion. ExitCode() == 0 with a non-nil ExitError shouldn't
		// happen in practice, but treat it as Completed for safety.
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 0 {
			status = daemon.StatusCompleted
		} else {
			status = daemon.StatusFailed
		}
	}
	d.setTerminal(status)

	// Closing event first so the drain can render the "stream closed" line
	// inside the same <system-reminder> block as the terminal lifecycle's
	// neighbours. Lifecycle next so the eviction happens after closing is
	// folded.
	d.state.Emit(daemon.NewEventSignal(d, "", true))
	d.state.Emit(daemon.NewLifecycleSignal(d, status))
}
