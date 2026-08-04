package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/common/proc"
	"github.com/johnny1110/evva/pkg/sandbox"
	"github.com/johnny1110/evva/pkg/tools"
)

// Compile-time assertion: bashDaemon satisfies daemon.Daemon.
// (Documents the contract; not strictly required by the compiler since
// state.Register takes the interface.)
//
//nolint:unused // exists for compile-time interface check
var _ = (*bashDaemon)(nil)

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

// BashTool runs `<shell> -c <command>` — proc.Shell() resolves the shell:
// /bin/sh on unix, Git Bash on Windows — with cmd.Dir set to the workdir
// captured at construction. One BashTool instance per agent — the
// toolset factory in internal/toolset/builtins.go calls NewBashWithHost so
// each agent (including subagents spawned with isolation: "worktree")
// gets a tool that runs in its own directory. The bash process is fresh
// per call — shell env state does NOT persist between invocations.
//
// When run_in_background=true and a DaemonHost is installed on the
// agent's ToolState, Execute returns immediately with a daemon id (prefix
// "b") and the command runs in a detached goroutine. Completion emits a
// terminal Lifecycle signal on the agent's DaemonState; the drain at the
// next iter start folds it into <system-reminder>.
type BashTool struct {
	workdir string
	host    DaemonHost
	// sandbox is the late-bound lookup for OS-level isolation (SBX). Nil, or
	// a lookup returning nil, means the command runs directly on the host —
	// the default. Resolved per call rather than captured at construction
	// because the container is provisioned after the tool exists, following
	// the worktree controller's late-binding precedent rather than the
	// checkpoint sink's construction-time capture.
	sandbox func() *sandbox.Container
}

// NewBash constructs a BashTool bound to workdir. An empty workdir means
// "use the process's current directory" (cmd.Dir = "" — exec defaults).
// Use this for tests / narrow callers; production tooling always passes
// the agent's workdir.
//
// host may be nil — in that case run_in_background falls back to the
// "not supported" error path. Production callers pass a non-nil host so
// the detached path works.
func NewBash(workdir string) *BashTool { return &BashTool{workdir: workdir} }

// NewBashWithHost is the production constructor used by the toolset
// builtins factory. The host supplies the DaemonState + RootCtx + AgentID
// the bash daemon needs; without it run_in_background is rejected with a
// clear message.
// WithSandbox installs the late-bound container lookup used to route commands
// into a sandboxed session. Chained-option shape so the existing constructors
// keep their signatures and every non-sandboxed caller is unaffected.
func (t *BashTool) WithSandbox(lookup func() *sandbox.Container) *BashTool {
	t.sandbox = lookup
	return t
}

// container resolves this call's sandbox, or nil when the session runs on the
// host.
func (t *BashTool) container() *sandbox.Container {
	if t.sandbox == nil {
		return nil
	}
	return t.sandbox()
}

// command builds the exec.Cmd for one shell invocation, routing through the
// session's container when there is one.
//
// The guest-path mapping is the part that is easy to get wrong: cmd.Dir is a
// host path and means nothing to `exec`, so a sandboxed call has to translate
// its workdir onto the mount and pass it as `-w`. A workdir outside the mount
// is a hard error rather than a silent fallback to the mount root — running
// somewhere other than where the agent believes it is would corrupt results
// far more quietly than a failed command does.
func (t *BashTool) command(ctx context.Context, shell, command string) (*exec.Cmd, error) {
	c := t.container()
	if c == nil {
		cmd := exec.CommandContext(ctx, shell, "-c", command)
		cmd.Dir = t.workdir
		return cmd, nil
	}
	guestDir := ""
	if t.workdir != "" {
		mapped, ok := c.GuestPath(t.workdir)
		if !ok {
			return nil, fmt.Errorf("bash: sandboxed session workdir %s is outside the container mount %s — refusing to run", t.workdir, c.HostRoot)
		}
		guestDir = mapped
	}
	cmd := exec.CommandContext(ctx, c.Runtime, c.ExecArgs(guestDir, shell, command)...)
	// The docker/podman CLI itself still runs on the host; anchor it somewhere
	// valid so a deleted workdir cannot break the client process.
	cmd.Dir = c.HostRoot
	return cmd, nil
}

func NewBashWithHost(workdir string, host DaemonHost) *BashTool {
	return &BashTool{workdir: workdir, host: host}
}

func (t *BashTool) Name() string { return string(tools.BASH) }

