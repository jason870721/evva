package input

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// newTestInput builds an Input wired to the default theme.
func newTestInput(t *testing.T) *Input {
	t.Helper()
	in := New(theme.Default())
	in.SetWidth(80)
	return in
}

// ----------------------------------------------------------------------------
// Paste handling
// ----------------------------------------------------------------------------

func TestPasteCompactsMultiLine(t *testing.T) {
	in := newTestInput(t)
	pasted := "line one\nline two\nline three"
	in.handlePaste(pasted)

	got := in.Value()
	if !strings.Contains(got, "paste total") {
		t.Errorf("multi-line paste not compacted, got: %q", got)
	}
	if strings.Contains(got, "line one") {
		t.Errorf("multi-line paste content leaked into input box: %q", got)
	}
	if len(in.pasted) != 1 || in.pasted[0] != pasted {
		t.Errorf("pasted buffer not populated: %+v", in.pasted)
	}
}

func TestPasteShortInline(t *testing.T) {
	in := newTestInput(t)
	in.handlePaste("hello")

	if got := in.Value(); got != "hello" {
		t.Errorf("short paste not inlined: got %q want %q", got, "hello")
	}
	if len(in.pasted) != 0 {
		t.Errorf("short paste should not populate buffer, got: %+v", in.pasted)
	}
}

func TestPasteLargeSingleLineCompacts(t *testing.T) {
	in := newTestInput(t)
	large := strings.Repeat("x", pasteCompactThreshold+1)
	in.handlePaste(large)

	if !strings.Contains(in.Value(), "paste total") {
		t.Errorf("large single-line paste not compacted, got: %q", in.Value())
	}
}

// ----------------------------------------------------------------------------
// Paste expansion
// ----------------------------------------------------------------------------

func TestExpandForAgentReplacesPlaceholders(t *testing.T) {
	pasted := []string{"line one\nline two"}
	text := "Here's my code: " + formatPlaceholder(len(pasted[0])) + " — what do you think?"
	got := expandForAgent(text, pasted)
	if !strings.Contains(got, "line one\nline two") {
		t.Errorf("agent expansion didn't inline paste content: %q", got)
	}
	if strings.Contains(got, "paste total") {
		t.Errorf("agent expansion left placeholder marker: %q", got)
	}
}

func TestExpandForViewWrapsInChips(t *testing.T) {
	pasted := []string{"func main() {}"}
	text := "look: " + formatPlaceholder(len(pasted[0]))
	got := expandForView(text, pasted, theme.Default())
	if !strings.Contains(got, "PASTE") {
		t.Errorf("view expansion missing chip header: %q", got)
	}
	if !strings.Contains(got, "func main()") {
		t.Errorf("view expansion didn't include paste content: %q", got)
	}
}

// TestExpandForAgentSurvivesExtraPlaceholders — a stray
// placeholder in the input (e.g. user typed "[- paste total 3 characters -]"
// literally) past the buffer length must NOT crash; the literal stays.
func TestExpandForAgentSurvivesExtraPlaceholders(t *testing.T) {
	pasted := []string{"real"}
	text := formatPlaceholder(len(pasted[0])) + " " + formatPlaceholder(99)
	got := expandForAgent(text, pasted)
	if !strings.Contains(got, "real") {
		t.Errorf("first placeholder not expanded: %q", got)
	}
	if !strings.Contains(got, formatPlaceholder(99)) {
		t.Errorf("excess placeholder should remain literal: %q", got)
	}
}

// ----------------------------------------------------------------------------
// History navigation
// ----------------------------------------------------------------------------

func TestHistoryAppendDedupes(t *testing.T) {
	in := newTestInput(t)
	in.appendHistory("hello")
	in.appendHistory("hello") // dup of latest — should be dropped
	in.appendHistory("world")
	if got := in.historyLen(); got != 2 {
		t.Errorf("history length = %d, want 2 (dedup)", got)
	}
}

func TestHistoryPrevWalksBackward(t *testing.T) {
	in := newTestInput(t)
	in.appendHistory("first")
	in.appendHistory("second")
	in.appendHistory("third")

	// First Up → newest entry.
	if !in.historyPrev() {
		t.Fatal("historyPrev should engage on first Up with empty input")
	}
	if got := in.Value(); got != "third" {
		t.Errorf("first Up = %q, want %q", got, "third")
	}
	// Second Up → next-newest.
	in.historyPrev()
	if got := in.Value(); got != "second" {
		t.Errorf("second Up = %q, want %q", got, "second")
	}
	// Third Up → oldest.
	in.historyPrev()
	if got := in.Value(); got != "first" {
		t.Errorf("third Up = %q, want %q", got, "first")
	}
	// Fourth Up — clamps at oldest.
	in.historyPrev()
	if got := in.Value(); got != "first" {
		t.Errorf("fourth Up should stay at oldest, got %q", got)
	}
}

func TestHistoryNextRestoresDraft(t *testing.T) {
	in := newTestInput(t)
	in.appendHistory("first")
	in.appendHistory("second")

	in.SetValue("draft in progress")
	if got := in.Value(); got != "draft in progress" {
		t.Fatalf("SetValue smoke check failed: %q", got)
	}

	// historyPrev should refuse when the input has typed (non-empty) content.
	if in.historyPrev() {
		t.Errorf("historyPrev should not engage when typed draft is present")
	}

	// Clear and engage nav.
	in.SetValue("")
	in.historyPrev() // -> "second"
	in.historyPrev() // -> "first"
	in.historyNext() // -> "second"
	in.historyNext() // past newest -> restore draft (empty here)
	if got := in.Value(); got != "" {
		t.Errorf("past-newest history should restore empty draft, got %q", got)
	}
}

