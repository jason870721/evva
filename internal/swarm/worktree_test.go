package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/pkg/config"
)

func mkSession(path, repoRoot string) mode.WorktreeSession {
	return mode.WorktreeSession{Path: path, RepoRoot: repoRoot}
}

// SWT-3: per-member worktree isolation wiring. The property under test is
// twofold — the member's EDITING surface moves into its own worktree, while
// every piece of space state stays anchored at the root workdir.

// workdirer is the accessor the constructed agent exposes; asserted through an
// interface so the test does not depend on the concrete agent type.
type workdirer interface{ Workdir() string }

// memberLiveWorkdir reads a constructed member's effective workdir. The public
// Agent interface deliberately does not expose it; Controller() hands back the
// inner agent, which does.
func memberLiveWorkdir(t *testing.T, sp *SwarmSpace, name string) string {
	t.Helper()
	ctrl, ok := sp.Roster.Controller(name)
	if !ok {
		t.Fatalf("member %q not in the roster", name)
	}
	w, ok := ctrl.(workdirer)
	if !ok {
		t.Fatalf("member %q controller does not expose Workdir()", name)
	}
	return w.Workdir()
}

// initRepoAt makes dir a git repo carrying the cross-platform trio the
// worktree stack's fixtures established (repo-local identity, autocrlf off).
func initRepoAt(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README")
	run("commit", "-q", "-m", "init")
}

// gitStubConfig is stubConfig with a git repo at the workdir. The workdir is
// canonicalized up front: `git rev-parse --show-toplevel` resolves symlinks
// (macOS /var → /private/var), and the nested-workdir mapping compares the two.
func gitStubConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := stubConfig(t)
	resolved, err := filepath.EvalSymlinks(cfg.WorkDir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	cfg.WorkDir = resolved
	initRepoAt(t, cfg.WorkDir)
	return cfg
}

func worktreeManifest() agentdef.Manifest {
	m := testManifest()
	m.Settings.WorktreeIsolation = true
	return m
}

func TestWorktreeIsolationInjectsMemberWorkdir(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-wt", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	for _, name := range []string{"worker-a", "worker-b"} {
		sess, ok := sp.memberWorktree(name)
		if !ok {
			t.Fatalf("%s should run under worktree isolation", name)
		}
		want := filepath.Join(cfg.WorkDir, ".evva", "worktrees", "swarm-"+name)
		if filepath.Clean(sess.Path) != filepath.Clean(want) {
			t.Errorf("%s worktree path = %q, want %q", name, sess.Path, want)
		}
		if sess.Branch != "worktree-swarm-"+name {
			t.Errorf("%s branch = %q", name, sess.Branch)
		}
		if _, sErr := os.Stat(sess.Path); sErr != nil {
			t.Errorf("%s worktree dir should exist: %v", name, sErr)
		}
		// The agent actually edits there — that is the whole point.
		if got := memberLiveWorkdir(t, sp, name); filepath.Clean(got) != filepath.Clean(sess.Path) {
			t.Errorf("%s agent workdir = %q, want the worktree %q", name, got, sess.Path)
		}
	}

	// D8: the leader integrates onto the base checkout, so it stays on it.
	if _, ok := sp.memberWorktree("leader"); ok {
		t.Error("the leader must never get a worktree")
	}
	if got := memberLiveWorkdir(t, sp, "leader"); filepath.Clean(got) != filepath.Clean(cfg.WorkDir) {
		t.Errorf("leader workdir = %q, want the space root %q", got, cfg.WorkDir)
	}

	// Two workers on two branches is the correctness property: concurrent
	// edits to the same file cannot collide.
	a, _ := sp.memberWorktree("worker-a")
	b, _ := sp.memberWorktree("worker-b")
	if a.Path == b.Path || a.Branch == b.Branch {
		t.Errorf("workers share a worktree: %+v vs %+v", a, b)
	}
}

