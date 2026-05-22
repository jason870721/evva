package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

// bashKillGrace is how long Wait waits for stdout/stderr pipes to
// drain after the process exits. Needed because exec.CommandContext's
// default kill only SIGKILLs the direct child; subprocesses (e.g.
// `npm install` → node) inherit the pipe fds and can keep them open
// even after we've torn down their parent. Without WaitDelay, Wait
// blocks forever and the timeout never surfaces to the model.
const bashKillGrace = 2 * time.Second

// Default and maximum timeouts. The maximum mirrors the schema's documented
// 600 000 ms cap; anything larger is clamped on input.
const (
	defaultBashTimeout = 2 * time.Minute
	maxBashTimeout     = 10 * time.Minute
)

// BashTool runs `/bin/sh -c <command>` with cmd.Dir set to the workdir
// captured at construction. One BashTool instance per agent — the
// toolset factory in internal/toolset/builtins.go calls NewBash(s.Workdir())
// so each agent (including subagents spawned with isolation: "worktree")
// gets a tool that runs in its own directory. The bash process is fresh
// per call — shell env state does NOT persist between invocations.
//
// When run_in_background=true and a BgTaskHost is installed on the
// agent's ToolState, Execute returns immediately with a task id and
// the command runs in a detached goroutine. Completion routes back to
// the agent's signal pump for idle-wake + drain-at-iter-start delivery
// (see Phase 16 design).
type BashTool struct {
	workdir string
	host    BgTaskHost
}

// NewBash constructs a BashTool bound to workdir. An empty workdir means
// "use the process's current directory" (cmd.Dir = "" — exec defaults).
// Use this for tests / narrow callers; production tooling always passes
// the agent's workdir.
//
// host may be nil — in that case run_in_background falls back to the
// historical "not supported" error path. Production callers (the
// toolset builtins factory) pass a non-nil host so the Phase 16
// detached path works.
func NewBash(workdir string) *BashTool { return &BashTool{workdir: workdir} }

// NewBashWithHost is the production constructor used by the toolset
// builtins factory. The host supplies the BgTaskStore + signal sender
// run_in_background needs; without it the flag is rejected with a
// clear message.
func NewBashWithHost(workdir string, host BgTaskHost) *BashTool {
	return &BashTool{workdir: workdir, host: host}
}

func (t *BashTool) Name() string { return string(tools.BASH) }

func (t *BashTool) Description() string {
	return "Executes a given bash command and returns its combined stdout+stderr output.\n\n" +
		"The working directory persists between commands, but shell state (env vars, aliases) does not — " +
		"each call runs in a fresh shell.\n\n" +
		"Prefer dedicated tools when one fits: Read for known paths, Edit for edits, Write for new files. " +
		"Reserve Bash for shell-only operations.\n\n" +
		"Timeout defaults to 120000 ms (2 min), max 600000 ms (10 min).\n\n" +
		"You can use the `run_in_background` parameter to run the command in the background. " +
		"Only use this if you don't need the result immediately and are OK being notified when the command completes later. " +
		"You do not need to check the output right away — you'll be notified when it finishes. " +
		"You do not need to use '&' at the end of the command when using this parameter. " +
		"The tool returns a task id; use task_list to enumerate active tasks, task_output to read captured output, and task_stop to terminate a running task.\n\n" +
		"dangerouslyDisableSandbox is accepted but ignored — the permission gate now mediates execution."
}

