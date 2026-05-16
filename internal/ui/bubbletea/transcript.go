package bubbletea

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"

	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/fs"
)

// blockKind tags a transcript entry so String() knows how to draw the
// timeline gutter — assistant text and tool blocks live on the timeline,
// user prompts cut it, the banner sits outside it.
type blockKind int

const (
	blockBanner       blockKind = iota
	blockUserPrompt             // cuts the timeline
	blockText                   // assistant text / markdown
	blockThinking               // dim reasoning text
	blockTool                   // tool_use_start, possibly with result attached
	blockSystem                 // compacting, draining, etc.
	blockError                  // KindError red banner
	blockSynthetic              // pre-formatted block injected by the UI
)

// transcriptBlock is one logical entry in the scrollback. content is the
// already-styled string; the kind decides the gutter prefix; toolID is
// only used for blockTool so the result event can find its start.
//
// For blockTool, content holds only the head line (the `◢ name(args)`
// invocation). The result body is stored separately on toolResult so
// the renderer can decide between a folded preview and the full payload
// based on the transcript-wide expandTools flag — see foldedToolBody.
type transcriptBlock struct {
	kind    blockKind
	content string
	toolID  string

	// toolResult is the styled multi-line result body (including diff
	// hunks if any) for blockTool entries. Empty until the matching
	// KindToolUseResult event lands. Kept separate from content so
	// the fold preview can re-render without re-styling.
	toolResult string
	// toolResultLines is the rendered line count of toolResult — used
	// to decide whether to fold and how many "more lines" the preview
	// marker should report.
	toolResultLines int
	// noFold opts the block out of the length-triggered fold pass.
	// Set on tool results that the user always wants to see in full
	// — currently file write / edit (their FileDiff metadata is the
	// load-bearing artifact of the call; folding it defeats the
	// purpose of the call having happened).
	noFold bool
	// hideResult suppresses the result body entirely in favor of a
	// terse one-line summary. Set on tool_search calls so the loaded
	// schemas don't pollute the user-facing transcript.
	hideResult bool
}

// transcript accumulates the scrollback the user reads in the viewport.
//
// Three behaviors above the obvious append:
//
//   - Streaming chunk coalescing: KindTextChunk / KindThinkingChunk
//     append into a single in-flight block via textInflightIdx /
//     thinkingInflightIdx. Any non-chunk event resets the markers.
//
//   - Tool pairing: KindToolUseStart appends a block keyed by ToolID;
//     KindToolUseResult finds the same block and folds the result line
//     into it so each call renders as one "use → result" unit even when
//     dispatched in parallel.
//
//   - Timeline rendering: String() draws a git-style gutter down the
//     left side. User prompts cut the line (separator + bullet); tool
//     blocks branch with ├─; everything else flows along │.
type transcript struct {
	blocks              []transcriptBlock
	textInflightIdx     int
	thinkingInflightIdx int
	rawText             string
	rawThinking         string
	// toolBlocks maps ToolID → block index so KindToolUseResult can
	// attach the result line to the matching KindToolUseStart block.
	toolBlocks map[string]int
	// md renders assistant content blocks (KindText / KindTextChunk)
	// as markdown. Width is re-set on every viewport resize so wrapping
	// tracks the layout.
	md *markdownRenderer
	// width is the column count String() uses to size the user-prompt
	// separator and any other width-aware glyphs. Updated by setWidth.
	width int
	// banner holds the raw inputs for the welcome block. Stored
	// separately from blocks so the rendered representation can be
	// recomputed (recentered, restyled) on every resize without
	// losing the source data.
	banner bannerSpec
	// bannerIdx is the index of the rendered banner block inside
	// blocks, or -1 when no banner is active. Used by reflowBanner
	// to swap the styled string on resize / metadata change.
	bannerIdx int
	// expandTools, when true, suppresses the fold-preview pass so
	// every tool result renders in full. Toggled by Ctrl+O in the
	// app.go key handler.
	expandTools bool
}

