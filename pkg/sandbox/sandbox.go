// Package sandbox runs an agent session's shell commands inside a container
// instead of directly on the host.
//
// # The axis this is on
//
// evva's older vocabulary already uses the word "sandbox" for the
// permission-approval layer — the dead `dangerouslyDisableSandbox` bash
// parameter, and pkg/permission's "a guardrail for the file tools, not a
// sandbox". This package is a DIFFERENT axis: OS-level process, filesystem
// and network isolation, orthogonal to whether an action needed approval. A
// command can be permission-approved and still run unsandboxed (the default),
// or sandboxed.
//
// # Shape
//
// A sandbox is a worktree plus a bind mount. The session's git worktree stays
// on the host — the fs edit/write tools keep writing to it directly — and a
// long-lived container mounts it at GuestRoot. Shell commands then run as
// `<runtime> exec <container> <shell> -c <cmd>` instead of a host subprocess.
// One `run` per session amortizes container startup across many cheap `exec`s,
// mirroring how the bash daemon already amortizes a long-lived process.
//
// What this isolates: the rest of the host filesystem (no ~/.ssh, no sibling
// repos, no `rm -rf /` outside the mount), arbitrary installs (`curl | sh`
// lands in a disposable container), and — with Network "none" — the network.
// What it does NOT isolate: the worktree itself, which the container reads and
// writes freely by design, because the agent needs to see its own build
// output. Git-tree isolation and OS isolation are complementary boundaries:
// one protects the rest of the repo from this session, the other protects the
// rest of the host from it.
//
// Zero new dependencies: this shells out to whatever `docker` or `podman` is
// already on PATH, the same external-binary pattern evva already uses for git.
//
// See docs/roadmap/PRD/sandbox-isolation.md.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GuestRoot is where a sandboxed session's worktree is mounted inside the
// container. Fixed rather than configurable: the path is an implementation
// detail the model never needs to reason about, and pinning it keeps
// GuestPath's mapping trivially invertible.
const GuestRoot = "/workspace"

// startTimeout bounds container creation. Generous because a cold first run
// may pull the image; the PRD accepts pull latency as a real, documented cost
// rather than trying to pre-warm.
const startTimeout = 10 * time.Minute

// stopTimeout bounds teardown. Short — `rm -f` on a container that is already
// gone should not hold a session's exit hostage.
const stopTimeout = 30 * time.Second

// Options configures one sandboxed session. Runtime is required; the zero
// value of the package means "sandboxing off" and callers should not reach
// Start at all.
type Options struct {
	// Runtime is "docker" or "podman".
	Runtime string
	// Image overrides devcontainer.json resolution. When empty, Start reads
	// <repo>/.devcontainer/devcontainer.json; with neither, it fails.
	Image string
	// Network is "allow" (default) or "none".
	Network string
	// HostRoot is the host directory bind-mounted at GuestRoot — the session's
	// worktree checkout.
	HostRoot string
	// Label is a human-readable session name folded into the container name so
	// `docker ps` is legible to the operator.
	Label string
}

// Container is a live sandbox. It is immutable after Start; the zero value is
// not usable. A nil *Container means "not sandboxed" and every consumer treats
// it that way, which is what makes the bash routing branch nil-safe.
type Container struct {
	// ID is the container id returned by `run -d`.
	ID string
	// Runtime is the binary that owns ID — needed for every later exec/rm.
	Runtime string
	// HostRoot is the bind-mount source, kept for GuestPath's mapping.
	HostRoot string
	// Image is what actually got run, for observability.
	Image string
	// Network is the effective stance, for observability.
	Network string
}

// ErrNoImage is returned when neither devcontainer.json nor an explicit image
// is available. Callers must surface it, never swallow it: falling back to
// unsandboxed execution here would be a silent safety downgrade at exactly the
// moment the operator thought they had opted into isolation.
var ErrNoImage = errors.New("sandbox: no image")

// Available reports whether the named runtime is on PATH. Checked at session
// start rather than at config load, since a config may legitimately be written
// on one machine and run on another.
func Available(runtime string) bool {
	if runtime == "" {
		return false
	}
	_, err := exec.LookPath(runtime)
	return err == nil
}

// ResolveImage picks the image for a sandboxed session rooted at repoRoot.
//
// Explicit config wins; otherwise <repoRoot>/.devcontainer/devcontainer.json's
// "image" key is used, piggybacking on a convention VS Code Dev Containers and
// GitHub Codespaces already established so evva need not invent image-
// selection UX. A devcontainer that only specifies a build (dockerFile /
// build.dockerfile) is deliberately NOT handled: building an image brings
// build context and cache questions this tier does not answer, so it reports
// a clear error naming sandbox_image as the way forward.
func ResolveImage(repoRoot, explicit string) (string, error) {
	if img := strings.TrimSpace(explicit); img != "" {
		return img, nil
	}
	path := filepath.Join(repoRoot, ".devcontainer", "devcontainer.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: no %s and no sandbox_image configured — sandboxing needs an image", ErrNoImage, path)
	}
	var dc struct {
		Image      string `json:"image"`
		DockerFile string `json:"dockerFile"`
		Build      struct {
			Dockerfile string `json:"dockerfile"`
		} `json:"build"`
	}
	if err := json.Unmarshal(stripJSONComments(raw), &dc); err != nil {
		return "", fmt.Errorf("%w: %s is not valid JSON: %v", ErrNoImage, path, err)
	}
	if img := strings.TrimSpace(dc.Image); img != "" {
		return img, nil
	}
	if dc.DockerFile != "" || dc.Build.Dockerfile != "" {
		return "", fmt.Errorf("%w: %s builds its image from a Dockerfile, which this tier does not do — set sandbox_image to a prebuilt image", ErrNoImage, path)
	}
	return "", fmt.Errorf("%w: %s has no \"image\" key — set sandbox_image instead", ErrNoImage, path)
}