func (t *BashTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["command"],
		"properties":{
			"command":{"type":"string","description":"The command to execute"},
			"description":{"type":"string","description":"Clear, concise description of what this command does in active voice."},
			"timeout":{"type":"number","description":"Optional timeout in milliseconds (max 600000, default 120000)"},
			"run_in_background":{"type":"boolean","description":"Set true to fire-and-forget. Returns a task id; completion is delivered as a notification on a later turn. Use Monitor instead for per-line streaming."},
			"dangerouslyDisableSandbox":{"type":"boolean","description":"Reserved — currently rejected."}
		}
	}`)
}

type bashInput struct {
	Command                   string  `json:"command"`
	Description               string  `json:"description"`
	Timeout                   *int64  `json:"timeout"`
	RunInBackground           bool    `json:"run_in_background"`
	DangerouslyDisableSandbox bool    `json:"dangerouslyDisableSandbox"`
	_                         float64 // silence unused-field warnings if any
}

func (t *BashTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("bash: decode input: %v", err)}, nil
	}
	if strings.TrimSpace(in.Command) == "" {
		return tools.Result{IsError: true, Content: "bash: command is required"}, nil
	}
	var timeoutMs int64
	if in.Timeout != nil {
		timeoutMs = *in.Timeout
	}
	logger.Debug("bash.dispatch", "cmd", in.Command, "timeout_ms", timeoutMs, "desc", in.Description, "bg", in.RunInBackground)
	if in.RunInBackground {
		if t.host == nil || t.host.BgTaskStore() == nil {
			return tools.Result{
				IsError: true,
				Content: "bash: run_in_background is not available in this context (no BgTaskHost)",
			}, nil
		}
		return t.runBackground(logger, in)
	}
	// dangerouslyDisableSandbox is accepted as a no-op now that the
	// permission gate (internal/permission) mediates execution. Drop the
	// hard rejection so existing rules / model habits don't bounce off it.

	timeout := defaultBashTimeout
	if in.Timeout != nil {
		ms := time.Duration(*in.Timeout) * time.Millisecond
		switch {
		case ms <= 0:
			timeout = defaultBashTimeout
		case ms > maxBashTimeout:
			timeout = maxBashTimeout
		default:
			timeout = ms
		}
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", in.Command)
	cmd.Dir = t.workdir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	// Put the shell in its own process group so the timeout-driven
	// teardown can SIGKILL the whole tree, not just the immediate
	// child. Without this, `bash -c "node server.js"` leaves node
	// alive — it inherited stdout, so cmd.Wait blocks indefinitely
	// waiting for the pipe to close, and the model never sees the
	// "timed out" result.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Cancel hook: send SIGKILL to the entire process group when
	// either the timeout fires or the caller cancels.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid → process group. Errors are best-effort: the
		// group may already be gone, and we still want WaitDelay to
		// catch any straggling pipe holders.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
	// Bound how long Wait can sit on file descriptors held by killed
	// subprocesses. After this elapses (Go 1.20+), Wait closes the
	// pipes itself and returns.
	cmd.WaitDelay = bashKillGrace

	err := cmd.Run()
	out := buf.String()

	// Distinguish timeout from generic exit-status failure for the model.
	if cctx.Err() == context.DeadlineExceeded {
		msg := fmt.Sprintf("bash: command timed out after %s\n--- partial output ---\n%s", timeout, out)
		return tools.Result{IsError: true, Content: msg}, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		// Caller cancellation — propagate via Go error so the loop returns
		// llm.ErrInterrupted to the CLI.
		return tools.Result{IsError: true, Content: "bash: cancelled"}, ctx.Err()
	}

	if err != nil {
		// Non-zero exit. Include the output and the exit-code suffix so the
		// model can reason about the failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			logger.Warn("bash.fail", "exit", exitErr.ExitCode(), "bytes", len(out))
			msg := fmt.Sprintf("%s\n--- exit code %d ---", out, exitErr.ExitCode())
			return tools.Result{IsError: true, Content: msg}, nil
		}
		// Spawn-level error (binary not found, etc.) — surface as IsError;
		// the model can suggest a different command.
		logger.Warn("bash.fail", "stage", "spawn", "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("bash: %v", err)}, nil
	}

	return tools.Result{Content: out}, nil
}

// runBackground spawns the bash command in a detached goroutine bound
// to the host's RootCtx (not the per-call ctx — the bg task must
// survive the LLM call that spawned it). The goroutine captures
// stdout+stderr, updates the BgTaskStore with the terminal status when
// the process exits, and signals the agent's pump so an idle agent
// wakes up to react.
//
// Returns immediately with the task id wrapped in a model-friendly
// status line. The model is expected to come back via task_output or
// the auto-delivered system-reminder when the task completes.
func (t *BashTool) runBackground(logger *slog.Logger, in bashInput) (tools.Result, error) {
	id := GenerateID()
	store := t.host.BgTaskStore()
	rootCtx := t.host.RootCtx()
	cctx, cancel := context.WithCancel(rootCtx)

	snap := BgTaskSnapshot{
		ID:          id,
		Command:     in.Command,
		Description: in.Description,
		Status:      BgRunning,
		StartedAt:   time.Now(),
		AgentID:     t.host.AgentID(),
	}
	store.Add(snap, cancel)

	workdir := t.workdir
	cmdText := in.Command

	go func() {
		defer cancel() // release the ctx tree no matter how we exit

		cmd := exec.CommandContext(cctx, "/bin/sh", "-c", cmdText)
		cmd.Dir = workdir
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return nil
		}
		cmd.WaitDelay = bashKillGrace

		runErr := cmd.Run()
		output := buf.String()

		status := BgCompleted
		exitCode := 0
		switch {
		case errors.Is(cctx.Err(), context.Canceled):
			// task_stop or root-ctx cancellation — the producer chose to
			// terminate, so the snapshot reads "killed" regardless of how
			// the command exited.
			status = BgKilled
			exitCode = -1
		case runErr != nil:
			status = BgFailed
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
				output = fmt.Sprintf("%s\nbg: spawn error: %v", output, runErr)
			}
		}

		store.Complete(id, status, exitCode, output)
		// Re-read the updated snapshot to surface the post-Complete fields
		// (output cap, completed_at) downstream.
		if finalSnap, ok := store.Get(id); ok {
			t.host.NotifyBgResult(finalSnap)
		} else {
			logger.Warn("bash.bg.notify.missing", "task_id", id)
		}
	}()

	msg := fmt.Sprintf("Task %s started in background. You will be notified when it completes; use task_output to read its captured output sooner, task_stop to terminate it.", id)
	return tools.Result{Content: msg}, nil
}