// sanitizeForTranscript scrubs control bytes that would corrupt the
// terminal when written through to the renderer. The dangerous ones are
// `\r` (cursor → column 0, overwriting prior cells), `\f` (form feed,
// some terminals clear screen), `\b` (backspace), and `\x07` (BEL).
//
// Embedded `\r` was the root cause of the "TUI frozen after tasks
// created" report: a pasted user prompt contained `python f  \rile`,
// and every subsequent redraw replayed that `\r` and clobbered the
// visible row. The Update goroutine was fine — the screen just looked
// stuck.
//
// We preserve `\n`, `\t`, and ESC (0x1b — the leader for ANSI/CSI
// escapes that lipgloss emits for styling).
func sanitizeForTranscript(s string) string {
	if !strings.ContainsAny(s, "\r\b\f\x07") {
		return s
	}
	// Normalize CRLF → LF first so we don't lose a legitimate newline
	// when stripping the leading \r.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\r', '\b', '\f', '\x07':
			// Drop — these wreck terminal layout from inside a
			// scrollback buffer the renderer can't escape from.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldToolThreshold and foldToolPreviewLines control when and how a
// long tool result gets folded:
//   - results with more than `foldToolThreshold` rendered lines fold
//     by default (anything shorter shows in full — folding 3 lines is
//     pointless ceremony)
//   - the preview shows the first `foldToolPreviewLines` lines plus a
//     dim "+N more lines · Ctrl+O to expand" marker
const (
	foldToolThreshold    = 8
	foldToolPreviewLines = 3
)

// bannerSpec captures everything the welcome block displays: the ASCII
// art, the greeting, and per-session metadata (agent id, model, start
// time) rendered as labeled rows below the greeting. Empty fields are
// silently omitted, so the spec is safe to populate incrementally as
// the UI learns about its controller.
type bannerSpec struct {
	Art      string
	Greeting string
	Info     []bannerInfoRow
}

// bannerInfoRow is a single labeled row in the banner footer (e.g.
// `agent · a97f34ac…`). Label is dimmed, Value is bright so the eye
// lands on the changing data.
type bannerInfoRow struct {
	Label string
	Value string
}

// String returns the entire scrollback as one newline-joined buffer.
// Each block contributes its rendered content with a timeline prefix
// applied to every line; blocks are separated by a blank gutter line.
func (t *transcript) String() string {
	if len(t.blocks) == 0 {
		return ""
	}
	var out strings.Builder
	for i, b := range t.blocks {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(t.renderWithTimeline(b))
		// Blank gutter line between blocks. User prompts already emit
		// their own separator above, so skip the spacer after them to
		// avoid double padding.
		if i < len(t.blocks)-1 {
			out.WriteByte('\n')
			out.WriteString(t.gutterLine(b, t.blocks[i+1]))
		}
	}
	return out.String()
}

// renderWithTimeline prefixes every line of the block's content with
// the right gutter glyph for its kind. Banner / user-prompt blocks are
// emitted verbatim — they sit outside the timeline column.
func (t *transcript) renderWithTimeline(b transcriptBlock) string {
	switch b.kind {
	case blockBanner:
		return b.content
	case blockUserPrompt:
		return t.renderUserPrompt(b.content)
	case blockTool:
		return t.applyToolGutter(t.composeToolBlock(b))
	default:
		return t.applyLineGutter(b.content)
	}
}

// gutterLine emits the blank-line spacer between two blocks. It picks
// either a plain pipe (continuation) or empty (when the next block is
// a user prompt that will draw its own separator).
func (t *transcript) gutterLine(cur, next transcriptBlock) string {
	if next.kind == blockUserPrompt {
		// The prompt draws its own separator above its body — no
		// extra gutter pipe needed.
		return ""
	}
	if cur.kind == blockBanner {
		return ""
	}
	return styles.Timeline.Render("│")
}

// applyLineGutter prepends "│ " to every line of s. Long lines are
// word-wrapped to (t.width - 2) first so the viewport never has to
// horizontal-clip; the 2-col reserve covers the gutter glyph + space.
func (t *transcript) applyLineGutter(s string) string {
	if s == "" {
		return styles.Timeline.Render("│")
	}
	pipe := styles.Timeline.Render("│") + " "
	lines := strings.Split(wrapForWidth(s, t.width-2), "\n")
	for i, line := range lines {
		lines[i] = pipe + line
	}
	return strings.Join(lines, "\n")
}

// applyToolGutter prefixes the first line with "├─" (the branch-out
// connector) and subsequent lines with "│  " so the body sits in line
// with the connector's arm. Content wraps to (width - 3) — gutter is
// 3 cols wide here ("├─ " / "│  ").
func (t *transcript) applyToolGutter(s string) string {
	if s == "" {
		return styles.Timeline.Render("├─")
	}
	branch := styles.Timeline.Render("├─") + " "
	pipe := styles.Timeline.Render("│") + "  "
	lines := strings.Split(wrapForWidth(s, t.width-3), "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = branch + line
		} else {
			lines[i] = pipe + line
		}
	}
	return strings.Join(lines, "\n")
}

