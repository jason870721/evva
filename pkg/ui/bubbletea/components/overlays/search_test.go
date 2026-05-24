package overlays

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/transcript"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// newSearchableTranscript builds a transcript with content the
// search tests look for.
func newSearchableTranscript(t *testing.T) *transcript.Transcript {
	t.Helper()
	tr := transcript.New()
	tr.SetTheme(theme.Default())
	tr.SetWidth(80)
	tr.AppendUserPrompt("how do I write a docker compose file?")
	tr.IngestEvent(event.Event{Kind: event.KindText, Text: &event.TextPayload{
		Text: "Docker Compose is a YAML-based orchestrator.",
	}})
	tr.IngestEvent(event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{
		Name: "bash", ToolID: "t1", Input: json.RawMessage(`{}`),
	}})
	tr.IngestEvent(event.Event{Kind: event.KindToolUseResult, ToolUseResult: &event.ToolUseResultPayload{
		ToolID: "t1", Content: "docker compose version v2.31", IsError: false,
	}})
	return tr
}

func TestNewSearchNil(t *testing.T) {
	if s := NewSearch(nil); s != nil {
		t.Errorf("NewSearch(nil) should return nil")
	}
}

func TestSearchKeyAndModal(t *testing.T) {
	s := &Search{}
	if s.Key() != "search" {
		t.Errorf("Key = %q, want search", s.Key())
	}
	if !s.Modal() {
		t.Errorf("Modal should be true")
	}
}

func TestSearchEmptyQueryHint(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	if h := s.Hint(); !contains(h, "search") {
		t.Errorf("empty-query hint should mention search, got %q", h)
	}
}

func TestSearchFindsAllOccurrences(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("docker")
	s.rescan()
	if len(s.matches) == 0 {
		t.Fatal("expected matches for 'docker' across multiple blocks")
	}
	// Each matched block id maps to ≥1 Range; the transcript-side
	// matches map should be populated.
	if mb := tr.MatchedBlocks(); len(mb) != len(s.matches) {
		t.Errorf("transcript MatchedBlocks = %d, search.matches = %d", len(mb), len(s.matches))
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("DOCKER")
	s.rescan()
	if len(s.matches) == 0 {
		t.Errorf("case-insensitive 'DOCKER' should still match")
	}
}

func TestSearchEmptyQueryClears(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("docker")
	s.rescan()
	if len(s.matches) == 0 {
		t.Fatal("setup: expected matches before clear")
	}
	s.input.SetValue("")
	s.rescan()
	if len(s.matches) != 0 {
		t.Errorf("empty query should clear matches, got %d", len(s.matches))
	}
	if tr.MatchedBlocks() != nil {
		t.Errorf("transcript matches should clear too")
	}
}

func TestSearchAdvanceWraps(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("docker")
	s.rescan()
	start := s.cursor
	// Step forward through all matches; should wrap back to start.
	for range s.matches {
		s.advance(+1)
	}
	if s.cursor != start {
		t.Errorf("after full forward cycle: cursor = %d, want %d", s.cursor, start)
	}
}

func TestSearchAdvanceBackward(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("docker")
	s.rescan()
	first := s.cursor
	s.advance(-1)
	// Backward from 0 wraps to last.
	if s.cursor != len(s.matches)-1 {
		t.Errorf("backward from %d wrapped to %d, want %d", first, s.cursor, len(s.matches)-1)
	}
}

func TestSearchEscClearsHighlight(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("docker")
	s.rescan()
	close, _ := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !close {
		t.Errorf("Esc should close")
	}
	if tr.MatchedBlocks() != nil {
		t.Errorf("Esc should clear the transcript's match map")
	}
}

func TestSearchEnterAdvances(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("docker")
	s.rescan()
	if len(s.matches) < 2 {
		t.Skip("need ≥2 matches to test advance")
	}
	start := s.cursor
	close, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if close {
		t.Errorf("Enter should not close")
	}
	if cmd == nil {
		t.Errorf("Enter should return a reveal cmd")
	}
	if s.cursor == start {
		t.Errorf("Enter should advance cursor")
	}
	msg, ok := cmd().(SearchRevealMsg)
	if !ok {
		t.Fatalf("expected SearchRevealMsg, got %T", cmd())
	}
	if msg.BlockID == 0 {
		t.Errorf("SearchRevealMsg.BlockID should be non-zero")
	}
}

func TestSearchNoMatchesHint(t *testing.T) {
	tr := newSearchableTranscript(t)
	s := NewSearch(tr)
	s.input.SetValue("doesnotappearanywhere")
	s.rescan()
	if h := s.Hint(); !contains(h, "no matches") {
		t.Errorf("hint should report 'no matches' for nonexistent query, got %q", h)
	}
}

func TestFindAllRangesNonOverlapping(t *testing.T) {
	ranges := findAllRanges("ababab", "aba")
	// Non-overlapping: matches at 0 and (skipping 2) ... only 0.
	// Actually "aba" at 0 → next start at 3, "bab" doesn't match.
	// So 1 match.
	if len(ranges) != 1 {
		t.Errorf("expected 1 non-overlapping match in 'ababab' for 'aba', got %d", len(ranges))
	}
}

func TestFindAllRangesEmpty(t *testing.T) {
	if r := findAllRanges("hello", ""); r != nil {
		t.Errorf("empty query should return nil ranges")
	}
}
