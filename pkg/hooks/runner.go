package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// commandResult is the raw outcome of a single subprocess invocation.
type commandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	timedOut bool
	err      error
}

// runCommand executes a shell hook synchronously. timeoutSec is the
// per-hook timeout; 0 falls back to defaultTimeout.
//
// The subprocess gets the JSON payload on stdin and reads env via baseEnv
// (which the dispatcher fills with EVVA_PROJECT_DIR, EVVA_SESSION_ID,
// EVVA_AGENT_ID). Stdout / stderr are captured fully (subprocess output
// is not streamed to the user in v1).
//
// Exit codes:
//   - 0  → caller parses stdout as Decision
//   - 1  → non-blocking error; stderr surfaced via logger
//   - 2  → legacy block signal; caller uses stderr as the reason
//   - >2 → treated like exit 1 (logged, ignored)
func runCommand(
	ctx context.Context,
	logger *slog.Logger,
	cmd Command,
	payload []byte,
	baseEnv []string,
	defaultTimeout time.Duration,
) commandResult {
	if cmd.Command == "" {
		return commandResult{err: errors.New("hooks: empty command")}
	}
	timeout := defaultTimeout
	if cmd.Timeout > 0 {
		timeout = time.Duration(cmd.Timeout) * time.Second
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := "/bin/sh"
	c := exec.CommandContext(cctx, shell, "-c", cmd.Command)
	c.Env = baseEnv
	c.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	start := time.Now()
	err := c.Run()
	elapsed := time.Since(start)

	res := commandResult{
		stdout:   stdout.Bytes(),
		stderr:   stderr.Bytes(),
		exitCode: 0,
		err:      err,
	}
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			res.timedOut = true
			logger.Warn("hooks.timeout", "cmd", truncate(cmd.Command, 80), "timeout", timeout, "elapsed", elapsed)
			return res
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exitCode = ee.ExitCode()
		} else {
			logger.Warn("hooks.exec_error", "cmd", truncate(cmd.Command, 80), "err", err)
		}
	}
	logger.Debug("hooks.exec",
		"cmd", truncate(cmd.Command, 80),
		"exit", res.exitCode,
		"elapsed", elapsed,
		"stdout_bytes", len(res.stdout),
		"stderr_bytes", len(res.stderr),
	)
	return res
}

// runCommandAsync fires a shell hook in a goroutine and returns
// immediately. Errors are logged. There's a 5-minute hard ceiling so an
// abandoned async hook can't outlive a long session.
func runCommandAsync(
	ctx context.Context,
	logger *slog.Logger,
	cmd Command,
	payload []byte,
	baseEnv []string,
) {
	go func() {
		hardCeiling := 5 * time.Minute
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hardCeiling)
		defer cancel()
		res := runCommand(cctx, logger, cmd, payload, baseEnv, hardCeiling)
		if res.exitCode != 0 {
			logger.Info("hooks.async.exit", "cmd", truncate(cmd.Command, 80), "exit", res.exitCode, "stderr", truncate(string(res.stderr), 200))
		}
	}()
}

// truncate is a defensive log-field shortener so a long hook command
// doesn't blow up structured logs.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// extractReason picks the user-facing block message for an exit-code-2
// hook. Stderr wins; if stderr is empty fall back to a stdout snippet
// and finally a generic label.
func extractReason(res commandResult, fallback string) string {
	if msg := strings.TrimSpace(string(res.stderr)); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(string(res.stdout)); msg != "" {
		return msg
	}
	if fallback != "" {
		return fallback
	}
	return fmt.Sprintf("hook exited %d", res.exitCode)
}
