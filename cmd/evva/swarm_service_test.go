package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/service"
)

// useServiceHome points the control-plane state at a temp dir for the test.
func useServiceHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(homeEnv, dir)
	return dir
}

// TestStatusStalePid: a pidfile whose process is gone reads as stopped, and a
// live pid (our own) reads as running.
func TestStatusStalePid(t *testing.T) {
	useServiceHome(t)

	// Dead pid: a very high value is not a live process.
	if err := writePid(1 << 30); err != nil {
		t.Fatal(err)
	}
	if processAlive(1 << 30) {
		t.Skip("environment unexpectedly has a live process at the probe pid")
	}
	var buf bytes.Buffer
	if err := serviceStatus(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "stale pidfile") {
		t.Fatalf("status = %q, want stale-pid stopped", buf.String())
	}

	// Live pid: ourselves.
	if err := writePid(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := serviceStatus(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "running") {
		t.Fatalf("status = %q, want running", buf.String())
	}
}

// TestStopNotRunning: stop with no pidfile is a clean no-op; stop with a stale
// pidfile clears it.
func TestStopNotRunning(t *testing.T) {
	useServiceHome(t)

	var buf bytes.Buffer
	if err := serviceStop(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not running") {
		t.Fatalf("stop (no pid) = %q", buf.String())
	}

	if err := writePid(1 << 30); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := serviceStop(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "stale") {
		t.Fatalf("stop (stale) = %q", buf.String())
	}
	if _, err := os.Stat(pidPath()); !os.IsNotExist(err) {
		t.Fatal("stale pidfile not cleared")
	}
}

// startInProcess brings a real service up on an ephemeral port and publishes its
// token+addr the way the daemon child would, so the client commands can target
// it. Returns the bound address.
func startInProcess(t *testing.T) string {
	t.Helper()
	svc := service.New("127.0.0.1:0")
	if err := svc.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = svc.Serve(ctx) }()
	t.Cleanup(cancel)

	if err := os.WriteFile(addrPath(), []byte(svc.Addr()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath(), []byte(svc.Token()), 0o600); err != nil {
		t.Fatal(err)
	}
	// Wait for the listener to answer before returning.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !healthy() {
		time.Sleep(10 * time.Millisecond)
	}
	return svc.Addr()
}

// TestClientAgainstLiveService: status (running+reachable), ls (empty), and a
// stop of an unknown space (error) against a real in-process service.
func TestClientAgainstLiveService(t *testing.T) {
	useServiceHome(t)
	startInProcess(t)
	// status needs a live pid to call the daemon "running"; reuse our own.
	if err := writePid(os.Getpid()); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := serviceStatus(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "running") || !strings.Contains(buf.String(), "reachable") {
		t.Fatalf("status = %q, want running+reachable", buf.String())
	}

	buf.Reset()
	if err := swarmLs(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no spaces") {
		t.Fatalf("ls = %q, want empty", buf.String())
	}

	if err := swarmStop(&buf, "does-not-exist"); err == nil {
		t.Fatal("swarm stop of unknown space should error")
	}
}

// TestSwarmRegisterClient drives the register client against a stub endpoint:
// it must validate the local manifest, POST {workdir} with the token, and print
// the returned space id. (The real agent build is the service's job and is
// covered in the service package's tests.)
func TestSwarmRegisterClient(t *testing.T) {
	useServiceHome(t)

	const wantToken = "tkn"
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/swarms" {
			http.Error(w, "bad route", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"sp-xyz"}`))
	}))
	defer stub.Close()

	// Publish the stub as the "running service".
	addr := strings.TrimPrefix(stub.URL, "http://")
	if err := os.WriteFile(addrPath(), []byte(addr), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath(), []byte(wantToken), 0o600); err != nil {
		t.Fatal(err)
	}

	// A workdir with a valid manifest, made the cwd.
	wd := t.TempDir()
	manifest := "name: team\nleader:\n  agent: leader\nworkers:\n  - agent: worker\n"
	if err := os.WriteFile(filepath.Join(wd, "evva-swarm.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)

	var buf bytes.Buffer
	if err := swarmRegister(&buf, ""); err != nil {
		t.Fatalf("swarmRegister: %v", err)
	}
	if !strings.Contains(buf.String(), "sp-xyz") {
		t.Fatalf("register output = %q, want the returned space id", buf.String())
	}
}

// TestSwarmRegisterNoManifest: registering from a dir without a manifest errors
// clearly and never touches the network.
func TestSwarmRegisterNoManifest(t *testing.T) {
	useServiceHome(t)
	t.Chdir(t.TempDir())
	if err := swarmRegister(&bytes.Buffer{}, ""); err == nil || !strings.Contains(err.Error(), "no evva-swarm.yml") {
		t.Fatalf("want a no-manifest error, got %v", err)
	}
}
