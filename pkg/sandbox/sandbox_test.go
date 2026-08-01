package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveImageExplicitWins(t *testing.T) {
	dir := t.TempDir()
	writeDevcontainer(t, dir, `{"image": "from-devcontainer"}`)
	got, err := ResolveImage(dir, "explicit:tag")
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if got != "explicit:tag" {
		t.Errorf("explicit config should beat devcontainer.json, got %q", got)
	}
}

func TestResolveImageFromDevcontainer(t *testing.T) {
	dir := t.TempDir()
	writeDevcontainer(t, dir, `{"name": "x", "image": "golang:1.23"}`)
	got, err := ResolveImage(dir, "")
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if got != "golang:1.23" {
		t.Errorf("got %q, want golang:1.23", got)
	}
}

// Real devcontainer.json files are routinely jsonc — VS Code accepts comments
// and trailing commas, so a parser that does not would reject configs that
// work everywhere else.
func TestResolveImageJSONCTolerant(t *testing.T) {
	dir := t.TempDir()
	writeDevcontainer(t, dir, `{
  // the base image
  "name": "demo",
  /* block comment */
  "image": "alpine:3.20",
}`)
	got, err := ResolveImage(dir, "")
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if got != "alpine:3.20" {
		t.Errorf("got %q, want alpine:3.20", got)
	}
}

// A "//" inside a string value must survive comment stripping.
func TestResolveImageKeepsURLInString(t *testing.T) {
	dir := t.TempDir()
	writeDevcontainer(t, dir, `{"image": "ghcr.io/o/i:1", "docs": "https://example.com/x"}`)
	got, err := ResolveImage(dir, "")
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if got != "ghcr.io/o/i:1" {
		t.Errorf("got %q", got)
	}
}

// The refuse-loudly invariant: no image anywhere is an error, never a silent
// fallback to unsandboxed execution.
func TestResolveImageRefusesWithoutSource(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveImage(dir, "")
	if err == nil {
		t.Fatal("want an error when there is no devcontainer.json and no explicit image")
	}
	if !strings.Contains(err.Error(), "sandbox_image") {
		t.Errorf("error should name the way forward, got: %v", err)
	}
}

func TestResolveImageRefusesDockerfileBuild(t *testing.T) {
	dir := t.TempDir()
	writeDevcontainer(t, dir, `{"build": {"dockerfile": "Dockerfile"}}`)
	_, err := ResolveImage(dir, "")
	if err == nil {
		t.Fatal("want an error for a build-only devcontainer")
	}
	if !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("error should explain the Dockerfile limitation, got: %v", err)
	}
}

func TestGuestPathMapsNestedWorkdir(t *testing.T) {
	root := t.TempDir()
	c := &Container{HostRoot: root}

	if got, ok := c.GuestPath(root); !ok || got != GuestRoot {
		t.Errorf("root should map to %s, got %q ok=%v", GuestRoot, got, ok)
	}

	nested := filepath.Join(root, "svc", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := c.GuestPath(nested)
	if !ok {
		t.Fatal("nested path should map")
	}
	if want := GuestRoot + "/svc/api"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The mapping must refuse rather than silently clamp: running in the wrong
// directory corrupts results far more quietly than failing does.
func TestGuestPathRejectsOutsideMount(t *testing.T) {
	root := t.TempDir()
	c := &Container{HostRoot: root}
	if _, ok := c.GuestPath(filepath.Dir(root)); ok {
		t.Error("a parent of the mount must not map")
	}
	if _, ok := c.GuestPath(t.TempDir()); ok {
		t.Error("an unrelated directory must not map")
	}
}

// A prefix that merely shares characters is not containment: /repo-backup is
// not inside /repo.
func TestGuestPathRejectsSiblingPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	sibling := filepath.Join(base, "repo-backup")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := &Container{HostRoot: root}
	if _, ok := c.GuestPath(sibling); ok {
		t.Error("repo-backup must not be treated as inside repo")
	}
}

func TestExecArgs(t *testing.T) {
	c := &Container{ID: "abc123", Runtime: "docker"}
	got := c.ExecArgs("/workspace/svc", "/bin/sh", "echo hi")
	want := []string{"exec", "-w", "/workspace/svc", "abc123", "/bin/sh", "-c", "echo hi"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestExecArgsOmitsEmptyWorkdir(t *testing.T) {
	c := &Container{ID: "abc", Runtime: "podman"}
	got := c.ExecArgs("", "/bin/sh", "true")
	for _, a := range got {
		if a == "-w" {
			t.Fatalf("empty workdir should not emit -w: %v", got)
		}
	}
}

func TestRegistryLongestPrefixWins(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	outerC := &Container{ID: "outer", HostRoot: outer}
	innerC := &Container{ID: "inner", HostRoot: inner}

	r := &Registry{}
	defer r.Register(outer, outerC)()
	defer r.Register(inner, innerC)()

	if got := r.Lookup(inner); got == nil || got.ID != "inner" {
		t.Errorf("nested path must resolve to the innermost container, got %v", got)
	}
	if got := r.Lookup(outer); got == nil || got.ID != "outer" {
		t.Errorf("outer path should resolve to outer, got %v", got)
	}
	if got := r.Lookup(base); got != nil {
		t.Errorf("a path above every root must not resolve, got %v", got)
	}
}

func TestRegistryReleaseUnbinds(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{}
	release := r.Register(dir, &Container{ID: "c1", HostRoot: dir})
	if r.Lookup(dir) == nil {
		t.Fatal("expected a hit after Register")
	}
	release()
	if got := r.Lookup(dir); got != nil {
		t.Errorf("release must unbind, got %v", got)
	}
	if r.Len() != 0 {
		t.Errorf("Len = %d, want 0", r.Len())
	}
}

// The overwhelmingly common case: nothing registered, so the bash hot path
// must resolve to nil without ceremony.
func TestRegistryEmptyLookupIsNil(t *testing.T) {
	r := &Registry{}
	if got := r.Lookup(t.TempDir()); got != nil {
		t.Errorf("empty registry must return nil, got %v", got)
	}
	if got := r.Lookup(""); got != nil {
		t.Errorf("empty path must return nil, got %v", got)
	}
}

// Provision with no runtime is the "sandboxing is off" path — not an error,
// and it must not register anything.
func TestProvisionOffIsNoop(t *testing.T) {
	c, release, err := Provision(context.Background(), "", "", "", t.TempDir(), "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil {
		t.Errorf("expected nil container, got %v", c)
	}
	release() // must not panic
}

func TestAvailableRejectsMissingRuntime(t *testing.T) {
	if Available("") {
		t.Error("empty runtime is never available")
	}
	if Available("definitely-not-a-real-runtime-binary") {
		t.Error("a missing binary must not report available")
	}
}

func TestStartRefusesMissingRuntime(t *testing.T) {
	_, err := Start(context.Background(), Options{
		Runtime:  "definitely-not-a-real-runtime-binary",
		Image:    "alpine",
		HostRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("want an error when the runtime is not on PATH")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error should say the binary is missing, got: %v", err)
	}
}

func writeDevcontainer(t *testing.T, dir, body string) {
	t.Helper()
	dc := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(dc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dc, "devcontainer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
