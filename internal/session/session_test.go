package session

import (
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

// Phase 1 analysis — Session surface:
//   - New() returns a zero-valued *Session
//   - Append / GetMessages — slice append; messages survive in order
//   - AddUsage — folds u into cumulative, leaves lastTurnInputTokens alone
//   - RecordTurn — folds u into cumulative AND sets lastTurnInputTokens
//   - LastTurnInputTokens — reads the field
//   - MicroCompact(msgs) — replaces Messages, flips microCompacted=true
//   - IsMicroCompacted — reads the flag
//   - FullCompact(msgs) — replaces Messages, resets microCompacted, increments fullCompactCount, clears lastTurnInputTokens
//   - GetFullCompactCount — reads the counter

func TestNew_ReturnsZeroValuedSession(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if len(s.Messages) != 0 {
		t.Errorf("Messages: got %d, want 0", len(s.Messages))
	}
	if s.Usage.Total() != 0 {
		t.Errorf("Usage.Total(): got %d, want 0", s.Usage.Total())
	}
	if s.IsMicroCompacted() {
		t.Error("IsMicroCompacted: got true, want false on fresh session")
	}
	if s.LastTurnInputTokens() != 0 {
		t.Errorf("LastTurnInputTokens: got %d, want 0", s.LastTurnInputTokens())
	}
	if s.GetFullCompactCount() != 0 {
		t.Errorf("GetFullCompactCount: got %d, want 0", s.GetFullCompactCount())
	}
}

func TestAppendAndGetMessages_PreservesOrder(t *testing.T) {
	s := New()
	s.Append(llm.Message{Role: llm.RoleUser, Content: "one"})
	s.Append(llm.Message{Role: llm.RoleAssistant, Content: "two"})
	s.Append(llm.Message{Role: llm.RoleTool})

	got := s.GetMessages()
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3", len(got))
	}
	if got[0].Content != "one" || got[1].Content != "two" {
		t.Errorf("order drifted: %v", got)
	}
	if got[2].Role != llm.RoleTool {
		t.Errorf("third role: got %q, want %q", got[2].Role, llm.RoleTool)
	}
}

func TestGetMessages_ReturnsLiveSlice(t *testing.T) {
	// GetMessages currently returns the underlying slice (not a copy).
	// Lock that behavior so a refactor to "return a copy" surfaces as a
	// deliberate decision, not an accident.
	s := New()
	s.Append(llm.Message{Role: llm.RoleUser, Content: "x"})
	got := s.GetMessages()
	if &got[0] != &s.Messages[0] {
		t.Error("GetMessages returned a copy; current contract is a live view")
	}
}

func TestAddUsage_FoldsIntoCumulativeOnly(t *testing.T) {
	s := New()
	s.AddUsage(llm.Usage{InputTokens: 100, OutputTokens: 50})
	s.AddUsage(llm.Usage{InputTokens: 30, CacheReadTokens: 10})

	if got, want := s.Usage.InputTokens, 130; got != want {
		t.Errorf("InputTokens: got %d, want %d", got, want)
	}
	if got, want := s.Usage.OutputTokens, 50; got != want {
		t.Errorf("OutputTokens: got %d, want %d", got, want)
	}
	if got, want := s.Usage.CacheReadTokens, 10; got != want {
		t.Errorf("CacheReadTokens: got %d, want %d", got, want)
	}
	// AddUsage must NOT touch lastTurnInputTokens — that's RecordTurn's job.
	if got := s.LastTurnInputTokens(); got != 0 {
		t.Errorf("lastTurnInputTokens leaked via AddUsage: got %d, want 0", got)
	}
}

func TestRecordTurn_UpdatesBothCumulativeAndLastTurn(t *testing.T) {
	s := New()
	s.RecordTurn(llm.Usage{InputTokens: 500, OutputTokens: 200})

	if got, want := s.Usage.InputTokens, 500; got != want {
		t.Errorf("cumulative InputTokens: got %d, want %d", got, want)
	}
	if got, want := s.LastTurnInputTokens(), 500; got != want {
		t.Errorf("LastTurnInputTokens: got %d, want %d", got, want)
	}

	// Next turn — last should track current, not accumulate.
	s.RecordTurn(llm.Usage{InputTokens: 50, OutputTokens: 10})
	if got, want := s.Usage.InputTokens, 550; got != want {
		t.Errorf("cumulative after 2nd turn: got %d, want %d", got, want)
	}
	if got, want := s.LastTurnInputTokens(), 50; got != want {
		t.Errorf("LastTurnInputTokens after 2nd turn: got %d, want %d (should be CURRENT, not accumulated)", got, want)
	}
}