// wrapForWidth wraps s to w columns while preserving every printable
// rune — including leading whitespace on lines that resulted from a
// forced break. Critical for pasted code: dropping the first-column
// indent on wrapped continuations reads to the user as "my prompt got
// cut", which was the bug that prompted this implementation.
//
// We deliberately do NOT pre-pass through wordwrap: wordwrap resets
// its space buffer on every internal newline, which silently strips
// leading indentation when a single line is wider than the column
// (long URL, minified JSON, etc.). wrap.String alone — with
// PreserveSpace enabled — keeps all bytes while honoring existing \n
// breaks. The cost is the occasional mid-word break on a long
// unbroken token; we accept it because losing content is worse than
// breaking a URL across two lines.
func wrapForWidth(s string, w int) string {
	if w < 5 {
		return s
	}
	ww := wrap.NewWriter(w)
	ww.PreserveSpace = true
	_, _ = ww.Write([]byte(s))
	return ww.String()
}

// renderUserPrompt draws a HUD separator + bullet so the prompt reads
// as a hard break between conversation rounds — a thick double-line
// "scanline" across the column with a diamond on the left, then the
// prompt body wrapped to the transcript column.
func (t *transcript) renderUserPrompt(body string) string {
	width := t.width
	if width < 20 {
		width = 20
	}
	sep := strings.Repeat("═", width-2)
	return styles.TimelineCut.Render("◆═"+sep) + "\n" + wrapForWidth(body, width)
}

// setWidth installs (or re-installs) the markdown renderer for the
// given column width and records the width for separator sizing.
// Called from the model's layout pass on every terminal resize. Also
// re-flows the banner block so its centering tracks the new width.
func (t *transcript) setWidth(width int) {
	if width == t.width {
		return
	}
	t.width = width
	mdWidth := width - 2
	if mdWidth < 20 {
		mdWidth = 20
	}
	if t.md == nil || t.md.width != mdWidth {
		t.md = newMarkdownRenderer(mdWidth)
	}
	t.reflowBanner()
}

// renderAssistant returns the markdown-rendered version of an assistant
// text block, falling back to plain Assistant styling if glamour isn't
// available yet (e.g. before the first layout pass).
func (t *transcript) renderAssistant(s string) string {
	if t.md == nil {
		return styles.Assistant.Render(s)
	}
	return t.md.Render(s)
}

// setBanner stores the welcome-block spec and renders (or re-renders)
// the corresponding transcript block. Called from newRootModel with
// just the ASCII art + greeting, and again from Attach once the agent's
// metadata (id, model, start time) is known. Safe to call repeatedly;
// always operates on the same block slot, so the banner never
// duplicates itself.
func (t *transcript) setBanner(spec bannerSpec) {
	t.banner = spec
	t.reflowBanner()
}

