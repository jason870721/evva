package shell

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/sandbox"
)

func requireRuntime(t *testing.T) string {
	t.Helper()
	for _, rt := range []string{"docker", "podman"} {
		if !sandbox.Available(rt) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := exec.CommandContext(ctx, rt, "info").Run()
		cancel()
		if err == nil {
			return rt
		}
	}
	t.Skip("no responding container runtime — skipping bash sandbox test")
	return ""
}

func runBash(t *testing.T, tool *BashTool, command string) string {
	t.Helper()
	in, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), slog.New(slog.NewTextHandler(os.Stderr, nil)), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("bash reported an error: %s", res.Content)
	}
	return res.Content
}

// The SBX-2 acceptance criterion: a command run by the real BashTool in a
// sandboxed session executes inside the container.
func TestBashRoutesThroughSandbox(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, release, err := sandbox.Provision(context.Background(), rt, "alpine:latest", "allow", root, "evva-bashtest")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	tool := NewBash(root).WithSandbox(func() *sandbox.Container { return c })
	if got := strings.TrimSpace(runBash(t, tool, "cat /etc/alpine-release")); got == "" {
		t.Fatal("expected the container's alpine marker")
	}
	if got := strings.TrimSpace(runBash(t, tool, "pwd")); got != sandbox.GuestRoot {
		t.Errorf("pwd = %q, want %q", got, sandbox.GuestRoot)
	}
}

// The nil-lookup path must be byte-identical to pre-SBX behavior — this is
// the regression guard for every unsandboxed session, which is nearly all of
// them.
func TestBashWithoutSandboxRunsOnHost(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "host-marker.txt")
	if err := os.WriteFile(marker, []byte("on-host"), 0o644); err != nil {
		t.Fatal(err)
	}

	plain := NewBash(dir)
	withNilLookup := NewBash(dir).WithSandbox(func() *sandbox.Container { return nil })

	for name, tool := range map[string]*BashTool{"plain": plain, "nil-lookup": withNilLookup} {
		got := strings.TrimSpace(runBash(t, tool, "cat host-marker.txt"))
		if got != "on-host" {
			t.Errorf("%s: got %q, want on-host", name, got)
		}
	}
}

// A sandboxed session whose workdir escaped the mount must fail loudly rather
// than silently run at the mount root.
func TestBashRefusesWorkdirOutsideMount(t *testing.T) {
	mount := t.TempDir()
	outside := t.TempDir()
	c := &sandbox.Container{ID: "fake", Runtime: "docker", HostRoot: mount}

	tool := NewBash(outside).WithSandbox(func() *sandbox.Container { return c })
	in, _ := json.Marshal(map[string]any{"command": "true"})
	res, err := tool.Execute(context.Background(), slog.New(slog.NewTextHandler(os.Stderr, nil)), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a refusal when the workdir is outside the mount")
	}
	if !strings.Contains(res.Content, "outside the container mount") {
		t.Errorf("error should explain the mismatch, got: %s", res.Content)
	}
}

// A sandboxed session's writes land in the worktree the host shares, which is
// what lets the fs tools and bash agree on the same tree.
func TestBashSandboxWritesVisibleOnHost(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, release, err := sandbox.Provision(context.Background(), rt, "alpine:latest", "allow", root, "evva-bashwrite")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	tool := NewBash(root).WithSandbox(func() *sandbox.Container { return c })
	runBash(t, tool, "echo built > artifact.txt")

	b, err := os.ReadFile(filepath.Join(root, "artifact.txt"))
	if err != nil {
		t.Fatalf("host should see the sandboxed write: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != "built" {
		t.Errorf("got %q, want built", got)
	}
}

// The timeout/kill-tree machinery is reused unchanged, so it must still govern
// a containerized command — an exec is just another subprocess.
func TestBashSandboxHonorsTimeout(t *testing.T) {
	rt := requireRuntime(t)
	root := t.TempDir()

	c, release, err := sandbox.Provision(context.Background(), rt, "alpine:latest", "allow", root, "evva-bashtimeout")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer release()

	tool := NewBash(root).WithSandbox(func() *sandbox.Container { return c })
	in, _ := json.Marshal(map[string]any{"command": "sleep 30", "timeout": 2000})

	start := time.Now()
	res, err := tool.Execute(context.Background(), slog.New(slog.NewTextHandler(os.Stderr, nil)), in)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 25*time.Second {
		t.Errorf("timeout did not govern the containerized command: took %s", elapsed)
	}
}
