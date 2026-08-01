package agent

import (
	"log/slog"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/checkpoint"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/llm"
)

// newForkTestAgent builds the minimal Agent the session-lifecycle methods
// touch: a live session, a config with a real AppHome + workdir (so
// persistSession actually writes), a toolState, and a checkpoint manager.
func newForkTestAgent(t *testing.T) *Agent {
	t.Helper()
	home, workdir := t.TempDir(), t.TempDir()
	a := &Agent{
		ID:               "parent-id",
		logger:           slog.New(slog.DiscardHandler),
		session:          session.New(),
		toolState:        toolset.NewToolState(),
		sessionCreatedAt: time.Now().UTC(),
		workdir:          workdir,
		cfg:              &config.Config{AppHome: home, WorkDir: workdir},
		checkpoints:      checkpoint.NewManager(workdir, "parent-id", checkpoint.Retention{MaxCount: 10}, slog.New(slog.DiscardHandler)),
	}
	a.checkpoints.SetSession(a.ID)
	return a
}

func (a *Agent) testSlug() string { return memdir.ProjectKey(a.workdir) }

// The fork contract: the child carries the conversation forward under a
// new id, and the parent's file stops where the branch was taken.
func TestForkSessionBranchesAndLeavesParentIntact(t *testing.T) {
	a := newForkTestAgent(t)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "port the parser"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "on it"})

	childID, err := a.ForkSession()
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if childID == "parent-id" || childID == "" {
		t.Fatalf("fork must mint a new id; got %q", childID)
	}
	if a.ID != childID {
		t.Errorf("the agent should now BE the child; ID=%q child=%q", a.ID, childID)
	}
	if n := len(a.session.GetMessages()); n != 2 {
		t.Errorf("the conversation continues across a fork; got %d messages", n)
	}

	slug := a.testSlug()
	parent, err := session.Load(a.cfg.AppHome, slug, "parent-id")
	if err != nil {
		t.Fatalf("the parent must remain resumable: %v", err)
	}
	if parent.ParentID != "" {
		t.Errorf("the parent is not itself a fork; got parent_id=%q", parent.ParentID)
	}
	if len(parent.Session.Messages) != 2 {
		t.Errorf("the parent should hold the history as of the fork point; got %d", len(parent.Session.Messages))
	}

	child, err := session.Load(a.cfg.AppHome, slug, childID)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if child.ParentID != "parent-id" {
		t.Errorf("child lineage: got %q, want parent-id", child.ParentID)
	}
	if child.ForkedAtLen != 2 {
		t.Errorf("ForkedAtLen: got %d, want 2", child.ForkedAtLen)
	}
}

// The PRD's headline invariant — a fork's rewind cannot reach past the
// fork point — is not enforced anywhere. It falls out of checkpoints being
// namespaced by session id, and this test is what says so out loud.
func TestForkStartsWithAnEmptyCheckpointNamespace(t *testing.T) {
	a := newForkTestAgent(t)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "first turn"})
	a.checkpoints.Begin(0, 0, "first turn")
	if got := a.checkpoints.List(); len(got) != 1 {
		t.Fatalf("parent should have one checkpoint; got %d", len(got))
	}

	if _, err := a.ForkSession(); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if got := a.checkpoints.List(); len(got) != 0 {
		t.Errorf("a fork inherits no checkpoints, so /rewind cannot cross the fork point; got %d", len(got))
	}
}

func TestForkSessionGuards(t *testing.T) {
	a := newForkTestAgent(t)
	a.running.Store(true)
	if _, err := a.ForkSession(); err != ErrRunInProgress {
		t.Errorf("fork mid-run: got %v, want ErrRunInProgress", err)
	}
	a.running.Store(false)

	sub := newForkTestAgent(t)
	sub.Parent = a
	if _, err := sub.ForkSession(); err == nil {
		t.Error("a subagent has no persisted session to fork")
	}
}

func TestTitleAndPinWriteThroughToTheLiveSession(t *testing.T) {
	a := newForkTestAgent(t)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	a.persistSession()

	if err := a.SetSessionTitle("", "the big port"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	if err := a.PinSession("", true); err != nil {
		t.Fatalf("PinSession: %v", err)
	}
	// The in-memory envelope must carry them, or the very next auto-save
	// would silently restore the old values.
	if a.sessionMeta.Title != "the big port" || !a.sessionMeta.Pinned {
		t.Fatalf("live envelope not updated: %+v", a.sessionMeta)
	}

	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	a.persistSession()

	got, err := session.Load(a.cfg.AppHome, a.testSlug(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "the big port" || !got.Pinned {
		t.Errorf("a later auto-save overwrote the curation: title=%q pinned=%v", got.Title, got.Pinned)
	}
}

// Deleting the session you are sitting in would be a flicker, not a
// deletion: the next iteration boundary writes the file straight back.
func TestDeleteSessionRefusesTheLiveOne(t *testing.T) {
	a := newForkTestAgent(t)
	a.persistSession()
	if err := a.DeleteSession(a.ID); err == nil {
		t.Error("deleting the live session should be refused")
	}
}

// A resumed session keeps writing to the slug it was created under, so
// resuming from a sibling directory updates that session rather than
// forking a second copy of it under a new slug.
func TestSessionSlugFollowsTheResumedSnapshot(t *testing.T) {
	a := newForkTestAgent(t)
	if got, want := a.sessionSlug(), memdir.ProjectKey(a.workdir); got != want {
		t.Fatalf("a fresh agent uses its workdir slug: got %q want %q", got, want)
	}
	a.sessionMeta.WorkdirSlug = "-elsewhere"
	if got := a.sessionSlug(); got != "-elsewhere" {
		t.Errorf("a resumed session pins its slug: got %q", got)
	}
	if err := a.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if got, want := a.sessionSlug(), memdir.ProjectKey(a.workdir); got != want {
		t.Errorf("/clear returns to the current workdir's slug: got %q want %q", got, want)
	}
}
