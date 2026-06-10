package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/johnny1110/evva/internal/swarm/service"
)

// parseLogLevel maps EVVA_LOG_LEVEL (debug|info|warn|error, case-insensitive)
// to a slog level, defaulting to info. Set EVVA_LOG_LEVEL=debug before
// `evva service start` to surface the swarm store path, task lifecycle, and
// per-member run/wake tracing in the service log.
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// runService dispatches `evva service <start|stop|status|install-unit>`.
//
//   - start  — daemonize the :8888 host (detached child), write a pidfile + token
//     + addr + log under <AppHome>/service/. Refuses if already running.
//     Flags: --addr <host:port> overrides the bind (else $EVVA_SERVICE_ADDR,
//     else 127.0.0.1:8888); --allow-remote opts into a non-loopback bind
//     (RP-15) — without it a non-loopback --addr refuses to start;
//     --foreground runs the host in THIS process instead of daemonizing —
//     the mode a supervisor (launchd/systemd) wants, since it owns the
//     lifetime and the restarts (RP-18).
//   - stop   — signal the daemon and clean the pidfile (stale pid → just clean).
//   - status — report running/stopped, pid, addr, healthz, and the token file.
//   - install-unit — write the platform's autostart unit (launchd plist on
//     macOS, systemd user unit on Linux) pointing at this binary, and print
//     the activation command. Never enables anything itself; --force
//     overwrites an existing unit file.
//
// The backgrounded child re-enters this same path with EVVA_SERVICE_DAEMON=1 and
// runs the blocking server (serviceRun); the flags reach it as env vars.
func runService(args []string) {
	if os.Getenv(daemonEnv) == "1" {
		serviceRun()
		return
	}

	sub := "start"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	flags, rest := extractServiceFlags(args)
	if len(rest) > 0 {
		exitf(2, "evva service %s: unexpected argument %q", sub, rest[0])
	}

	var err error
	switch sub {
	case "start":
		if flags.foreground {
			err = serviceForeground(os.Stdout, flags.addr, flags.allowRemote)
		} else {
			err = serviceStart(os.Stdout, flags.addr, flags.allowRemote)
		}
	case "stop":
		err = serviceStop(os.Stdout)
	case "status":
		err = serviceStatus(os.Stdout)
	case "install-unit":
		err = serviceInstallUnit(os.Stdout, flags.force)
	default:
		exitf(2, "evva service: unknown subcommand %q (want start|stop|status|install-unit; start takes --addr <host:port>, --allow-remote and --foreground)", sub)
	}
	if err != nil {
		exitf(1, "evva service %s: %v", sub, err)
	}
}

// serviceFlags is everything `evva service` accepts after the subcommand.
type serviceFlags struct {
	addr        string
	allowRemote bool
	foreground  bool
	force       bool
}

// extractServiceFlags pulls the service flags out of args from any position,
// returning the leftovers.
func extractServiceFlags(args []string) (f serviceFlags, rest []string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--addr" && i+1 < len(args):
			f.addr = args[i+1]
			i++
		case strings.HasPrefix(a, "--addr="):
			f.addr = strings.TrimPrefix(a, "--addr=")
		case a == "--allow-remote":
			f.allowRemote = true
		case a == "--foreground":
			f.foreground = true
		case a == "--force":
			f.force = true
		default:
			rest = append(rest, a)
		}
	}
	return f, rest
}

// validateBind enforces the RP-15 loopback gate for both start modes.
func validateBind(addr string, allowRemote bool) error {
	if !allowRemote && !service.IsLoopbackAddr(addr) {
		return fmt.Errorf("refusing non-loopback bind %q — anyone who reaches the service holds operator power over this machine (the agents run shell). Pass --allow-remote to expose it anyway; every endpoint then requires the session token (%s)", addr, tokenPath())
	}
	return nil
}

// serviceForeground runs the host in the CURRENT process — what launchd /
// systemd want (RP-18): the supervisor owns the lifetime, the restarts, and
// stdout/stderr. It still writes the pidfile so `evva service status` / `stop`
// keep telling the truth, and blocks until SIGTERM/SIGINT (serviceRun clears
// the runtime files on exit).
func serviceForeground(out io.Writer, addrFlag string, allowRemote bool) error {
	if pid, ok := readPid(); ok && processAlive(pid) {
		return fmt.Errorf("already running (pid %d) at %s", pid, targetAddr())
	}
	addr := addrFlag
	if addr == "" {
		addr = listenAddr()
	}
	if err := validateBind(addr, allowRemote); err != nil {
		return err
	}
	if err := os.MkdirAll(serviceDir(), 0o755); err != nil {
		return err
	}
	if err := writePid(os.Getpid()); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	// serviceRun reads its parameters from the env — the daemon-child contract.
	// Reuse it rather than growing a second plumbing path.
	_ = os.Setenv(addrEnv, addr)
	if allowRemote {
		_ = os.Setenv(allowRemoteEnv, "1")
	}
	fmt.Fprintf(out, "evva service running in the foreground on http://%s (pid %d)\n", addr, os.Getpid())
	serviceRun()
	return nil
}

