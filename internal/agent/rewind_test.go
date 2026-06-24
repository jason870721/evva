package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnny1110/evva/internal/checkpoint"
	"github.com/johnny1110/evva/pkg/llm"
)

// rewindAgent builds a bare agent wired to a checkpoint manager rooted at a
// temp workdir. a.workdir stays "" so rewindConversation's persistSession is a
// no-op (no real-home session files written during the test).
func rewindAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	tmp := t.TempDir()
	a := newTestAgent(nil)
	a.checkpoints = checkpoint.NewManager(tmp, a.ID, checkpoint.Retention{}, slog.Default())
	if a.checkpoints == nil {
		t.Fatal("expected a manager")
	}
	return a, tmp
}

func TestRestoreCheckpoint_ChatTruncates(t *testing.T) {
	a, _ := rewindAgent(t)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "one"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ack"})

	a.checkpoints.Begin(len(a.session.GetMessages()), a.session.GetFullCompactCount(), "turn 2")
	// The turn appends more messages.
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "two"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "more"})
	a.session.Append(llm.Message{Role: llm.RoleTool})

	summary, err := a.RestoreCheckpoint("1", "chat")
	if err != nil {
		t.Fatalf("chat restore: %v", err)
	}
	if got := len(a.session.GetMessages()); got != 2 {
		t.Fatalf("history truncated to %d, want 2 (cut-point)", got)
	}
	if summary == "" {
		t.Fatal("expected a summary")
	}
}

// §5.2: once a full compaction bumps the epoch, the stored cut-point is stale
// and chat restore must be refused (code restore stays available).
func TestRestoreCheckpoint_ChatGatedAfterFullCompact(t *testing.T) {
	a, _ := rewindAgent(t)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "one"})
	a.checkpoints.Begin(1, a.session.GetFullCompactCount(), "turn") // epoch 0
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "x"})

	// Simulate a full compaction since the checkpoint.
	a.session.SetCompactState(false, 1)

	// The picker should mark chat restore unavailable.
	infos := a.Checkpoints()
	if len(infos) != 1 || infos[0].ChatRestoreOK {
		t.Fatalf("ChatRestoreOK should be false after a full compact; got %+v", infos)
	}

	// chat-only restore is refused...
	if _, err := a.RestoreCheckpoint("1", "chat"); err == nil {
		t.Fatal("chat restore should be refused across a compaction boundary")
	}
	// ...but "both" still applies code and merely skips the conversation rewind.
	summary, err := a.RestoreCheckpoint("1", "both")
	if err != nil {
		t.Fatalf("both restore should not error (code still applies): %v", err)
	}
	if summary == "" {
		t.Fatal("expected a summary noting the skipped conversation rewind")
	}
}

func TestRestoreCheckpoint_CodeThroughController(t *testing.T) {
	a, tmp := rewindAgent(t)
	f := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(f, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.checkpoints.Begin(0, 0, "edit")
	a.checkpoints.CaptureBefore(f)
	_ = os.WriteFile(f, []byte("after\n"), 0o644) // the turn's edit

	if _, err := a.RestoreCheckpoint("1", "code"); err != nil {
		t.Fatalf("code restore: %v", err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "before\n" {
		t.Fatalf("file = %q, want before-image", string(got))
	}
}

func TestRestoreCheckpoint_BadInput(t *testing.T) {
	a, _ := rewindAgent(t)
	a.checkpoints.Begin(0, 0, "x")
	if _, err := a.RestoreCheckpoint("1", "sideways"); err == nil {
		t.Fatal("unknown mode should error")
	}
	if _, err := a.RestoreCheckpoint("nope", "code"); err == nil {
		t.Fatal("non-numeric id should error")
	}
	if _, err := a.RestoreCheckpoint("999", "code"); err == nil {
		t.Fatal("missing checkpoint should error")
	}
}

func TestCheckpointsNilWhenDisabled(t *testing.T) {
	a := newTestAgent(nil) // no checkpoint manager wired
	if a.Checkpoints() != nil {
		t.Fatal("Checkpoints should be nil when checkpointing is disabled")
	}
	if _, err := a.RestoreCheckpoint("1", "code"); err == nil {
		t.Fatal("RestoreCheckpoint should error when checkpointing is disabled")
	}
}