// reflowBanner builds (or rebuilds) the styled banner block from the
// stored spec and slots it at t.bannerIdx, creating the slot if this
// is the first call. Width-dependent — re-invoked from setWidth on
// every resize so the centering tracks the terminal.
func (t *transcript) reflowBanner() {
	content := t.renderBannerContent()
	if content == "" {
		// Nothing to show — release the slot if we previously held one.
		if t.bannerIdx >= 0 && t.bannerIdx < len(t.blocks) {
			t.blocks = append(t.blocks[:t.bannerIdx], t.blocks[t.bannerIdx+1:]...)
		}
		t.bannerIdx = -1
		return
	}
	block := transcriptBlock{kind: blockBanner, content: content}
	if t.bannerIdx < 0 || t.bannerIdx >= len(t.blocks) {
		t.blocks = append([]transcriptBlock{block}, t.blocks...)
		t.bannerIdx = 0
		return
	}
	t.blocks[t.bannerIdx] = block
}

// renderBannerContent builds the styled banner block from t.banner.
// Layout inside the double-magenta border:
//
//   [ ASCII art ]
//   ──── ◆ ────       ← scanline separator
//   greeting
//   ──── ◆ ────
//   ▸ AGENT    …      ← HUD info rows
//   ▸ MODEL    …
//   ▸ STARTED  …
//
// Whole block is centered horizontally inside the transcript column.
// Returns "" when the spec is empty so callers can branch on "no
// banner to show".
func (t *transcript) renderBannerContent() string {
	art := strings.TrimRight(t.banner.Art, "\n")
	greeting := strings.TrimSpace(t.banner.Greeting)
	rows := t.banner.Info

	if art == "" && greeting == "" && len(rows) == 0 {
		return ""
	}

	// Pick a separator length proportional to the widest section so
	// the scanline reads as "in-line with everything else" instead
	// of floating.
	sepLen := bannerSepWidth(art, greeting, rows)
	separator := styles.Timeline.Render(strings.Repeat("─", sepLen/2)) +
		styles.TimelineCut.Render(" ◆ ") +
		styles.Timeline.Render(strings.Repeat("─", sepLen-sepLen/2-3))

	var inner strings.Builder
	if art != "" {
		inner.WriteString(styles.Banner.Render(art))
	}
	if greeting != "" {
		if inner.Len() > 0 {
			inner.WriteString("\n")
			inner.WriteString(separator)
			inner.WriteString("\n")
		}
		inner.WriteString(styles.Greeting.Render(greeting))
	}
	if len(rows) > 0 {
		if inner.Len() > 0 {
			inner.WriteString("\n")
			inner.WriteString(separator)
			inner.WriteString("\n")
		}
		inner.WriteString(renderBannerInfo(rows))
	}

	box := styles.BannerBox.Render(inner.String())
	if t.width <= 0 {
		return box
	}
	return lipgloss.PlaceHorizontal(t.width, lipgloss.Center, box)
}

// bannerSepWidth returns the column count to use for the in-banner
// scanline separator. Picks the widest section's width so the line
// spans roughly the same as the content above/below it. Clamped to a
// sane window so very tall ASCII art doesn't blow out the box.
func bannerSepWidth(art, greeting string, rows []bannerInfoRow) int {
	max := 0
	for _, line := range strings.Split(art, "\n") {
		if n := lipglossWidth(line); n > max {
			max = n
		}
	}
	if n := lipglossWidth(greeting); n > max {
		max = n
	}
	for _, r := range rows {
		// approximate: arrow + label + 2 + value
		n := 2 + len(r.Label) + 2 + len(r.Value)
		if n > max {
			max = n
		}
	}
	switch {
	case max < 24:
		return 24
	case max > 80:
		return 80
	default:
		return max
	}
}

// lipglossWidth is a thin alias for lipgloss.Width — wraps the import
// site so callers don't need to pull lipgloss themselves for one
// measurement.
func lipglossWidth(s string) int {
	return lipgloss.Width(s)
}