// Start creates and runs the session container. The container runs `sleep
// infinity` as PID 1 and does nothing on its own; all work arrives later via
// Exec. --rm means an abandoned container still disappears when it stops, so a
// crashed evva cannot leak one indefinitely.
func Start(ctx context.Context, opt Options) (*Container, error) {
	if !Available(opt.Runtime) {
		return nil, fmt.Errorf("sandbox: %q is not on PATH — install it or unset sandbox_runtime", opt.Runtime)
	}
	if opt.HostRoot == "" {
		return nil, errors.New("sandbox: HostRoot is empty")
	}
	abs, err := filepath.Abs(opt.HostRoot)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve %s: %w", opt.HostRoot, err)
	}
	image, err := ResolveImage(abs, opt.Image)
	if err != nil {
		return nil, err
	}

	args := []string{
		"run", "-d", "--rm",
		"-v", abs + ":" + GuestRoot,
		"-w", GuestRoot,
		"--name", containerName(opt.Label),
	}
	if opt.Network == "none" {
		args = append(args, "--network", "none")
	}
	args = append(args, image, "sleep", "infinity")

	cctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, opt.Runtime, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sandbox: %s run failed: %w\n%s", opt.Runtime, err, strings.TrimSpace(string(out)))
	}
	id := strings.TrimSpace(string(out))
	// `run -d` prints the id last; a pull writes progress lines before it.
	if i := strings.LastIndexByte(id, '\n'); i >= 0 {
		id = strings.TrimSpace(id[i+1:])
	}
	if id == "" {
		return nil, fmt.Errorf("sandbox: %s run returned no container id", opt.Runtime)
	}
	network := opt.Network
	if network == "" {
		network = "allow"
	}
	return &Container{ID: id, Runtime: opt.Runtime, HostRoot: abs, Image: image, Network: network}, nil
}

// Stop removes the container. Safe to call twice and on an already-dead
// container: `rm -f` on a missing id is an error we deliberately swallow,
// because teardown races (the --rm already fired) are normal, not failures.
// Uses context.WithoutCancel so a cancelled session still cleans up — the
// cancellation is usually WHY we are tearing down.
func (c *Container) Stop(ctx context.Context) error {
	if c == nil || c.ID == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, c.Runtime, "rm", "-f", c.ID).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("sandbox: %s rm %s: %w\n%s", c.Runtime, short(c.ID), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GuestPath maps a host path onto its location inside the container.
//
// This exists because cmd.Dir is a host concept that means nothing to
// `<runtime> exec`. A sandboxed session's workdir is not always the mount root
// — the swarm hands a member the worktree-relative position of a space nested
// inside a larger repo — so the working directory has to be translated and
// passed as `exec -w`, not inherited.
//
// Returns false for a path outside HostRoot, which callers treat as a hard
// error rather than silently running somewhere else.
func (c *Container) GuestPath(hostPath string) (string, bool) {
	if c == nil {
		return "", false
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(c.HostRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return GuestRoot, true
	}
	return GuestRoot + "/" + filepath.ToSlash(rel), true
}

// ExecArgs builds the argv that runs one shell command inside the container.
// The caller still owns timeout, process-group and kill-tree handling: an
// `exec` is itself an ordinary subprocess, so the existing bash machinery
// governs it unchanged.
func (c *Container) ExecArgs(guestDir, shell, command string) []string {
	args := []string{"exec"}
	if guestDir != "" {
		args = append(args, "-w", guestDir)
	}
	return append(args, c.ID, shell, "-c", command)
}

// Describe renders the container for operator-facing listings.
func (c *Container) Describe() string {
	if c == nil {
		return ""
	}
	s := fmt.Sprintf("%s %s (%s)", c.Runtime, short(c.ID), c.Image)
	if c.Network == "none" {
		s += " no-network"
	}
	return s
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// containerName composes a legible, collision-resistant container name so an
// operator running `docker ps` can tell which evva session owns what.
func containerName(label string) string {
	flat := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, label)
	flat = strings.Trim(flat, "-")
	if flat == "" {
		flat = "session"
	}
	if len(flat) > 40 {
		flat = flat[:40]
	}
	return fmt.Sprintf("evva-%s-%d", flat, time.Now().UnixNano()%1e9)
}

// stripJSONComments removes // line comments and trailing commas so a
// devcontainer.json written in "jsonc" (which the VS Code tooling accepts, and
// real-world files use freely) still parses. Deliberately small: it skips
// anything inside a string literal and does not attempt block comments in
// pathological positions.
func stripJSONComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		ch := b[i]
		if inStr {
			out = append(out, ch)
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch {
		case ch == '"':
			inStr = true
			out = append(out, ch)
		case ch == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case ch == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, ch)
		}
	}
	return dropTrailingCommas(out)
}

func dropTrailingCommas(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		ch := b[i]
		if inStr {
			out = append(out, ch)
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		if ch == '"' {
			inStr = true
			out = append(out, ch)
			continue
		}
		if ch == ',' {
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue
			}
		}
		out = append(out, ch)
	}
	return out
}