func (t *BashTool) Description() string {
	d := "Executes a given bash command and returns its combined stdout+stderr output.\n\n" +
		"The working directory persists between commands, but shell state (env vars, aliases) does not — " +
		"each call runs in a fresh shell.\n\n" +
		"Prefer dedicated tools when one fits: Read for known paths, Edit for edits, Write for new files. " +
		"Reserve Bash for shell-only operations.\n\n" +
		"Timeout defaults to 120000 ms (2 min), max 600000 ms (10 min).\n\n" +
		"You can use the `run_in_background` parameter to run the command in the background. " +
		"Only use this if you don't need the result immediately and are OK being notified when the command completes later. " +
		"You do not need to check the output right away — you'll be notified when it finishes. " +
		"You do not need to use '&' at the end of the command when using this parameter. " +
		"The tool returns a daemon id (prefix \"b\"); use daemon_list to enumerate, daemon_output to read captured output, and daemon_stop to terminate.\n\n" +
		"dangerouslyDisableSandbox is accepted but ignored — the permission gate now mediates execution."
	if runtime.GOOS == "windows" {
		d += "\n\nOn Windows, commands run via Git Bash (bash.exe from Git for Windows): write POSIX bash syntax, not PowerShell/cmd. " +
			"Inside the shell, native paths like C:\\Users\\me appear in POSIX form as /c/Users/me; both forms are accepted by most tooling."
	}
	return d
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
		if t.host == nil || t.host.DaemonState() == nil {
			return tools.Result{
				IsError: true,
				Content: "bash: run_in_background is not available in this context (no DaemonHost)",
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

	shell, shErr := proc.Shell()
	if shErr != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("bash: %v", shErr)}, nil
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Sandbox routing (SBX-2): when this session is backed by a container, the
	// same command runs as `<runtime> exec` instead of a direct subprocess.
	// Everything downstream — timeout, process group, kill tree, WaitDelay,
	// output capture — is deliberately untouched: an exec is itself just a
	// subprocess, so the existing teardown machinery already owns it
	// correctly.
	cmd, sbErr := t.command(cctx, shell, in.Command)
	if sbErr != nil {
		return tools.Result{IsError: true, Content: sbErr.Error()}, nil
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	// Put the shell in its own kill unit (process group on unix; process
	// group + taskkill tree on Windows) so the timeout-driven teardown
	// takes down the whole tree, not just the immediate child. Without
	// this, `bash -c "node server.js"` leaves node alive — it inherited
	// stdout, so cmd.Wait blocks indefinitely waiting for the pipe to
	// close, and the model never sees the "timed out" result.
	proc.Group(cmd)
	// Cancel hook: kill the entire tree when either the timeout fires or
	// the caller cancels. Best-effort: the tree may already be gone, and
	// we still want WaitDelay to catch any straggling pipe holders.
	cmd.Cancel = func() error {
		_ = proc.KillTree(cmd)
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
		//
		// The partial output rides along (STE-3). The kill tree has already
		// fired, so whatever is in the buffer is everything the command
		// managed to say; discarding it costs the model the one piece of
		// evidence about how far a four-minute build actually got. The
		// agent layer caps it — see interruptedPartialCap — because that is
		// where the interject-vs-abort distinction is known.
		msg := "bash: cancelled"
		if out != "" {
			msg += "\n--- partial output ---\n" + out
		}
		return tools.Result{IsError: true, Content: msg}, ctx.Err()
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

// runBackground constructs a bashDaemon, registers it on the host's
// DaemonState, and spawns the goroutine that drives the process. Returns
// immediately with the daemon id wrapped in a model-friendly status
// line. The model picks the result up via daemon_output or via the
// auto-delivered <system-reminder> when the daemon emits its terminal
// lifecycle.
func (t *BashTool) runBackground(logger *slog.Logger, in bashInput) (tools.Result, error) {
	state := t.host.DaemonState()
	d := newBashDaemon(
		t.host.RootCtx(),
		state,
		t.workdir,
		in.Command,
		in.Description,
		t.host.AgentID(),
		t.container(),
		logger,
	)
	state.Register(d)
	go d.run()

	msg := fmt.Sprintf(
		"Daemon %s started in background. You will be notified when it completes; use daemon_output to read its captured output sooner, daemon_stop to terminate it.",
		d.ID(),
	)
	return tools.Result{Content: msg}, nil
}