// renderBannerInfo lays out the labeled metadata rows beneath the
// greeting in HUD readout style: a hot-pink ▸ arrow, the upper-cased
// label in muted cyan, a fixed-width column gap, then the value in
// bright fog white. Reads like a system telemetry panel.
func renderBannerInfo(rows []bannerInfoRow) string {
	maxLabel := 0
	for _, r := range rows {
		if len(r.Label) > maxLabel {
			maxLabel = len(r.Label)
		}
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		label := strings.ToUpper(r.Label) + strings.Repeat(" ", maxLabel-len(r.Label))
		b.WriteString(styles.UserPrompt.Render("▸ "))
		b.WriteString(styles.BannerInfo.Render(label))
		b.WriteString("  ")
		b.WriteString(styles.StatusValue.Render(r.Value))
	}
	return b.String()
}

// appendUserPrompt records a prompt the user just submitted. Resets
// in-flight streaming so the prompt and the next assistant turn each
// get their own block. Also clears the tool-block map: tool IDs from
// the previous turn are gone, and a stale entry could cause the next
// turn's result to attach to the wrong block.
//
// Embedded ANSI escapes (paste boundary markers) survive intact:
// styling is applied per-line, and lines that already carry escapes
// are passed through verbatim.
func (t *transcript) appendUserPrompt(text string) {
	t.resetInflight()
	t.toolBlocks = nil
	t.blocks = append(t.blocks, transcriptBlock{
		kind:    blockUserPrompt,
		content: styleUserPromptLines(sanitizeForTranscript(text)),
	})
}

