package bubbletea

import (
	"strings"
	"testing"
)

// TestWrapForWidthPreservesIndent locks down the fix for the
// "pasted code loses leading spaces after wrap" regression:
// muesli/reflow's wrap drops whitespace after a forced line break by
// default, which makes indented code paste look truncated to the user.
// wrapForWidth has to opt into PreserveSpace so indentation survives.
func TestWrapForWidthPreservesIndent(t *testing.T) {
	// 60-col line of indented code that needs a forced break at 40.
	input := "    " + strings.Repeat("abcdefghij", 6)
	out := wrapForWidth(input, 40)

	// Every input rune must appear in the output (modulo any newlines
	// the wrapper introduces). The leading 4 spaces are the smoking
	// gun — they must survive the wrap.
	inRunes := []rune(strings.ReplaceAll(input, "\n", ""))
	outRunes := []rune(strings.ReplaceAll(out, "\n", ""))
	if string(inRunes) != string(outRunes) {
		t.Fatalf("content not preserved through wrap\n want=%q\n  got=%q", input, out)
	}
	if !strings.HasPrefix(out, "    ") {
		t.Fatalf("leading indent dropped\n got=%q", out)
	}
}

// TestWrapForWidthPreservesNewlines verifies a multi-line paste keeps
// every original newline through both wrap passes.
func TestWrapForWidthPreservesNewlines(t *testing.T) {
	input := "line one\nline two\nline three"
	out := wrapForWidth(input, 80)
	if !strings.Contains(out, "line one") ||
		!strings.Contains(out, "line two") ||
		!strings.Contains(out, "line three") {
		t.Fatalf("paste lines lost\n in=%q\nout=%q", input, out)
	}
	if strings.Count(out, "\n") < 2 {
		t.Fatalf("newlines collapsed: %q", out)
	}
}

// TestToolResultFoldsLongBody locks down the "long tool result is
// folded by default, Ctrl+O expands" behavior. A 30-line payload
// should render as 3 preview lines + a "+27 more lines" marker; after
// expandTools flips, the full body must be present.
func TestToolResultFoldsLongBody(t *testing.T) {
	tr := transcript{
		width:               80,
		textInflightIdx:     -1,
		thinkingInflightIdx: -1,
		bannerIdx:           -1,
	}
	// Build a tool block manually with a 30-line styled result.
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	body := strings.Join(lines, "\n")
	tr.blocks = []transcriptBlock{{
		kind:            blockTool,
		content:         "◢ bash({...})",
		toolID:          "tool_1",
		toolResult:      body,
		toolResultLines: 30,
	}}

	folded := tr.String()
	if !strings.Contains(folded, "+27 more lines") {
		t.Fatalf("expected fold marker '+27 more lines', got:\n%s", folded)
	}
	if strings.Contains(folded, "line "+string(rune('a'+25%26))) {
		// We don't want any line beyond the preview to show through.
		// Lines 0..2 are the preview; line at index 25 should be hidden.
		t.Fatalf("late line leaked into folded output:\n%s", folded)
	}

	tr.expandTools = true
	expanded := tr.String()
	if strings.Contains(expanded, "more lines") {
		t.Fatalf("expanded view should drop the fold marker:\n%s", expanded)
	}
	for _, line := range lines {
		if !strings.Contains(expanded, line) {
			t.Fatalf("expanded view missing line %q:\n%s", line, expanded)
		}
	}
}

// TestToolResultDiffNeverFolds locks down that file write/edit results
// (which carry FileDiff metadata) always render in full, even when
// long. The diff IS the artifact of the call; folding it is hostile.
func TestToolResultDiffNeverFolds(t *testing.T) {
	tr := transcript{
		width:               80,
		textInflightIdx:     -1,
		thinkingInflightIdx: -1,
		bannerIdx:           -1,
	}
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "+ added line "+string(rune('a'+i%26)))
	}
	body := strings.Join(lines, "\n")
	tr.blocks = []transcriptBlock{{
		kind:            blockTool,
		content:         "◢ write_file({...})",
		toolID:          "tool_w",
		toolResult:      body,
		toolResultLines: 50,
		noFold:          true,
	}}
	out := tr.String()
	if strings.Contains(out, "more lines") {
		t.Fatalf("file write result must not fold:\n%s", out)
	}
	for _, line := range lines {
		if !strings.Contains(out, line) {
			t.Fatalf("noFold block missing line %q:\n%s", line, out)
		}
	}
}