// serviceStart backgrounds a detached copy of this binary running the host, then
// records its pid. Idempotent: a live daemon makes it refuse. addrFlag is the
// --addr override ("" = env/default); allowRemote is the explicit non-loopback
// opt-in, validated here too so a bad combination fails in the caller's
// terminal instead of only in the daemon log.
func serviceStart(out io.Writer, addrFlag string, allowRemote bool) error {
	if pid, ok := readPid(); ok && processAlive(pid) {
		return fmt.Errorf("already running (pid %d) at %s", pid, targetAddr())
	}
	// A stale pidfile (process gone) is fine to overwrite.

	addr := addrFlag
	if addr == "" {
		addr = listenAddr()
	}
	if err := validateBind(addr, allowRemote); err != nil {
		return err
	}

	if err := os.MkdirAll(serviceDir(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logf.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "service", "start")
	cmd.Env = append(os.Environ(), daemonEnv+"=1", addrEnv+"="+addr)
	if allowRemote {
		cmd.Env = append(cmd.Env, allowRemoteEnv+"=1")
	}
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from this terminal

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	if err := writePid(cmd.Process.Pid); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}

	fmt.Fprintf(out, "evva service started (pid %d) on http://%s\n", cmd.Process.Pid, addr)
	fmt.Fprintf(out, "  logs:  %s\n", logPath())
	fmt.Fprintf(out, "  token: %s\n", tokenPath())
	if allowRemote {
		fmt.Fprintf(out, "  REMOTE MODE: all endpoints require the token above; webhook POSTs from other hosts need each space's settings.webhook_secret.\n")
	}
	return nil
}

// serviceRun is the daemon child: bind, publish the token + addr for clients,
// and serve until SIGTERM/SIGINT. It removes the runtime files on a clean exit.
func serviceRun() {
	svc := service.New(listenAddr())
	svc.SetAllowRemote(os.Getenv(allowRemoteEnv) == "1") // before Listen — it gates non-loopback binds
	svc.SetStateDir(serviceDir())                        // persist + reconcile registered spaces across restarts
	if err := svc.Listen(); err != nil {
		fmt.Fprintf(os.Stderr, "evva service: %v\n", err)
		os.Exit(1)
	}
	// One logger for the whole daemon. SetDefault routes package-level slog
	// calls (the swarm store path / task lifecycle / bus) to the same service
	// log as the service's own logger. Level is env-tunable: run
	// `EVVA_LOG_LEVEL=debug evva service start` to see the full swarm trace.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("EVVA_LOG_LEVEL"))}))
	slog.SetDefault(logger)
	svc.SetLogger(logger)

	if err := os.WriteFile(tokenPath(), []byte(svc.Token()), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "evva service: write token: %v\n", err)
	}
	if err := os.WriteFile(addrPath(), []byte(svc.Addr()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "evva service: write addr: %v\n", err)
	}

	// Rebuild any spaces that were registered before the last shutdown so a
	// restart picks up where it left off (SPRD-1-11).
	if err := svc.Reconcile(); err != nil {
		slog.Warn("evva service: reconcile incomplete", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("evva service listening", "addr", svc.Addr())
	if err := svc.Serve(ctx); err != nil {
		slog.Error("evva service exited", "err", err)
	}
	clearRuntimeFiles()
}

// serviceStop terminates the daemon (SIGTERM) and clears the pidfile. A stale
// pidfile (process already gone) is cleaned without error.
func serviceStop(out io.Writer) error {
	pid, ok := readPid()
	if !ok {
		fmt.Fprintln(out, "evva service: not running")
		return nil
	}
	if !processAlive(pid) {
		clearRuntimeFiles()
		fmt.Fprintln(out, "evva service: not running (cleared stale pidfile)")
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}

	// Give the daemon a moment to drain, then ensure the pidfile is gone.
	for i := 0; i < 50 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	clearRuntimeFiles()
	fmt.Fprintf(out, "evva service stopped (pid %d)\n", pid)
	return nil
}

// serviceStatus reports the daemon's liveness, address, and token location.
func serviceStatus(out io.Writer) error {
	pid, ok := readPid()
	switch {
	case !ok:
		fmt.Fprintln(out, "evva service: stopped")
	case !processAlive(pid):
		fmt.Fprintln(out, "evva service: stopped (stale pidfile present)")
	default:
		reach := "unreachable"
		if healthy() {
			reach = "reachable"
		}
		fmt.Fprintf(out, "evva service: running (pid %d)\n", pid)
		fmt.Fprintf(out, "  addr:   http://%s (%s)\n", targetAddr(), reach)
		fmt.Fprintf(out, "  token:  %s\n", tokenPath())
	}
	return nil
}
