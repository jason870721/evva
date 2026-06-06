package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnny1110/evva/pkg/config"
)

// The swarm control plane keeps its runtime state under <AppHome>/service/:
//
//	evva-service.pid   the daemon's pid (parent-written, child-cleaned)
//	token              the session token the daemon minted (clients read it)
//	addr               the resolved listen address (so clients find the port)
//	evva-service.log   the daemon's stdout+stderr
//
// EVVA_SERVICE_HOME overrides the directory (tests point it at a temp dir);
// EVVA_SERVICE_ADDR overrides the listen/target address.
const (
	daemonEnv  = "EVVA_SERVICE_DAEMON" // set on the backgrounded child
	addrEnv    = "EVVA_SERVICE_ADDR"
	homeEnv    = "EVVA_SERVICE_HOME"
	defaultSvc = "127.0.0.1:8888"

	pidName   = "evva-service.pid"
	tokenName = "token"
	addrName  = "addr"
	logName   = "evva-service.log"
)

// serviceDir is <AppHome>/service/ (or EVVA_SERVICE_HOME), created on demand.
func serviceDir() string {
	if d := os.Getenv(homeEnv); d != "" {
		return d
	}
	return filepath.Join(config.Get().AppHome, "service")
}

func pidPath() string   { return filepath.Join(serviceDir(), pidName) }
func tokenPath() string { return filepath.Join(serviceDir(), tokenName) }
func addrPath() string  { return filepath.Join(serviceDir(), addrName) }
func logPath() string   { return filepath.Join(serviceDir(), logName) }

// listenAddr is the address the daemon binds (start) — env override or default.
func listenAddr() string {
	if a := os.Getenv(addrEnv); a != "" {
		return a
	}
	return defaultSvc
}

// targetAddr is the address a client talks to: the resolved addr file the
// running daemon wrote, else the env override, else the default.
func targetAddr() string {
	if b, err := os.ReadFile(addrPath()); err == nil {
		if a := strings.TrimSpace(string(b)); a != "" {
			return a
		}
	}
	return listenAddr()
}

func readPid() (int, bool) {
	b, err := os.ReadFile(pidPath())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func writePid(pid int) error {
	if err := os.MkdirAll(serviceDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidPath(), []byte(strconv.Itoa(pid)), 0o644)
}

// processAlive reports whether pid names a live process. Signal 0 performs the
// kernel's permission/existence check without delivering a signal: nil (or an
// EPERM we don't expect for our own daemon) means alive; ESRCH means gone.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// readToken returns the daemon's session token, or "" if absent.
func readToken() string {
	b, err := os.ReadFile(tokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// clearRuntimeFiles removes the pidfile/token/addr (daemon teardown).
func clearRuntimeFiles() {
	_ = os.Remove(pidPath())
	_ = os.Remove(tokenPath())
	_ = os.Remove(addrPath())
}

// serviceClient issues an authenticated request to the running daemon and
// decodes a JSON response into out (out may be nil). A connection error is
// wrapped so callers can tell "service not running" from an API error.
func serviceClient(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}

	url := "http://" + targetAddr() + path
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+readToken())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach evva service at %s (is it running? `evva service start`): %w", targetAddr(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// healthy reports whether the daemon answers GET /healthz at the target addr.
func healthy() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + targetAddr() + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
