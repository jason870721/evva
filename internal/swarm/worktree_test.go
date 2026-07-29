package swarm

import (
	"context"
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

// --- SWT-5: lifecycle durability ---------------------------------------

// gitInWorktree runs git inside a member's worktree with a test identity.
func gitInWorktree(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// A worktree survives the space and reattaches with its history intact — the
// restart / reconcile-rebuild contract.
func TestWorktreeSurvivesSpaceRebuild(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-restart", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	sess, _ := sp.memberWorktree("worker-a")
	if err := os.WriteFile(filepath.Join(sess.Path, "wip.txt"), []byte("worker work\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitInWorktree(t, sess.Path, "add", "wip.txt")
	gitInWorktree(t, sess.Path, "commit", "-q", "-m", "worker commit")
	want := strings.TrimSpace(gitInWorktree(t, sess.Path, "rev-parse", "HEAD"))
	sp.Shutdown()

	// Rebuild the space over the same workdir, as a restart would.
	sp2, err := NewSpace("space-restart", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("rebuild NewSpace: %v", err)
	}
	defer sp2.Shutdown()

	again, ok := sp2.memberWorktree("worker-a")
	if !ok {
		t.Fatal("worker-a should reattach to its worktree after a rebuild")
	}
	if filepath.Clean(again.Path) != filepath.Clean(sess.Path) || again.Branch != sess.Branch {
		t.Errorf("reattached elsewhere: %q/%q want %q/%q", again.Path, again.Branch, sess.Path, sess.Branch)
	}
	if got := strings.TrimSpace(gitInWorktree(t, again.Path, "rev-parse", "HEAD")); got != want {
		t.Errorf("history lost across rebuild: HEAD %q want %q", got, want)
	}
	if b, err := os.ReadFile(filepath.Join(again.Path, "wip.txt")); err != nil || string(b) != "worker work\n" {
		t.Errorf("committed work should survive: %q err %v", b, err)
	}
}

// RemoveMember on a CLEAN worktree removes it and its branch.
func TestRemoveMemberCleansWorktree(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-rm-clean", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sess, _ := sp.memberWorktree("worker-a")
	sup := NewSupervisor(sp)

	if err := sup.RemoveMember("worker-a"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Errorf("a clean worktree should be removed with the member; stat err = %v", err)
	}
	if branches := gitInWorktree(t, cfg.WorkDir, "branch", "--list", sess.Branch); strings.TrimSpace(branches) != "" {
		t.Errorf("branch %q should be deleted, still listed: %q", sess.Branch, branches)
	}
	if _, ok := sp.memberWorktree("worker-a"); ok {
		t.Error("the space should forget a removed member's worktree")
	}
}

// RemoveMember on a worktree holding work PRESERVES it and says so — removal
// is the accidental path, so it must never destroy work.
func TestRemoveMemberPreservesDirtyWorktree(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-rm-dirty", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()
	sess, _ := sp.memberWorktree("worker-a")
	if err := os.WriteFile(filepath.Join(sess.Path, "unsaved.txt"), []byte("in flight\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sup := NewSupervisor(sp)

	if err := sup.RemoveMember("worker-a"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Fatalf("a worktree holding work must be preserved: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(sess.Path, "unsaved.txt")); err != nil || string(b) != "in flight\n" {
		t.Errorf("the work itself must be untouched: %q err %v", b, err)
	}

	// Exactly one durable notice, naming the branch so the work is findable.
	msgs, err := sp.Store.ListMessages(50)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var preserved []string
	for _, m := range msgs {
		if m.Recipient == "user" && strings.Contains(m.Subject, "Worktree preserved") {
			preserved = append(preserved, m.Body)
		}
	}
	if len(preserved) != 1 {
		t.Fatalf("want exactly 1 preserved-worktree notice, got %d", len(preserved))
	}
	if !strings.Contains(preserved[0], sess.Branch) {
		t.Errorf("the notice must name the branch:\n%s", preserved[0])
	}
}

// Reset is the deliberate blank-slate path: it force-removes every member
// worktree and branch, uncommitted work included.
func TestResetWorktreesSweepsEverything(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-reset", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	a, _ := sp.memberWorktree("worker-a")
	b, _ := sp.memberWorktree("worker-b")
	// Dirty one of them — reset must take it anyway.
	if err := os.WriteFile(filepath.Join(a.Path, "unsaved.txt"), []byte("gone\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sp.Shutdown()

	ResetWorktrees(context.Background(), cfg.WorkDir, nil)

	for _, sess := range []struct {
		name string
		path string
		br   string
	}{{"worker-a", a.Path, a.Branch}, {"worker-b", b.Path, b.Branch}} {
		if _, err := os.Stat(sess.path); !os.IsNotExist(err) {
			t.Errorf("%s worktree should be gone after reset; stat err = %v", sess.name, err)
		}
		if out := gitInWorktree(t, cfg.WorkDir, "branch", "--list", sess.br); strings.TrimSpace(out) != "" {
			t.Errorf("%s branch should be deleted, still listed: %q", sess.name, out)
		}
	}
	// The base checkout itself is untouched.
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, "README")); err != nil {
		t.Errorf("reset must not touch the base checkout: %v", err)
	}
}

// --- SWT-6: observability ----------------------------------------------

// The roster column is what makes drift visible before it becomes a conflict
// pileup: branch, work waiting to merge, staleness, uncommitted files.
func TestWorktreeStatusForReportsDrift(t *testing.T) {
	cfg := gitStubConfig(t)
	loaded := testLoaded()
	loaded[2].Worktree = agentdef.WorktreeOff // worker-b stays shared
	sp, err := NewSpace("space-status", worktreeManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	// Fresh worktree: on its branch, nothing ahead, nothing behind, clean.
	got, ok := sp.WorktreeStatusFor("worker-a")
	if !ok {
		t.Fatal("worker-a is isolated and must report a status")
	}
	if got.Branch != "worktree-swarm-worker-a" || got.Ahead != 0 || got.Behind != 0 || got.Dirty != 0 {
		t.Errorf("fresh worktree status = %+v, want branch/0/0/0", got)
	}

	// A member on the shared workdir has no column at all.
	if _, ok := sp.WorktreeStatusFor("worker-b"); ok {
		t.Error("a shared-workdir member should report no worktree status")
	}
	if _, ok := sp.WorktreeStatusFor("leader"); ok {
		t.Error("the leader should report no worktree status")
	}

	// Commit in the worktree → ahead. Move base → behind. Leave a file → dirty.
	sess, _ := sp.memberWorktree("worker-a")
	if err := os.WriteFile(filepath.Join(sess.Path, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitInWorktree(t, sess.Path, "add", "work.txt")
	gitInWorktree(t, sess.Path, "commit", "-q", "-m", "worker work")
	if err := os.WriteFile(filepath.Join(cfg.WorkDir, "base.txt"), []byte("base moved\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	gitInWorktree(t, cfg.WorkDir, "add", "base.txt")
	gitInWorktree(t, cfg.WorkDir, "commit", "-q", "-m", "base work")
	if err := os.WriteFile(filepath.Join(sess.Path, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}
	gitInWorktree(t, sess.Path, "add", "scratch.txt")

	got, _ = sp.WorktreeStatusFor("worker-a")
	if got.Ahead != 1 {
		t.Errorf("Ahead = %d, want 1 (one commit waiting to merge)", got.Ahead)
	}
	if got.Behind != 1 {
		t.Errorf("Behind = %d, want 1 (base moved on)", got.Behind)
	}
	if got.Dirty != 1 {
		t.Errorf("Dirty = %d, want 1 (uncommitted file blocks a merge)", got.Dirty)
	}
}

// A conflict is the one merge outcome the operator should see without
// scraping transcripts — exactly one durable mail, naming the paths.
func TestMergeConflictMailsOperator(t *testing.T) {
	cfg := gitStubConfig(t)
	sp, err := NewSpace("space-conflictmail", worktreeManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	sess, _ := sp.memberWorktree("worker-a")
	if err := os.WriteFile(filepath.Join(sess.Path, "README"), []byte("worker\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitInWorktree(t, sess.Path, "commit", "-qam", "worker edit")
	if err := os.WriteFile(filepath.Join(cfg.WorkDir, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	gitInWorktree(t, cfg.WorkDir, "commit", "-qam", "base edit")

	res, err := sp.MergeMemberWorktree(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("MergeMemberWorktree: %v", err)
	}
	if len(res.Conflicts) == 0 {
		t.Fatal("expected a conflict")
	}

	msgs, err := sp.Store.ListMessages(50)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var mails []string
	for _, m := range msgs {
		if m.Recipient == "user" && strings.Contains(m.Subject, "Merge conflict") {
			mails = append(mails, m.Body)
		}
	}
	if len(mails) != 1 {
		t.Fatalf("want exactly 1 operator conflict mail, got %d", len(mails))
	}
	for _, want := range []string{"README", "worker-a", sess.Branch, "NOT applied"} {
		if !strings.Contains(mails[0], want) {
			t.Errorf("conflict mail missing %q:\n%s", want, mails[0])
		}
	}

	// A clean merge is routine — it must NOT page the operator.
	sp2Cfg := gitStubConfig(t)
	sp2, err := NewSpace("space-cleanmail", worktreeManifest(), testLoaded(), nil, sp2Cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp2.Shutdown()
	s2, _ := sp2.memberWorktree("worker-a")
	if err := os.WriteFile(filepath.Join(s2.Path, "ok.txt"), []byte("fine\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitInWorktree(t, s2.Path, "add", "ok.txt")
	gitInWorktree(t, s2.Path, "commit", "-q", "-m", "clean work")
	if _, err := sp2.MergeMemberWorktree(context.Background(), "worker-a"); err != nil {
		t.Fatalf("clean merge: %v", err)
	}
	msgs2, _ := sp2.Store.ListMessages(50)
	for _, m := range msgs2 {
		if strings.Contains(m.Subject, "Merge conflict") {
			t.Errorf("a clean merge must not mail the operator: %+v", m)
		}
	}
}