// TestToolResultShortStaysInline keeps short results inline — folding
// 3 lines wastes a row on the marker and obscures useful output.
func TestToolResultShortStaysInline(t *testing.T) {
	tr := transcript{
		width:               80,
		textInflightIdx:     -1,
		thinkingInflightIdx: -1,
		bannerIdx:           -1,
	}
	body := "line one\nline two\nline three"
	tr.blocks = []transcriptBlock{{
		kind:            blockTool,
		content:         "◢ bash({})",
		toolID:          "t",
		toolResult:      body,
		toolResultLines: 3,
	}}
	out := tr.String()
	if strings.Contains(out, "more lines") {
		t.Fatalf("short result should not fold:\n%s", out)
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(out, line) {
			t.Fatalf("short result missing line %q:\n%s", line, out)
		}
	}
}

// TestRenderUserPromptPreservesPaste runs the full transcript path
// — user prompt with embedded paste content + chips — and checks no
// runes are lost. This is the regression the user reported: paste
// content getting "cut" in the conversation history.
func TestRenderUserPromptPreservesPaste(t *testing.T) {
	tr := transcript{width: 60, textInflightIdx: -1, thinkingInflightIdx: -1, bannerIdx: -1}

	paste := "func main() {\n    for i := 0; i < 100; i++ {\n        doSomethingExpensive(i)\n    }\n}"
	body := "here's the code I'm worried about:\n" +
		styles.PasteChip.Render("╔═ PASTE 80 chars ═╗") + "\n" +
		paste + "\n" +
		styles.PasteChip.Render("╚════════════════════╝") + "\n"
	tr.appendUserPrompt(body)

	out := tr.String()
	// Every line of the paste must still be present in the rendered
	// transcript. We check substrings (not whole-string equality)
	// because the renderer adds the scanline header + styling.
	for _, line := range strings.Split(paste, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(out, strings.TrimLeft(line, " ")) {
			t.Fatalf("paste line missing from transcript\n line=%q\n  out=\n%s", line, out)
		}
	}
	// The full first indent token must survive even after wrap forces
	// breaks on long lines.
	if !strings.Contains(out, "    for i :=") {
		t.Fatalf("indented code lost leading spaces\n out=\n%s", out)
	}
}

// TestSanitizeStripsTerminalControlBytes locks in the fix for the
// "TUI looks frozen after task creation" bug: a pasted prompt
// contained an embedded `\r`, and every redraw replayed that CR so the
// terminal cursor kept jumping back to column 0 and overwriting cells.
// The Update goroutine was alive — the screen just looked stuck.
// Sanitizing at the transcript ingest points keeps the renderer
// terminal-safe.
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
			got := sanitizeForTranscript(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeForTranscript(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestUserPromptDoesNotEmitCR asserts that appendUserPrompt scrubs
// embedded CR bytes — the original repro path for the "frozen TUI"
// report. Without sanitization the transcript string would contain
// \r, and writing it through bubbletea's renderer would reset the
// cursor to column 0 on every redraw.
func TestUserPromptDoesNotEmitCR(t *testing.T) {
	tr := transcript{width: 60, textInflightIdx: -1, thinkingInflightIdx: -1, bannerIdx: -1}
	tr.appendUserPrompt("Test Case - 2: prompt with embedded \rcarriage return")
	out := tr.String()
	if strings.ContainsRune(out, '\r') {
		t.Fatalf("transcript still contains \\r after appendUserPrompt:\n%q", out)
	}
}
