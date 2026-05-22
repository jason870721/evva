// Package monitor hosts the deferred Monitor tool — a background process
// watcher that streams stdout lines as agent-loop notifications.
//
// The tool spawns the configured shell command in its own process group
// bound to the host's RootCtx (so monitor goroutines survive the LLM call
// that spawned them). For each stdout line the goroutine writes a
// MonitorEvent to the host's queue and fires a signal so an idle agent
// wakes up to react; events arriving while the loop is busy are folded
// into the next iteration via drainMonitorEvents.
package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.MONITOR} }

// timeout defaults / clamps mirror the schema documentation.
const (
	defaultMonitorTimeout = 5 * time.Minute
	maxMonitorTimeout     = 60 * time.Minute
	monitorKillGrace      = 2 * time.Second
)

// MonitorTool spawns a long-running shell command and streams its
// stdout lines back to the agent as MonitorEvents. host supplies the
// MonitorTaskStore + event queue + signal sender; without it the tool
// reports a clean error rather than panicking.
type MonitorTool struct {
	host MonitorHost
}

// NewMonitor constructs the production MonitorTool. The toolset factory
// passes the agent's *ToolState as host so per-event delivery routes
// through the agent's signal pump.
func NewMonitor(host MonitorHost) *MonitorTool { return &MonitorTool{host: host} }

func (t *MonitorTool) Name() string { return string(tools.MONITOR) }

func (t *MonitorTool) Description() string {
	return "Start a background monitor that streams events from a long-running script. " +
		"Each stdout line becomes a notification delivered to the agent loop on a later turn. " +
		"Use for per-occurrence events: log watchers, file-change loops, dev-server outputs, " +
		"poll loops that emit one line per signal. " +
		"For a single \"tell me when X is done\" notification, prefer `bash run_in_background:true` instead. " +
		"Use `grep --line-buffered` in pipes so lines flush promptly. " +
		"The monitor stops itself when the underlying process exits OR when task_stop is called with its id. " +
		"Persistent monitors run for the lifetime of the session (no timeout); non-persistent monitors honour `timeout_ms`."
}

func (t *MonitorTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["description","timeout_ms","persistent","command"],
		"properties":{
			"command":{"type":"string","description":"Shell command or script. Each stdout line is an event; exit ends the watch."},
			"description":{"type":"string","description":"Short human-readable description of what you are monitoring (shown in notifications and the monitor strip)."},
			"persistent":{"type":"boolean","default":false,"description":"Run for the lifetime of the session (no timeout). Stop with task_stop."},
			"timeout_ms":{"type":"number","default":300000,"minimum":1000,"description":"Kill the monitor after this deadline. Default 300000ms, max 3600000ms. Ignored when persistent is true."}
		}
	}`)
}

type monitorInput struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Persistent  bool   `json:"persistent"`
	TimeoutMs   int64  `json:"timeout_ms"`
}

func (t *MonitorTool) Execute(_ context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	var in monitorInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("monitor: decode: %v", err)}, nil
	}
	if strings.TrimSpace(in.Command) == "" {
		return tools.Result{IsError: true, Content: "monitor: command is required"}, nil
	}
	if t.host == nil || t.host.MonitorTaskStore() == nil {
		return tools.Result{IsError: true, Content: "monitor: host is not configured"}, nil
	}

	id := GenerateID()
	store := t.host.MonitorTaskStore()
	rootCtx := t.host.RootCtx()

	// Compute the lifetime ctx. Persistent monitors live as long as
	// rootCtx; bounded ones get a child ctx with the (clamped) deadline.
	var monCtx context.Context
	var cancel context.CancelFunc
	if in.Persistent {
		monCtx, cancel = context.WithCancel(rootCtx)
	} else {
		dur := time.Duration(in.TimeoutMs) * time.Millisecond
		switch {
		case dur <= 0:
			dur = defaultMonitorTimeout
		case dur > maxMonitorTimeout:
			dur = maxMonitorTimeout
		}
		monCtx, cancel = context.WithTimeout(rootCtx, dur)
	}

	snap := MonitorTaskSnapshot{
		ID:          id,
		Command:     in.Command,
		Description: in.Description,
		Status:      Monitoring,
		StartedAt:   time.Now(),
		AgentID:     t.host.AgentID(),
	}
	store.Add(snap, cancel)

	cmdText := in.Command
	host := t.host

	go func() {
		defer cancel()
		runMonitor(monCtx, host, id, cmdText, store, logger)
	}()

	msg := fmt.Sprintf("Monitor %s started. Stream events will be delivered as notifications; use task_stop %s to terminate it.", id, id)
	return tools.Result{Content: msg}, nil
}

// runMonitor is the per-monitor goroutine: spawn the shell command,
// stream stdout line-by-line, enqueue + signal each line as a
// MonitorEvent. The closing event (Closing:true) fires after the
// process exits so drain folds a clean "monitor closed" reminder
// instead of leaving the model wondering whether to expect more.
func runMonitor(ctx context.Context, host MonitorHost, id, command string, store *MonitorTaskStore, logger *slog.Logger) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
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
		logger.Warn("monitor.pipe.err", "id", id, "err", pipeErr)
		store.Complete(id, Failed)
		emitClosing(host, id)
		return
	}
	// Merge stderr into stdout so the model sees both as monitor events.
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		logger.Warn("monitor.start.err", "id", id, "err", err)
		store.Complete(id, Failed)
		emitClosing(host, id)
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 4*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		ev := MonitorEvent{MonitorID: id, Line: line, At: time.Now()}
		store.IncEventCount(id)
		host.NotifyMonitorEvent(ev)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		logger.Warn("monitor.scan.err", "id", id, "err", err)
	}

	waitErr := cmd.Wait()
	status := Stopped
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			// non-zero exit — still "stopped" from the monitor's POV; the
			// command's job is to stream and exit. A spawn failure already
			// landed in Failed above.
			status = Stopped
		} else if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = Stopped
		} else {
			status = Failed
		}
	}

	store.Complete(id, status)
	emitClosing(host, id)
}

// emitClosing pushes one closing event onto the queue + signals the
// agent so the drain reminder includes a clear "monitor <id> closed"
// message. Safe to call multiple times — extra closings are folded by
// the drain.
func emitClosing(host MonitorHost, id string) {
	host.NotifyMonitorEvent(MonitorEvent{MonitorID: id, Closing: true, At: time.Now()})
}
