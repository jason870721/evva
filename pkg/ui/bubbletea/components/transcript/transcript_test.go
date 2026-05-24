package transcript

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func init() {
	// Force truecolor so rendered output is deterministic across
	// host environments.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// newTestTranscript builds a transcript wired up for rendering at
// the given width — theme attached, width set so the markdown
// renderer is initialised. Most tests use width=80.
func newTestTranscript(t *testing.T, width int) *Transcript {
	t.Helper()
	tr := New()
	tr.SetTheme(theme.Default())
	tr.SetWidth(width)
	return tr
}

// ----------------------------------------------------------------------------
// Ported v1 tests — utilities
// ----------------------------------------------------------------------------

// TestWrapForWidthPreservesIndent locks down the fix for the
// "pasted code loses leading spaces after wrap" regression. (Same
// invariant as v1's transcript_test.go.)
func TestWrapForWidthPreservesIndent(t *testing.T) {
	input := "    " + strings.Repeat("abcdefghij", 6)
	out := wrapForWidth(input, 40)
	inRunes := []rune(strings.ReplaceAll(input, "\n", ""))
	outRunes := []rune(strings.ReplaceAll(out, "\n", ""))
	if string(inRunes) != string(outRunes) {
		t.Fatalf("content not preserved\n want=%q\n  got=%q", input, out)
	}
	if !strings.HasPrefix(out, "    ") {
		t.Fatalf("leading indent dropped\n got=%q", out)
	}
}

func TestWrapForWidthPreservesNewlines(t *testing.T) {
	input := "line one\nline two\nline three"
	out := wrapForWidth(input, 80)
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("paste lines lost\n in=%q\nout=%q", input, out)
		}
	}
	if strings.Count(out, "\n") < 2 {
		t.Fatalf("newlines collapsed: %q", out)
	}
}

func TestSanitizeStripsTerminalControlBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"lone CR", "python f  \rile", "python f  ile"},
		{"CRLF normalized", "line1\r\nline2", "line1\nline2"},
		{"BEL stripped", "alert\x07now", "alertnow"},
		{"FF stripped", "page\fbreak", "pagebreak"},
		{"BS stripped", "a\bbc", "abc"},
		{"preserves tab and newline", "a\tb\nc", "a\tb\nc"},
		{"preserves ANSI escape", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeForTranscript(tc.in); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUserPromptDoesNotEmitCR(t *testing.T) {
	tr := newTestTranscript(t, 60)
	tr.AppendUserPrompt("Test Case - 2: prompt with embedded \rcarriage return")
	out := tr.View()
	if strings.ContainsRune(out, '\r') {
		t.Fatalf("transcript still contains \\r after AppendUserPrompt:\n%q", out)
	}
}

func TestRenderUserPromptPreservesPaste(t *testing.T) {
	tr := newTestTranscript(t, 60)
	paste := "func main() {\n    for i := 0; i < 100; i++ {\n        doSomethingExpensive(i)\n    }\n}"
	body := "here's the code I'm worried about:\n" +
		theme.Default().PasteChip.Render("╔═ PASTE 80 chars ═╗") + "\n" +
		paste + "\n" +
		theme.Default().PasteChip.Render("╚════════════════════╝") + "\n"
	tr.AppendUserPrompt(body)

	out := tr.View()
	for _, line := range strings.Split(paste, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(out, strings.TrimLeft(line, " ")) {
			t.Fatalf("paste line missing\n line=%q\n  out=\n%s", line, out)
		}
	}
	if !strings.Contains(out, "    for i :=") {
		t.Fatalf("indented code lost leading spaces\n out=\n%s", out)
	}
}

// ----------------------------------------------------------------------------
// Ported v1 tests — fold / expand behaviour
// ----------------------------------------------------------------------------

// buildLongToolBlock manually constructs a ToolBlock with a 30-line
// styled result, used by the fold tests. We bypass the event path so
// we can control the line count precisely.
func buildLongToolBlock(t *testing.T, n int) *ToolBlock {
	t.Helper()
	tb := newToolBlock("bash", "tool_1", json.RawMessage(`{}`), false)
	var lines []string
	for i := 0; i < n; i++ {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	tb.SetResult(strings.Join(lines, "\n"), false, nil, nil)
	return tb
}

func TestToolResultFoldsLongBody(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tb := buildLongToolBlock(t, 30)
	tr.AppendBlock(tb)

	folded := tr.View()
	if !strings.Contains(folded, "+29 more lines") {
		t.Fatalf("expected fold marker '+29 more lines', got:\n%s", folded)
	}
	// The block must NOT show a line beyond the preview window.
	lateLine := "line " + string(rune('a'+25%26))
	// The preview shows lines a/b/c (indices 0,1,2). 'a' appears at
	// index 0 and again at index 26 — we can't substring-match 'a'
	// to detect leak. Pick a deeper index for the assertion.
	if strings.Contains(folded, "line "+string(rune('a'+29%26))) {
		t.Fatalf("late line leaked into folded output (looking for %q):\n%s", lateLine, folded)
	}

	tr.ToggleExpand()
	expanded := tr.View()
	if strings.Contains(expanded, "more lines") {
		t.Fatalf("expanded view should drop the fold marker:\n%s", expanded)
	}
	for i := 0; i < 30; i++ {
		want := "line " + string(rune('a'+i%26))
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded view missing line %q:\n%s", want, expanded)
		}
	}
}

func TestToolResultDiffNeverFolds(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tb := newToolBlock("write_file", "tool_w", json.RawMessage(`{}`), false)
	// Build a long FileDiff so the rendered result spans many lines.
	var lines []fs.DiffLine
	for i := 0; i < 50; i++ {
		lines = append(lines, fs.DiffLine{
			Kind: fs.LineAdd, New: i + 1, Text: "added " + string(rune('a'+i%26)),
		})
	}
	d := &fs.FileDiff{Path: "x", Op: fs.OpCreate, Hunks: []fs.DiffHunk{
		{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 50, Lines: lines},
	}}
	tb.SetResult("created x.go", false, d, nil)
	tr.AppendBlock(tb)

	out := tr.View()
	if strings.Contains(out, "more lines") {
		t.Fatalf("FileDiff result must not fold:\n%s", out)
	}
	// Spot-check several rows of the diff are present.
	for i := 0; i < 50; i += 7 {
		want := "added " + string(rune('a'+i%26))
		if !strings.Contains(out, want) {
			t.Fatalf("diff line %q missing from un-folded output", want)
		}
	}
}

func TestToolResultShortStaysInline(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tb := newToolBlock("bash", "t", json.RawMessage(`{}`), false)
	// 1-line result — trivially short, fold is a no-op.
	tb.SetResult("line one", false, nil, nil)
	tr.AppendBlock(tb)

	out := tr.View()
	if strings.Contains(out, "more lines") {
		t.Fatalf("single-line result should not fold:\n%s", out)
	}
	if !strings.Contains(out, "line one") {
		t.Fatalf("single-line result missing content:\n%s", out)
	}
}

// ----------------------------------------------------------------------------
// New M3 contract — cache invalidation
// ----------------------------------------------------------------------------

// TestCacheHitsOnUnchangedRender verifies that calling View twice
// with no mutations doesn't re-run Block.Render. We can't directly
// instrument Render without modifying the Block interface, so we
// check the cache's Size() — a second identical View call should
// not create new entries.
func TestCacheHitsOnUnchangedRender(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.AppendBlock(newTextBlock("hello world"))

	first := tr.View()
	sizeAfterFirst := tr.cacheSize()

	second := tr.View()
	sizeAfterSecond := tr.cacheSize()

	if first != second {
		t.Fatalf("identical View calls produced different output")
	}
	if sizeAfterFirst != sizeAfterSecond {
		t.Errorf("cache grew between identical renders: %d → %d",
			sizeAfterFirst, sizeAfterSecond)
	}
}

// TestCacheInvalidatesOnWidthChange — changing terminal width must
// produce a different render even with no block mutations. Verifies
// the cache key correctly includes width.
func TestCacheInvalidatesOnWidthChange(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.AppendBlock(newTextBlock("hello world wrapping target text " + strings.Repeat("x ", 20)))

	wide := tr.View()
	tr.SetWidth(40)
	narrow := tr.View()

	if wide == narrow {
		t.Errorf("expected different output at different widths, got identical")
	}
}

// TestCacheInvalidatesOnBlockRev — appending a chunk to a streaming
// block must produce a render that includes the appended content.
// This guards against the "rev not bumped" bug. We use ThinkingBlock
// (no glamour) so length is monotonic with input length — TextBlock's
// glamour pass adds variable padding that can shorten the output even
// as content grows.
func TestCacheInvalidatesOnBlockRev(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tb := newThinkingBlock("first")
	tr.AppendBlock(tb)

	short := stripANSI(tr.View())
	tb.Append(" second")
	long := stripANSI(tr.View())

	if !strings.Contains(short, "first") {
		t.Errorf("initial content missing:\n%s", short)
	}
	if !strings.Contains(long, "first second") {
		t.Errorf("appended chunk not in render:\n%s", long)
	}
	if short == long {
		t.Errorf("cache returned stale render after Append (rev not bumped?)")
	}
}

// ----------------------------------------------------------------------------
// New M3 contract — streaming + tool pairing
// ----------------------------------------------------------------------------

// TestStreamingTextCoalesces — two KindTextChunk events with the
// same in-flight scope produce ONE TextBlock, not two. The merged
// text appears in the block's PlainText. (We don't assert on the
// rendered View() because glamour styles individual tokens — the
// concatenated "hello world" string can be split across ANSI
// escape boundaries in the styled output.)
func TestStreamingTextCoalesces(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.IngestEvent(event.Event{Kind: event.KindTextChunk, Text: &event.TextPayload{Text: "hello "}})
	tr.IngestEvent(event.Event{Kind: event.KindTextChunk, Text: &event.TextPayload{Text: "world"}})

	var textBlocks []*TextBlock
	for _, b := range tr.Blocks() {
		if tb, ok := b.(*TextBlock); ok {
			textBlocks = append(textBlocks, tb)
		}
	}
	if len(textBlocks) != 1 {
		t.Fatalf("two text chunks should produce ONE TextBlock, got %d", len(textBlocks))
	}
	if got := textBlocks[0].PlainText(); got != "hello world" {
		t.Errorf("merged PlainText = %q, want %q", got, "hello world")
	}
}

// TestStreamingResetsOnNonChunk — a non-chunk event between chunks
// closes the in-flight block, so a subsequent chunk opens a fresh
// one.
func TestStreamingResetsOnNonChunk(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.IngestEvent(event.Event{Kind: event.KindTextChunk, Text: &event.TextPayload{Text: "first"}})
	tr.IngestEvent(event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{
		Name: "bash", ToolID: "t1", Input: json.RawMessage(`{}`),
	}})
	tr.IngestEvent(event.Event{Kind: event.KindTextChunk, Text: &event.TextPayload{Text: "second"}})

	textBlocks := 0
	for _, b := range tr.Blocks() {
		if b.Kind() == KindText {
			textBlocks++
		}
	}
	if textBlocks != 2 {
		t.Errorf("chunks straddling a tool event should produce 2 blocks, got %d", textBlocks)
	}
}