// styleUserPromptLines applies UserPrompt styling line-by-line so the
// `▶ ` head sits on row 0 and any lines that already carry ANSI codes
// (paste boundary chips, etc.) flow through without re-styling that
// would clobber their colors.
func styleUserPromptLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.ContainsRune(line, 0x1b) {
			if i == 0 {
				lines[i] = styles.UserPrompt.Render("▶ ") + line
			}
			continue
		}
		if i == 0 {
			lines[i] = styles.UserPrompt.Render("▶ " + line)
		} else {
			lines[i] = styles.UserPrompt.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// appendBlock appends a pre-styled block verbatim. Used by the UI to
// inject synthetic blocks (e.g. the all-tasks-complete snapshot).
func (t *transcript) appendBlock(block string) {
	if block == "" {
		return
	}
	t.resetInflight()
	t.blocks = append(t.blocks, transcriptBlock{
		kind:    blockSynthetic,
		content: block,
	})
}

// resetInflight closes the active streamed text/thinking blocks so the
// next chunk starts a fresh entry.
func (t *transcript) resetInflight() {
	t.textInflightIdx = -1
	t.thinkingInflightIdx = -1
	t.rawText = ""
	t.rawThinking = ""
}

// foldEvent translates one agent event into a transcript entry (or
// updates an in-flight one). Returns true if the transcript changed
// and the viewport should re-render.
func (t *transcript) foldEvent(e event.Event) bool {
	switch e.Kind {
	case event.KindThinking:
		t.resetInflight()
		if e.Thinking != nil && e.Thinking.Text != "" {
			t.blocks = append(t.blocks, transcriptBlock{
				kind:    blockThinking,
				content: styles.Thinking.Render("· " + sanitizeForTranscript(e.Thinking.Text)),
			})
			return true
		}
	case event.KindText:
		t.resetInflight()
		if e.Text != nil && e.Text.Text != "" {
			t.blocks = append(t.blocks, transcriptBlock{
				kind:    blockText,
				content: t.renderAssistant(sanitizeForTranscript(e.Text.Text)),
			})
			return true
		}
	case event.KindThinkingChunk:
		if e.Thinking == nil || e.Thinking.Text == "" {
			return false
		}
		t.rawThinking += sanitizeForTranscript(e.Thinking.Text)
		rendered := styles.Thinking.Render("· " + t.rawThinking)
		if t.thinkingInflightIdx >= 0 && t.thinkingInflightIdx < len(t.blocks) {
			t.blocks[t.thinkingInflightIdx].content = rendered
		} else {
			t.blocks = append(t.blocks, transcriptBlock{kind: blockThinking, content: rendered})
			t.thinkingInflightIdx = len(t.blocks) - 1
		}
		return true
	case event.KindTextChunk:
		if e.Text == nil || e.Text.Text == "" {
			return false
		}
		t.rawText += sanitizeForTranscript(e.Text.Text)
		rendered := t.renderAssistant(t.rawText)
		if t.textInflightIdx >= 0 && t.textInflightIdx < len(t.blocks) {
			t.blocks[t.textInflightIdx].content = rendered
		} else {
			t.blocks = append(t.blocks, transcriptBlock{kind: blockText, content: rendered})
			t.textInflightIdx = len(t.blocks) - 1
		}
		return true
	case event.KindToolUseStart:
		if e.ToolUseStart != nil {
			t.resetInflight()
			label := fmt.Sprintf("◢ %s(%s)", e.ToolUseStart.Name, compactInput(e.ToolUseStart.Input))
			t.blocks = append(t.blocks, transcriptBlock{
				kind:       blockTool,
				content:    styles.ToolCall.Render(label),
				toolID:     e.ToolUseStart.ToolID,
				hideResult: e.ToolUseStart.Name == string(tools.TOOL_SEARCH),
			})
			if t.toolBlocks == nil {
				t.toolBlocks = map[string]int{}
			}
			t.toolBlocks[e.ToolUseStart.ToolID] = len(t.blocks) - 1
			return true
		}
	case event.KindToolUseResult:
		if e.ToolUseResult != nil {
			t.resetInflight()
			return t.attachToolResult(e.ToolUseResult)
		}
	case event.KindCompacting:
		if e.Compacting != nil {
			t.resetInflight()
			label := fmt.Sprintf("↻ COMPACTING [%s]  ctx %.0f%%",
				strings.ToUpper(e.Compacting.Type), e.Compacting.UsageRatio*100)
			t.blocks = append(t.blocks, transcriptBlock{
				kind:    blockSystem,
				content: styles.Compacting.Render(label),
			})
			return true
		}
	case event.KindDrainingInfo:
		t.resetInflight()
		t.blocks = append(t.blocks, transcriptBlock{
			kind:    blockSystem,
			content: styles.Draining.Render("◈ DRAINING async subagent results"),
		})
		return true
	case event.KindError:
		if e.Error != nil {
			t.resetInflight()
			t.blocks = append(t.blocks, transcriptBlock{
				kind:    blockError,
				content: styles.ErrorBanner.Render(fmt.Sprintf("✘ [%s] %v", strings.ToUpper(e.Error.Stage), e.Error.Err)),
			})
			return true
		}
	case event.KindRunCancelled:
		t.resetInflight()
		t.blocks = append(t.blocks, transcriptBlock{
			kind:    blockSystem,
			content: styles.DimText.Render("◇ CANCELLED"),
		})
		return true
	case event.KindIterLimit:
		if e.IterLimit != nil {
			t.resetInflight()
			t.blocks = append(t.blocks, transcriptBlock{
				kind: blockSystem,
				content: styles.Compacting.Render(
					fmt.Sprintf("⏸ ITER-LIMIT %d — press Enter to continue", e.IterLimit.Reached)),
			})
			return true
		}
	}
	return false
}

// attachToolResult stores the styled result body on the matching tool
// block so renderWithTimeline can decide between a folded preview and
// the full payload at draw time. Falls back to appending a standalone
// block when the ToolID is unknown (defensive — the agent should
// always emit a start before the result).
//
// Visual contract:
//   - status glyph (`▸` green ok / `✘` red error) signals outcome
//   - body content rendered in soft sky blue (paletteSky) so it sits
//     beneath the brown call line as a quieter output stream
//   - FileDiff metadata (write_file / edit_file) renders below the
//     status line with its own +/− colored hunks; bundled into the
//     same toolResult so fold/expand applies uniformly
func (t *transcript) attachToolResult(r *event.ToolUseResultPayload) bool {
	body := strings.TrimRight(sanitizeForTranscript(r.Content), "\n")
	var resultBody string
	if r.IsError {
		resultBody = styles.ToolErr.Render("  ✘ ") + styles.ToolErr.Render(body)
	} else {
		resultBody = styles.ToolOK.Render("  ▸ ") + styles.ToolResult.Render(body)
	}
	hasDiff := false
	if diff, ok := r.Metadata.(*fs.FileDiff); ok && diff != nil {
		// Reserve 3 cols for the tool block's gutter (`├─ ` / `│  `)
		// so the colored fill terminates flush against the gutter
		// instead of bleeding past it.
		resultBody += "\n" + renderFileDiff(diff, t.width-3)
		hasDiff = true
	}

	idx, ok := t.toolBlocks[r.ToolID]
	if !ok || idx < 0 || idx >= len(t.blocks) {
		// No matching head — synthesize a bare block carrying just
		// the result. content stays empty; the result body itself
		// carries the user-facing output.
		t.blocks = append(t.blocks, transcriptBlock{
			kind:            blockTool,
			toolID:          r.ToolID,
			toolResult:      resultBody,
			toolResultLines: lineCount(resultBody),
			noFold:          hasDiff,
		})
		return true
	}
	if t.blocks[idx].hideResult {
		// tool_search and friends: replace the schema-laden payload
		// with a terse summary so users don't see the full tool
		// definitions, but still know the call resolved.
		resultBody = redactedResultBody(r.IsError, body)
		hasDiff = false
	}
	t.blocks[idx].toolResult = resultBody
	t.blocks[idx].toolResultLines = lineCount(resultBody)
	t.blocks[idx].noFold = hasDiff
	return true
}

// redactedResultBody renders the one-line placeholder shown in place
// of a hidden tool result. Errors are still surfaced — the body of an
// error is small and worth seeing — but successful payloads collapse
// to "▸ schema loaded".
func redactedResultBody(isError bool, body string) string {
	if isError {
		return styles.ToolErr.Render("  ✘ ") + styles.ToolErr.Render(body)
	}
	return styles.ToolOK.Render("  ▸ ") + styles.ToolResult.Render("schema loaded")
}

// composeToolBlock returns the rendered body for a blockTool: the head
// line (already in b.content) followed by the result body. The result
// is shown in full when:
//   - the transcript is in expand-all mode (Ctrl+O),
//   - the block opted out of folding (file write/edit — the diff is
//     the load-bearing output of the call; folding it defeats the
//     purpose of the call having happened),
//   - the result is short enough not to warrant folding, OR
//   - the result is empty (still in flight)
//
// Otherwise the renderer trims to the preview window and tacks on a
// dim `+N more lines · Ctrl+O to expand` marker so the user can see
// at a glance what's hidden and how to reveal it.
func (t *transcript) composeToolBlock(b transcriptBlock) string {
	if b.toolResult == "" {
		return b.content
	}
	if t.expandTools || b.noFold || b.toolResultLines <= foldToolThreshold {
		if b.content == "" {
			return b.toolResult
		}
		return b.content + "\n" + b.toolResult
	}
	preview := previewLines(b.toolResult, foldToolPreviewLines)
	hidden := b.toolResultLines - foldToolPreviewLines
	marker := styles.DimText.Render(
		fmt.Sprintf("  … +%d more lines · Ctrl+O to expand", hidden))
	if b.content == "" {
		return preview + "\n" + marker
	}
	return b.content + "\n" + preview + "\n" + marker
}

// previewLines returns the first n newline-separated lines of s. If s
// has n or fewer lines the original is returned unchanged. Preserves
// every embedded ANSI escape — we slice on \n boundaries only.
func previewLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

func compactInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	s := truncate(string(raw), 160)
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
