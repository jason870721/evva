package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// integrationImage is deliberately tiny: these tests assert the plumbing, not
// the toolchain, and a 5 MB image keeps the first-run pull from dominating.
const integrationImage = "alpine:latest"

// requireRuntime skips unless a container runtime is genuinely usable — on
// PATH *and* with a responding daemon. `docker` installed but not running is
// a common developer state and must skip, not fail.
func requireRuntime(t *testing.T) string {
	t.Helper()
	for _, rt := range []string{"docker", "podman"} {
		if !Available(rt) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := exec.CommandContext(ctx, rt, "info").Run()
		cancel()
		if err == nil {
			return rt
		}
	}
	t.Skip("no responding container runtime (docker/podman) — skipping sandbox integration test")
	return ""
}

// The core promise: a command issued through the sandbox actually executes
// inside the container, not on the host. Proven by a marker only the container
// can see — the host filesystem has no /etc/alpine-release.
func TestIntegrationCommandRunsInsideContainer(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, release, err := Provision(context.Background(), rt, integrationImage, "allow", root, "evva-test-inside")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	out := mustExec(t, c, GuestRoot, "cat /etc/alpine-release")
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected alpine release marker from inside the container")
	}
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		t.Skip("host also has /etc/alpine-release — marker is not discriminating here")
	}
	t.Logf("container reported alpine %s; host has no such file", strings.TrimSpace(out))
}

// The bind mount is bidirectional and is what makes "fs tools write on the
// host, bash runs in the container" coherent: the agent must see its own
// build output.
func TestIntegrationBindMountIsShared(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	hostFile := filepath.Join(root, "from-host.txt")
	if err := os.WriteFile(hostFile, []byte("written-on-host"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, release, err := Provision(context.Background(), rt, integrationImage, "allow", root, "evva-test-mount")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	if got := strings.TrimSpace(mustExec(t, c, GuestRoot, "cat from-host.txt")); got != "written-on-host" {
		t.Errorf("container should read host-written file, got %q", got)
	}

	mustExec(t, c, GuestRoot, "echo written-in-container > from-container.txt")
	b, err := os.ReadFile(filepath.Join(root, "from-container.txt"))
	if err != nil {
		t.Fatalf("host should see the container's write: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != "written-in-container" {
		t.Errorf("got %q, want written-in-container", got)
	}
}

// The isolation claim itself: paths outside the mount are simply not there.
func TestIntegrationHostFilesystemIsOutOfReach(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("host-only"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, release, err := Provision(context.Background(), rt, integrationImage, "allow", root, "evva-test-isolation")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	cmd := exec.Command(c.Runtime, c.ExecArgs(GuestRoot, "/bin/sh", "cat "+secret)...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("reading a host path from inside the container should fail, got output: %q", out)
	}
	if strings.Contains(string(out), "host-only") {
		t.Fatal("container read a file outside its mount — isolation is not holding")
	}
}

// The nested-workdir mapping the audit pass flagged as missing from the PRD:
// a session whose workdir sits below the mount root must run there, not at the
// root.
func TestIntegrationNestedWorkdirRunsInRightPlace(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()
	nested := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "marker.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, release, err := Provision(context.Background(), rt, integrationImage, "allow", root, "evva-test-nested")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	guestDir, ok := c.GuestPath(nested)
	if !ok {
		t.Fatal("nested workdir should map into the container")
	}
	if got := strings.TrimSpace(mustExec(t, c, guestDir, "pwd")); got != guestDir {
		t.Errorf("pwd = %q, want %q", got, guestDir)
	}
	// Relative read only succeeds from the right directory.
	if got := strings.TrimSpace(mustExec(t, c, guestDir, "cat marker.txt")); got != "nested" {
		t.Errorf("relative read from nested workdir = %q, want nested", got)
	}
}

// sandbox_network:"none" is the whole network surface, so it has to actually
// cut the network.
func TestIntegrationNetworkNoneIsIsolated(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, release, err := Provision(context.Background(), rt, integrationImage, "none", root, "evva-test-nonet")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	cmd := exec.Command(c.Runtime, c.ExecArgs(GuestRoot, "/bin/sh", "wget -T 3 -q -O- http://example.com")...)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("network should be unreachable with sandbox_network=none, got: %q", out)
	}
	if c.Network != "none" {
		t.Errorf("Network = %q, want none", c.Network)
	}
	if !strings.Contains(c.Describe(), "no-network") {
		t.Errorf("Describe should mark the stance, got %q", c.Describe())
	}
}

// Teardown must actually remove the container, and must tolerate being called
// when it is already gone — a teardown race is normal, not a failure.
func TestIntegrationStopRemovesContainerAndIsIdempotent(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, err := Start(context.Background(), Options{
		Runtime: rt, Image: integrationImage, Network: "allow",
		HostRoot: root, Label: "evva-test-teardown",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !containerExists(t, rt, c.ID) {
		t.Fatal("container should exist right after Start")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if containerExists(t, rt, c.ID) {
		t.Error("container should be gone after Stop")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Errorf("second Stop should be a no-op, got: %v", err)
	}
}

// Stop uses context.WithoutCancel precisely so a cancelled session still
// cleans up — cancellation is usually why teardown is happening.
func TestIntegrationStopWorksWithCancelledContext(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, err := Start(context.Background(), Options{
		Runtime: rt, Image: integrationImage, HostRoot: root, Label: "evva-test-cancelled",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop with a cancelled ctx must still work: %v", err)
	}
	if containerExists(t, rt, c.ID) {
		t.Error("container should be gone even though the ctx was cancelled")
	}
}

// End-to-end through the production entry point: Provision registers in
// Default so a workdir-keyed lookup finds the container, and release undoes
// both halves.
func TestIntegrationProvisionRegistersAndReleases(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, release, err := Provision(context.Background(), rt, integrationImage, "allow", root, "evva-test-registry")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := Default.Lookup(root); got == nil || got.ID != c.ID {
		t.Fatalf("Provision should bind the workdir in Default, got %v", got)
	}
	nested := filepath.Join(root, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Default.Lookup(nested); got == nil || got.ID != c.ID {
		t.Errorf("a nested workdir should resolve to the same container, got %v", got)
	}

	release()
	if got := Default.Lookup(root); got != nil {
		t.Errorf("release should unbind, got %v", got)
	}
	if containerExists(t, rt, c.ID) {
		t.Error("release should stop the container")
	}
}

func mustExec(t *testing.T, c *Container, guestDir, command string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Runtime, c.ExecArgs(guestDir, "/bin/sh", command)...).CombinedOutput()
	if err != nil {
		t.Fatalf("exec %q: %v\n%s", command, err, out)
	}
	return string(out)
}

func containerExists(t *testing.T, rt, id string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, rt, "ps", "-aq", "--filter", "id="+id).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