// TestToolPairingByID — a KindToolUseResult for a known ToolID
// attaches to the matching ToolBlock. The matched block must show
// both the head and the result.
func TestToolPairingByID(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.IngestEvent(event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{
		Name: "bash", ToolID: "tool_abc", Input: json.RawMessage(`{"cmd":"ls"}`),
	}})
	tr.IngestEvent(event.Event{Kind: event.KindToolUseResult, ToolUseResult: &event.ToolUseResultPayload{
		ToolID: "tool_abc", Content: "file1.txt\nfile2.txt", IsError: false,
	}})

	toolBlocks := 0
	for _, b := range tr.Blocks() {
		if b.Kind() == KindTool {
			toolBlocks++
		}
	}
	if toolBlocks != 1 {
		t.Fatalf("expected ONE paired tool block, got %d", toolBlocks)
	}
	out := tr.View()
	if !strings.Contains(out, "bash") {
		t.Errorf("tool head missing:\n%s", out)
	}
	if !strings.Contains(out, "file1.txt") {
		t.Errorf("tool result missing:\n%s", out)
	}
}

// TestToolResultWithoutStartAppendsStandalone — a result event for
// an unknown ToolID synthesises a bare ToolBlock so the result still
// renders (defensive against agent reordering bugs).
func TestToolResultWithoutStartAppendsStandalone(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.IngestEvent(event.Event{Kind: event.KindToolUseResult, ToolUseResult: &event.ToolUseResultPayload{
		ToolID: "orphan", Content: "lonely result", IsError: false,
	}})
	out := tr.View()
	if !strings.Contains(out, "lonely result") {
		t.Errorf("orphan result didn't render:\n%s", out)
	}
}