// NB on history "draft" preservation: v1's logic only engages
// history nav when the input is empty / whitespace-only — see
// historyPrev. That means the saved draft is always "" in practice;
// the "draft" name is historical, the real behaviour is "Down past
// newest leaves the input empty". v2 ports the same semantic, so
// there's no separate test for a non-empty draft round-trip.

// ----------------------------------------------------------------------------
// Submit flow
// ----------------------------------------------------------------------------

// TestSubmitEmitsCmdWithPasteExpansion — Enter on non-empty content
// emits a SubmitMsg via the returned tea.Cmd. The ForAgent payload
// contains the raw paste; ForView contains the chip-wrapped form.
func TestSubmitEmitsCmdWithPasteExpansion(t *testing.T) {
	in := newTestInput(t)
	in.handlePaste("paste content\nspread across lines")
	in.ta.InsertString(" — explain please")

	cmd := in.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on non-empty input should emit a tea.Cmd")
	}
	msg := cmd()
	sub, ok := msg.(SubmitMsg)
	if !ok {
		t.Fatalf("expected SubmitMsg, got %T: %+v", msg, msg)
	}
	if !strings.Contains(sub.ForAgent, "paste content\nspread across lines") {
		t.Errorf("ForAgent missing raw paste: %q", sub.ForAgent)
	}
	if !strings.Contains(sub.ForAgent, "explain please") {
		t.Errorf("ForAgent missing typed text: %q", sub.ForAgent)
	}
	if !strings.Contains(sub.ForView, "PASTE") {
		t.Errorf("ForView missing chip header: %q", sub.ForView)
	}
	if !strings.Contains(sub.ForView, "explain please") {
		t.Errorf("ForView missing typed text: %q", sub.ForView)
	}
}

// TestSubmitEmptyEmitsEmpty — Enter on empty input still emits a
// SubmitMsg (the App decides what an empty submit means; e.g.
// iter-limit continue).
func TestSubmitEmptyEmitsEmpty(t *testing.T) {
	in := newTestInput(t)
	cmd := in.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should always emit a cmd, even on empty input")
	}
	sub, ok := cmd().(SubmitMsg)
	if !ok {
		t.Fatalf("expected SubmitMsg, got %T", cmd())
	}
	if sub.ForAgent != "" || sub.ForView != "" {
		t.Errorf("empty submit should produce empty payload, got %+v", sub)
	}
}

// TestCtrlJInsertsNewline — Ctrl+J during composition adds a literal
// newline instead of submitting.
func TestCtrlJInsertsNewline(t *testing.T) {
	in := newTestInput(t)
	in.ta.InsertString("line 1")
	cmd := in.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if cmd != nil {
		// May return cmd for cursor blink etc., that's fine. Just
		// assert no SubmitMsg.
		if msg := cmd(); msg != nil {
			if _, isSubmit := msg.(SubmitMsg); isSubmit {
				t.Fatal("Ctrl+J must not submit")
			}
		}
	}
	if got := in.Value(); !strings.Contains(got, "line 1\n") {
		t.Errorf("Ctrl+J should insert newline, got %q", got)
	}
}

// TestResetClearsPastedBuffer — Reset wipes input + paste buffer so
// a stale paste doesn't haunt the next prompt.
func TestResetClearsPastedBuffer(t *testing.T) {
	in := newTestInput(t)
	in.handlePaste("multi\nline\npaste")
	in.Reset()
	if in.Value() != "" {
		t.Errorf("Reset should clear value, got %q", in.Value())
	}
	if len(in.pasted) != 0 {
		t.Errorf("Reset should clear paste buffer, got %d entries", len(in.pasted))
	}
}

// TestPasteThenSubmitMatchesV1 — end-to-end shape: paste + submit,
// ForAgent shouldn't contain any placeholder, ForView wraps chips.
func TestPasteThenSubmitMatchesV1(t *testing.T) {
	in := newTestInput(t)
	in.handlePaste("def foo():\n    return 42")
	cmd := in.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sub := cmd().(SubmitMsg)

	if strings.Contains(sub.ForAgent, "paste total") {
		t.Errorf("ForAgent should not contain placeholder, got %q", sub.ForAgent)
	}
	if !strings.Contains(sub.ForAgent, "def foo()") {
		t.Errorf("ForAgent should contain expanded paste, got %q", sub.ForAgent)
	}
	if !strings.Contains(sub.ForView, "PASTE") {
		t.Errorf("ForView should contain chip header, got %q", sub.ForView)
	}
}

// TestFormatPlaceholderMatchesRegex — sanity check that the
// placeholder we produce is matched by the regex used for expansion.
func TestFormatPlaceholderMatchesRegex(t *testing.T) {
	for _, size := range []int{0, 1, 99, 100000} {
		ph := formatPlaceholder(size)
		if !pastePlaceholderRe.MatchString(ph) {
			t.Errorf("regex should match placeholder %q", ph)
		}
	}
}

// Silence unused-import warning if all tests trim — fmt and tea are
// real imports used above.
var _ = fmt.Sprintf
var _ tea.Cmd
