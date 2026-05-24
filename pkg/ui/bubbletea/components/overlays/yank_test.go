package overlays

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/transcript"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// newPopulatedTranscript returns a transcript with three blocks:
// user prompt, assistant text, tool call + result. Enough variety
// for yank-mode navigation tests.
func newPopulatedTranscript(t *testing.T) *transcript.Transcript {
	t.Helper()
	tr := transcript.New()
	tr.SetTheme(theme.Default())
	tr.SetWidth(80)
	tr.AppendUserPrompt("first prompt")
	tr.IngestEvent(event.Event{Kind: event.KindText, Text: &event.TextPayload{Text: "first reply"}})
	tr.IngestEvent(event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{
		Name: "bash", ToolID: "t1", Input: json.RawMessage(`{}`),
	}})
	tr.IngestEvent(event.Event{Kind: event.KindToolUseResult, ToolUseResult: &event.ToolUseResultPayload{
		ToolID: "t1", Content: "output line 1\noutput line 2", IsError: false,
	}})
	return tr
}

func TestNewYankNilOnEmpty(t *testing.T) {
	tr := transcript.New()
	tr.SetTheme(theme.Default())
	tr.SetWidth(80)
	if y := NewYank(tr); y != nil {
		t.Errorf("NewYank on empty transcript should return nil")
	}
	if y := NewYank(nil); y != nil {
		t.Errorf("NewYank on nil transcript should return nil")
	}
}

func TestYankStartsOnLast(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	blocks := tr.Blocks()
	if got := y.cursor; got != len(blocks)-1 {
		t.Errorf("cursor = %d, want last (%d)", got, len(blocks)-1)
	}
	if tr.FocusedBlock() != blocks[len(blocks)-1].ID() {
		t.Errorf("transcript focus not set to last block")
	}
}

func TestYankKeyAndModal(t *testing.T) {
	y := &Yank{tr: newPopulatedTranscript(t)}
	if y.Key() != "yank" {
		t.Errorf("Key = %q, want yank", y.Key())
	}
	if !y.Modal() {
		t.Errorf("Modal should be true")
	}
	if y.View(80, theme.Default()) != "" {
		t.Errorf("yank View should be empty (renders no panel)")
	}
}

func TestYankNavigation(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	blocks := tr.Blocks()
	startIdx := y.cursor

	// k → previous
	close, _ := y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if close {
		t.Fatal("k should not close")
	}
	if y.cursor != startIdx-1 {
		t.Errorf("after k: cursor = %d, want %d", y.cursor, startIdx-1)
	}

	// j → next
	y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if y.cursor != startIdx {
		t.Errorf("after j: cursor = %d, want %d", y.cursor, startIdx)
	}

	// g → first
	y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if y.cursor != 0 {
		t.Errorf("after g: cursor = %d, want 0", y.cursor)
	}
	if tr.FocusedBlock() != blocks[0].ID() {
		t.Errorf("transcript focus not updated to first")
	}

	// G → last
	y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if y.cursor != len(blocks)-1 {
		t.Errorf("after G: cursor = %d, want last", y.cursor)
	}
}

func TestYankNavigationClampsAtEdges(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	// At last block already; j should be a no-op.
	last := y.cursor
	y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if y.cursor != last {
		t.Errorf("j at last clamped wrong: cursor = %d, want %d", y.cursor, last)
	}
	// Jump to 0, k should clamp.
	y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if y.cursor != 0 {
		t.Errorf("k at first clamped wrong: cursor = %d, want 0", y.cursor)
	}
}

func TestYankEscClosesAndClearsFocus(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	close, _ := y.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !close {
		t.Errorf("Esc should close")
	}
	if tr.FocusedBlock() != 0 {
		t.Errorf("Esc should clear transcript focus, got %d", tr.FocusedBlock())
	}
}

func TestYankQClosesAndClearsFocus(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	close, _ := y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !close {
		t.Errorf("q should close")
	}
	if tr.FocusedBlock() != 0 {
		t.Errorf("q should clear transcript focus")
	}
}

func TestYankEnterCopiesViaCmd(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	// Focus is on the last block (tool result with content).
	close, cmd := y.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if close {
		t.Errorf("Enter should NOT close — yank mode stays open for multi-copy")
	}
	if cmd == nil {
		t.Fatal("Enter should return a clipboard cmd")
	}
	msg, _ := cmd().(events.ClipboardMsg)
	if !msg.OK {
		t.Errorf("clipboard write failed: %v", msg.Err)
	}
	if msg.Size <= 0 {
		t.Errorf("clipboard wrote zero bytes")
	}
}

func TestYankEnterOnEmptyBlockHintsAndStays(t *testing.T) {
	// Construct a transcript whose only block has empty PlainText.
	tr := transcript.New()
	tr.SetTheme(theme.Default())
	tr.SetWidth(80)
	tr.AppendUserPrompt("")
	y := NewYank(tr)
	close, cmd := y.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if close {
		t.Errorf("Enter on empty block should not close")
	}
	if cmd != nil {
		t.Errorf("Enter on empty block should NOT emit a clipboard cmd")
	}
	if y.last != "empty" {
		t.Errorf("status should report 'empty', got %q", y.last)
	}
}

func TestYankCursorChangedMsg(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	_, cmd := y.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if cmd == nil {
		t.Fatal("k should emit a cursor-changed cmd")
	}
	msg, ok := cmd().(YankCursorChangedMsg)
	if !ok {
		t.Fatalf("expected YankCursorChangedMsg, got %T", cmd())
	}
	if msg.BlockID == 0 {
		t.Errorf("YankCursorChangedMsg.BlockID should be non-zero")
	}
}

func TestYankHintIncludesPosition(t *testing.T) {
	tr := newPopulatedTranscript(t)
	y := NewYank(tr)
	hint := y.Hint()
	if hint == "" {
		t.Fatal("Hint should be non-empty")
	}
	// Expect something like "yank 4/4 · ..."
	if !contains(hint, "yank ") {
		t.Errorf("Hint missing 'yank N/M' prefix: %q", hint)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