func TestMicroCompact_ReplacesMessagesAndFlipsFlag(t *testing.T) {
	s := New()
	s.Append(llm.Message{Role: llm.RoleUser, Content: "before"})

	replacement := []llm.Message{
		{Role: llm.RoleUser, Content: "after-1"},
		{Role: llm.RoleUser, Content: "after-2"},
	}
	s.MicroCompact(replacement)

	if !s.IsMicroCompacted() {
		t.Error("IsMicroCompacted: got false after MicroCompact")
	}
	got := s.GetMessages()
	if len(got) != 2 || got[0].Content != "after-1" || got[1].Content != "after-2" {
		t.Errorf("Messages after MicroCompact: got %v", got)
	}
}

func TestMicroCompact_AcceptsEmptySlice(t *testing.T) {
	// Defensive: a caller that builds an empty replacement (everything
	// was elidable, somehow) shouldn't panic the session.
	s := New()
	s.Append(llm.Message{Role: llm.RoleUser, Content: "x"})
	s.MicroCompact([]llm.Message{})
	if len(s.GetMessages()) != 0 {
		t.Errorf("expected empty messages, got %d", len(s.GetMessages()))
	}
	if !s.IsMicroCompacted() {
		t.Error("IsMicroCompacted should still flip on empty replacement")
	}
}

func TestFullCompact_ResetsMicroFlagAndSetsLastTurnToBrief(t *testing.T) {
	s := New()
	s.RecordTurn(llm.Usage{InputTokens: 1234})
	s.MicroCompact([]llm.Message{{Role: llm.RoleUser, Content: "mid"}})
	if !s.IsMicroCompacted() {
		t.Fatal("precondition: MicroCompact should have flipped flag")
	}

	brief := []llm.Message{{Role: llm.RoleUser, Content: "[BRIEF]"}}
	s.FullCompact(brief, 250)

	if s.IsMicroCompacted() {
		t.Error("FullCompact should reset microCompacted to false")
	}
	if got, want := s.GetFullCompactCount(), 1; got != want {
		t.Errorf("GetFullCompactCount: got %d, want %d", got, want)
	}
	// LastTurnInputTokens is set to the brief size so the next
	// compact() call reads a realistic post-compact prompt and the
	// TUI context bar settles at the brief % rather than 0.
	if got := s.LastTurnInputTokens(); got != 250 {
		t.Errorf("LastTurnInputTokens after FullCompact: got %d, want 250 (post-compact estimate from brief size)", got)
	}
	if got := s.GetMessages(); len(got) != 1 || got[0].Content != "[BRIEF]" {
		t.Errorf("Messages after FullCompact: got %v", got)
	}
}

func TestFullCompact_ResetsCumulativeUsageToBrief(t *testing.T) {
	// FullCompact resets cumulative Usage to reflect the post-compact
	// context so the HUD reads as "fresh start". Pre-compact totals are
	// preserved in the structured log by the caller (compact.full
	// pre_compact_in / pre_compact_out fields).
	s := New()
	s.AddUsage(llm.Usage{InputTokens: 999, OutputTokens: 111})

	s.FullCompact([]llm.Message{{Role: llm.RoleUser, Content: "x"}}, 42)

	if got, want := s.Usage.InputTokens, 42; got != want {
		t.Errorf("cumulative InputTokens after FullCompact: got %d, want %d (brief size)", got, want)
	}
	if got, want := s.Usage.OutputTokens, 0; got != want {
		t.Errorf("cumulative OutputTokens after FullCompact: got %d, want %d (fresh context)", got, want)
	}
}

func TestFullCompact_CountIncrementsAcrossCalls(t *testing.T) {
	s := New()
	for i := 0; i < 3; i++ {
		s.FullCompact([]llm.Message{{Role: llm.RoleUser, Content: "x"}}, 0)
	}
	if got, want := s.GetFullCompactCount(), 3; got != want {
		t.Errorf("GetFullCompactCount: got %d, want %d", got, want)
	}
}
