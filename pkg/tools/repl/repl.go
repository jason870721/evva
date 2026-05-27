// Package repl hosts the repl tool: a scratch REPL that runs a Python or
// JavaScript snippet in a fresh subprocess and returns its output.
package repl

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

// killGrace bounds how long Wait sits on stdout/stderr pipes after the
// process is killed. Subprocesses spawned by the snippet inherit the pipe
// fds and can keep them open; without WaitDelay, Wait blocks forever and
// the timeout never surfaces to the model. Mirrors shell.bashKillGrace.
const killGrace = 2 * time.Second

// Default and maximum timeouts mirror the bash tool: the maximum matches
// the schema's documented 600 000 ms cap; anything larger is clamped.
const (
	defaultTimeout = 2 * time.Minute
	maxTimeout     = 10 * time.Minute
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.REPL} }

// REPLTool runs a code snippet via a language interpreter with cmd.Dir set
// to the workdir captured at construction. One instance per agent. The
// interpreter process is fresh per call — no variables, imports, or
// definitions persist between invocations.
type REPLTool struct {
	workdir string
}

// NewREPL constructs a REPLTool bound to workdir. An empty workdir means
// "use the process's current directory" (cmd.Dir = "" — exec defaults).
func NewREPL(workdir string) *REPLTool { return &REPLTool{workdir: workdir} }

func (t *REPLTool) Name() string { return string(tools.REPL) }

func (t *REPLTool) Description() string {
	return "Executes a Python or JavaScript code snippet in a fresh subprocess and returns its combined stdout+stderr output.\n\n" +
		"State does NOT persist between calls — each invocation runs a brand-new interpreter process, so variables, imports, and definitions from a previous call are gone. Put everything the snippet needs in a single `code` string.\n\n" +
		"language selects the interpreter: \"python\" runs python3 (falling back to python), \"javascript\" runs node. Defaults to python.\n\n" +
		"The snippet is passed as a single argument (python -c / node -e), so keep it reasonably sized — a very large program may hit the OS argument-length limit; in that case write a file and run it with bash instead.\n\n" +
		"Prefer bash for shell/CLI operations and the dedicated read/write/edit tools for files. Reach for repl when a real language is the clearest way to compute or transform data.\n\n" +
		"Timeout defaults to 120000 ms (2 min), max 600000 ms (10 min)."
}

func (t *REPLTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["code"],
		"properties":{
			"code":{"type":"string","description":"The Python or JavaScript source to execute."},
			"language":{"type":"string","enum":["python","javascript"],"description":"Interpreter to run the code with. Defaults to python."},
			"timeout":{"type":"number","description":"Optional timeout in milliseconds (max 600000, default 120000)."}
		}
	}`)
}

type replInput struct {
	Code     string `json:"code"`
	Language string `json:"language"`
	Timeout  *int64 `json:"timeout"`
}

func (t *REPLTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in replInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("repl: decode input: %v", err)}, nil
	}
	if strings.TrimSpace(in.Code) == "" {
		return tools.Result{IsError: true, Content: "repl: code is required"}, nil
	}

	language := strings.ToLower(strings.TrimSpace(in.Language))
	if language == "" {
		language = "python"
	}
	bin, codeFlag, err := resolveInterpreter(language)
	if err != nil {
		return tools.Result{IsError: true, Content: "repl: " + err.Error()}, nil
	}

	var timeoutMs int64
	if in.Timeout != nil {
		timeoutMs = *in.Timeout
	}
	logger.Debug("repl.dispatch", "lang", language, "bin", bin, "timeout_ms", timeoutMs, "bytes", len(in.Code))

	timeout := defaultTimeout
	if in.Timeout != nil {
		ms := time.Duration(*in.Timeout) * time.Millisecond
		switch {
		case ms <= 0:
			timeout = defaultTimeout
		case ms > maxTimeout:
			timeout = maxTimeout
		default:
			timeout = ms
		}
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, codeFlag, in.Code)
	cmd.Dir = t.workdir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	// Put the interpreter in its own process group so the timeout-driven
	// teardown SIGKILLs the whole tree, not just the immediate child — a
	// snippet may spawn subprocesses that inherit the output pipes and
	// would otherwise keep cmd.Wait blocked past the timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid → process group. Best-effort: the group may already
		// be gone, and WaitDelay still catches straggling pipe holders.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
	cmd.WaitDelay = killGrace

	runErr := cmd.Run()
	out := buf.String()

	if cctx.Err() == context.DeadlineExceeded {
		msg := fmt.Sprintf("repl: timed out after %s\n--- partial output ---\n%s", timeout, out)
		return tools.Result{IsError: true, Content: msg}, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		// Caller cancellation — propagate via Go error so the agent loop
		// returns llm.ErrInterrupted to the CLI.
		return tools.Result{IsError: true, Content: "repl: cancelled"}, ctx.Err()
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			logger.Warn("repl.fail", "lang", language, "exit", exitErr.ExitCode(), "bytes", len(out))
			msg := fmt.Sprintf("%s\n--- exit code %d ---", out, exitErr.ExitCode())
			return tools.Result{IsError: true, Content: msg}, nil
		}
		logger.Warn("repl.fail", "lang", language, "stage", "spawn", "err", runErr)
		return tools.Result{IsError: true, Content: fmt.Sprintf("repl: %v", runErr)}, nil
	}

	return tools.Result{Content: out}, nil
}

// resolveInterpreter maps a language to an executable on PATH and the flag
// that passes the program as a single argument. Errors are model-facing.
func resolveInterpreter(language string) (bin, codeFlag string, err error) {
	switch language {
	case "python", "py":
		for _, c := range []string{"python3", "python"} {
			if p, e := exec.LookPath(c); e == nil {
				return p, "-c", nil
			}
		}
		return "", "", fmt.Errorf("no Python interpreter found on PATH (looked for python3, python)")
	case "javascript", "js", "node":
		if p, e := exec.LookPath("node"); e == nil {
			return p, "-e", nil
		}
		return "", "", fmt.Errorf("no JavaScript interpreter found on PATH (looked for node)")
	default:
		return "", "", fmt.Errorf("unsupported language %q (supported: python, javascript)", language)
	}
}