// ----------------------------------------------------------------------------
// PlainText contract — yank/search will read from this
// ----------------------------------------------------------------------------

// TestPlainTextStripsGutter — PlainText must NOT include the
// timeline gutter (│ ├─) even though Render does. This is the
// guarantee yank-mode (M8) and search (M9) rely on.
func TestPlainTextStripsGutter(t *testing.T) {
	tb := newTextBlock("hello")
	if got := tb.PlainText(); got != "hello" {
		t.Errorf("TextBlock.PlainText = %q, want %q", got, "hello")
	}

	tool := newToolBlock("bash", "x", json.RawMessage(`{}`), false)
	tool.SetResult("output", false, nil, nil)
	plain := tool.PlainText()
	if strings.Contains(plain, "│") || strings.Contains(plain, "├─") {
		t.Errorf("ToolBlock.PlainText contains gutter glyph: %q", plain)
	}
	if !strings.Contains(plain, "output") {
		t.Errorf("ToolBlock.PlainText missing result content: %q", plain)
	}
}

// TestThinkingSpriteStaysAtTail verifies the sprite-as-anchor
// invariant: once ShowThinkingSprite mounts the sprite, every
// subsequent block (text, tool, thinking, user prompt, synthetic)
// inserts BEFORE it so the sprite reads as sitting at the end of
// the latest output, not stranded at the top.
func TestThinkingSpriteStaysAtTail(t *testing.T) {
	tr := newTestTranscript(t, 80)
	tr.ShowThinkingSprite()

	// Initial: just the sprite.
	if got := len(tr.blocks); got != 1 {
		t.Fatalf("expected 1 block (sprite), got %d", got)
	}
	if tr.blocks[0] != tr.thinkingSprite {
		t.Fatalf("expected sprite at index 0")
	}

	// Stream a few events of different kinds; sprite must stay at tail.
	tr.IngestEvent(event.Event{Kind: event.KindText, Text: &event.TextPayload{Text: "hello"}})
	tr.IngestEvent(event.Event{
		Kind: event.KindToolUseStart,
		ToolUseStart: &event.ToolUseStartPayload{
			Name: "bash", ToolID: "t1", Input: json.RawMessage(`{"command":"ls"}`),
		},
	})
	tr.IngestEvent(event.Event{Kind: event.KindThinking, Thinking: &event.TextPayload{Text: "pondering"}})
	tr.AppendUserPrompt("another prompt")
	tr.AppendSynthetic("synthetic note")

	// Every append must land before the sprite — so the sprite is the
	// last entry in blocks.
	n := len(tr.blocks)
	if n < 2 {
		t.Fatalf("expected multiple blocks after appends, got %d", n)
	}
	if tr.blocks[n-1] != tr.thinkingSprite {
		t.Errorf("sprite must be at the tail; got index %d of %d", indexOfBlock(tr.blocks, tr.thinkingSprite), n)
	}

	// Hide and verify the sprite is gone.
	tr.HideThinkingSprite()
	if tr.HasThinkingSprite() {
		t.Errorf("HideThinkingSprite did not clear the sprite")
	}
	if tr.thinkingSprite != nil {
		t.Errorf("thinkingSprite field not cleared")
	}
	for i, b := range tr.blocks {
		if _, ok := b.(*ThinkingSpriteBlock); ok {
			t.Errorf("sprite still in blocks at index %d after hide", i)
		}
	}
}

func indexOfBlock(blocks []Block, target Block) int {
	for i, b := range blocks {
		if b == target {
			return i
		}
	}
	return -1
}

// TestStripANSI sanity — the PlainText helper strips lipgloss output.
func TestStripANSI(t *testing.T) {
	styled := theme.Default().ToolCall.Render("◢ bash({})")
	plain := stripANSI(styled)
	if strings.ContainsRune(plain, 0x1b) {
		t.Errorf("stripANSI left an ESC byte: %q", plain)
	}
	if !strings.Contains(plain, "bash") {
		t.Errorf("stripANSI removed visible content: %q", plain)
	}
}