// D7: everything that is space state — not member editing surface — must stay
// anchored at the ROOT workdir even when the member's cwd moves.
func TestWorktreeIsolationKeepsStateRootAnchored(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-anchor", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	sess, ok := sp.memberWorktree("worker-a")
	if !ok {
		t.Fatal("worker-a should be isolated")
	}

	// The ledger lives at the root, never in a worktree.
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, ".vero")); err != nil {
		t.Errorf(".vero should sit at the space root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sess.Path, ".vero")); !os.IsNotExist(err) {
		t.Errorf("no .vero should be created inside a member worktree; stat err = %v", err)
	}

	// Member memory is root-anchored (agentdef.MemoryDir keys off sp.Workdir).
	memDir := agentdef.MemoryDir(cfg.WorkDir, agentdef.RoleWorker, "worker-a")
	if _, err := os.Stat(memDir); err != nil {
		t.Errorf("member memory dir should be created under the root: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(memDir), filepath.Clean(cfg.WorkDir)) ||
		strings.Contains(filepath.ToSlash(memDir), "/.evva/worktrees/") {
		t.Errorf("memory dir escaped the root: %q", memDir)
	}

	// D7 fix 3 — the subtlest change in the wave, asserted behaviorally: run
	// the isolated member and check WHERE its transcript lands. It must be the
	// ROOT workdir's slug; under the worktree's slug, ResetSpace /
	// ClearMemberSession / restart-resume would all silently miss it.
	if _, err := sp.agents["worker-a"].Run(sp.ctx, "hello"); err != nil {
		t.Fatalf("run worker-a: %v", err)
	}
	rootSessions := session.SessionsDir(cfg.AppHome, memdir.ProjectKey(cfg.WorkDir))
	if n := countJSON(t, rootSessions); n == 0 {
		t.Errorf("no transcript under the ROOT slug %s — the session followed the worktree", rootSessions)
	}
	wtSessions := session.SessionsDir(cfg.AppHome, memdir.ProjectKey(sess.Path))
	if n := countJSON(t, wtSessions); n != 0 {
		t.Errorf("%d transcript(s) leaked under the WORKTREE slug %s", n, wtSessions)
	}
}

// countJSON counts *.json files in dir; a missing dir counts as 0.
func countJSON(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// The per-member escape hatch for mixed teams.
func TestWorktreeIsolationPerMemberOverride(t *testing.T) {
	cfg := gitStubConfig(t)
	loaded := testLoaded()
	loaded[2].Worktree = agentdef.WorktreeOff // worker-b opts out of a space-wide on

	sp, err := NewSpace("space-mixed", worktreeManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	if _, ok := sp.memberWorktree("worker-a"); !ok {
		t.Error("worker-a should inherit the space-wide on")
	}
	if _, ok := sp.memberWorktree("worker-b"); ok {
		t.Error("worker-b opted out and must stay on the shared workdir")
	}
	if got := memberLiveWorkdir(t, sp, "worker-b"); filepath.Clean(got) != filepath.Clean(cfg.WorkDir) {
		t.Errorf("opted-out member workdir = %q, want root %q", got, cfg.WorkDir)
	}

	// The inverse: a member opting IN while the space is off.
	cfg2 := gitStubConfig(t)
	loaded2 := testLoaded()
	loaded2[1].Worktree = agentdef.WorktreeOn
	sp2, err := NewSpace("space-optin", testManifest(), loaded2, nil, cfg2)
	if err != nil {
		t.Fatalf("NewSpace(opt-in): %v", err)
	}
	defer sp2.Shutdown()
	if _, ok := sp2.memberWorktree("worker-a"); !ok {
		t.Error("worker-a opted in and should be isolated even with the space off")
	}
	if _, ok := sp2.memberWorktree("worker-b"); ok {
		t.Error("worker-b should stay shared")
	}
}

// §5.2: fail-fast, never degrade silently — a degrade would drop an isolation
// property the operator explicitly asked for.
func TestWorktreeIsolationNonRepoFailsRegister(t *testing.T) {
	cfg := stubConfig(t) // deliberately NOT a git repo
	_, err := NewSpace("space-norepo", worktreeManifest(), testLoaded(), nil, cfg)
	if err == nil {
		t.Fatal("a non-repo workdir with isolation enabled must fail the register")
	}
	for _, want := range []string{"worktree isolation", "not a git repository"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got %v", want, err)
		}
	}

	// A space whose members all opt out needs no repo at all.
	cfg2 := stubConfig(t)
	loaded := testLoaded()
	for i := range loaded {
		loaded[i].Worktree = agentdef.WorktreeOff
	}
	sp, err := NewSpace("space-allrooted", worktreeManifest(), loaded, nil, cfg2)
	if err != nil {
		t.Fatalf("all-opted-out space should not need a repo: %v", err)
	}
	sp.Shutdown()
}

// Flag off = byte-identical to the pre-SWT space.
func TestWorktreeIsolationOffChangesNothing(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-off", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	for _, name := range []string{"leader", "worker-a", "worker-b"} {
		if _, ok := sp.memberWorktree(name); ok {
			t.Errorf("%s should have no worktree when the knob is off", name)
		}
		if got := memberLiveWorkdir(t, sp, name); filepath.Clean(got) != filepath.Clean(cfg.WorkDir) {
			t.Errorf("%s workdir = %q, want the shared root %q", name, got, cfg.WorkDir)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, ".evva", "worktrees")); !os.IsNotExist(err) {
		t.Errorf("no worktrees dir should be created when the knob is off; stat err = %v", err)
	}
}

// §5.1: a space workdir nested inside a larger repo keeps the same
// project-relative cwd inside the worktree.
func TestMemberWorkdirNestedSpace(t *testing.T) {
	repoRoot := "/repo"
	sess := mkSession("/repo/.evva/worktrees/swarm-qa", repoRoot)

	// Space workdir IS the repo root → the worktree root.
	if got := memberWorkdir(sess, repoRoot); got != sess.Path {
		t.Errorf("root-level space: got %q want %q", got, sess.Path)
	}
	// Space workdir nested → same relative position inside the worktree.
	want := filepath.Join(sess.Path, "services", "api")
	if got := memberWorkdir(sess, filepath.Join(repoRoot, "services", "api")); got != want {
		t.Errorf("nested space: got %q want %q", got, want)
	}
	// No RepoRoot recorded (single-agent sessions) → the worktree root.
	if got := memberWorkdir(mkSession("/x/wt", ""), "/anything"); got != "/x/wt" {
		t.Errorf("no repo root: got %q want /x/wt", got)
	}
}

// D7 fix 2: the memory-index path in the wake reminder is workdir-relative,
// which only resolves from the space root — an isolated member gets the
// absolute path instead.
func TestMemoryWakeReminderPathUnderIsolation(t *testing.T) {
	cfg := gitStubConfig(t)
	loaded := testLoaded()
	loaded[2].Worktree = agentdef.WorktreeOff // worker-b stays on the root
	sp, err := NewSpace("space-memidx", worktreeManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	for _, name := range []string{"worker-a", "worker-b"} {
		dir := agentdef.MemoryDir(cfg.WorkDir, agentdef.RoleWorker, name)
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [X](x.md) — hook\n"), 0o644); err != nil {
			t.Fatalf("write MEMORY.md: %v", err)
		}
	}

	isolated := sp.memoryWakeReminder("worker-a")
	if !strings.Contains(isolated, filepath.ToSlash(cfg.WorkDir)) {
		t.Errorf("an isolated member needs the ABSOLUTE index path (its cwd is the worktree); got:\n%s", isolated)
	}

	shared := sp.memoryWakeReminder("worker-b")
	if strings.Contains(shared, filepath.ToSlash(cfg.WorkDir)) {
		t.Errorf("a shared-workdir member keeps the relative path; got:\n%s", shared)
	}
	if !strings.Contains(shared, "agents/sub/worker-b/memory/MEMORY.md") {
		t.Errorf("shared member should keep the workdir-relative path; got:\n%s", shared)
	}
}
