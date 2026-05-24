package transcript

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/diff"
)

// ToolBlock holds a tool call's invocation head and (eventually) its
// result. Pairing with the result event is done by the transcript
// via ToolID lookup; ToolBlock itself just exposes setters.
//
// State fields:
//
//   - head:       the styled `◢ name(args)` line shown on the gutter
//   - rawHead:    same content but plain — fed to PlainText() and
//                 to the search index
//   - name:       the tool name (used to decide e.g. web-summary
//                 collapse on the result; M3 supports a basic
//                 hideResult flag for tool_search)
//   - toolID:     correlation token from the agent event stream
//   - resultBody: styled multi-line result (lazy: empty until the
//                 KindToolUseResult event lands and SetResult is
//                 called)
//   - rawResult:  same plain — for PlainText/search
//   - resultLines: line count of resultBody, used to drive fold
//   - hasError:   true when the result was IsError — affects the
//                 status glyph and the styling choice
//   - hasDiff:    true when Metadata carried a *fs.FileDiff — diff
//                 results bypass the fold pass (their entire value is
//                 the visible artifact)
//   - hideResult: true for tool_search and similar — payload is
//                 replaced with a "schema loaded" placeholder
//   - expanded:   transcript-level Ctrl+O fold override
type ToolBlock struct {
	id  uint64
	rev uint64

	toolID  string
	name    string
	rawHead string
	head    string

	rawResult   string
	resultBody  string
	resultLines int
	hasError    bool
	hasDiff     bool
	hideResult  bool

	// diff is the structured payload (if any) the tool reported.
	// Kept on the block so Render can re-style it on theme swap or
	// width change without the transcript having to re-parse the
	// event.
	diff *fs.FileDiff

	// imageBlocks carries multimodal content blocks from the tool result.
	// Rendered as [image: <mime>, <bytes>] stubs appended after the text body.
	imageBlocks []tools.ContentBlock

	expanded bool
}

func newToolBlock(name, toolID string, input json.RawMessage, hideResult bool) *ToolBlock {
	rawHead := fmt.Sprintf("◢ %s(%s)", name, compactJSON(input))
	return &ToolBlock{
		id:         allocID(),
		rev:        1,
		toolID:     toolID,
		name:       name,
		rawHead:    rawHead,
		hideResult: hideResult,
	}
}

func (b *ToolBlock) ID() uint64    { return b.id }
func (b *ToolBlock) Rev() uint64   { return b.rev }
func (b *ToolBlock) Kind() Kind    { return KindTool }
func (b *ToolBlock) ToolID() string { return b.toolID }
func (b *ToolBlock) Name() string  { return b.name }

func (b *ToolBlock) PlainText() string {
	if b.rawResult == "" {
		return b.rawHead
	}
	return b.rawHead + "\n" + b.rawResult
}

// SetResult attaches the result body to the block. content is the
// model-facing summary; isError true means the call failed; diffMeta
// is non-nil for write_file / edit_file calls. imageBlocks carries
// multimodal content (e.g. read of a PNG) for inline rendering.
//
// The transcript calls this when the matching KindToolUseResult event
// arrives. Idempotent — re-applying the same result is a no-op (Rev
// not bumped, cache not invalidated).
func (b *ToolBlock) SetResult(content string, isError bool, diffMeta *fs.FileDiff, imageBlocks []tools.ContentBlock) {
	plain := strings.TrimRight(content, "\n")
	if b.hideResult && !isError {
		// tool_search and friends — collapse to a fixed placeholder.
		plain = "schema loaded"
	}

	// Append image block stubs to the plain-text result so they appear
	// in search / yank and in the rendered body.
	for _, cb := range imageBlocks {
		if cb.Type == tools.ContentBlockImage && cb.Image != nil {
			plain += fmt.Sprintf("\n[image: %s, %d bytes]", cb.Image.MIMEType, cb.Image.OriginalSize)
		}
	}

	if plain == b.rawResult && isError == b.hasError && diffMeta == b.diff {
		return
	}
	b.rawResult = plain
	b.hasError = isError
	b.diff = diffMeta
	b.hasDiff = diffMeta != nil
	b.imageBlocks = imageBlocks
	b.rev++
}

// SetExpanded toggles the per-block "show full result" override.
// expanded=true forces full render; false defers to fold logic. The
// transcript-wide Ctrl+O wires through this on every tool block.
func (b *ToolBlock) SetExpanded(v bool) {
	if v == b.expanded {
		return
	}
	b.expanded = v
	b.rev++
}

// Expanded reports whether this block's per-block override is
// currently on. Used by yank mode to flip a single block
// independently of the transcript-wide Ctrl+O state.
func (b *ToolBlock) Expanded() bool { return b.expanded }

// Render builds the gutter-prefixed call head + optional folded /
// expanded result body.
func (b *ToolBlock) Render(ctx RenderContext) string {
	// Build head styling — this part is deterministic; the tool
	// label always renders in ToolCall brown bold.
	b.head = ctx.Theme.ToolCall.Render(b.rawHead)

	// Style the result body (if any). Build from scratch on every
	// render so theme swaps reflow correctly.
	var styledResult string
	var styledLines int
	if b.rawResult != "" {
		styledResult = b.styleResultBody(ctx)
		styledLines = lineCount(styledResult)
	}
	b.resultBody = styledResult
	b.resultLines = styledLines

	body := b.compose()
	return applyToolGutter(body, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// styleResultBody produces the multi-line styled result + optional
// diff. Mirrors v1's attachToolResult render path; differs only in
// that styling happens at Render time, not ingest time.
func (b *ToolBlock) styleResultBody(ctx RenderContext) string {
	var styled string
	if b.hasError {
		styled = ctx.Theme.ToolErr.Render("  ✘ ") + ctx.Theme.ToolErr.Render(b.rawResult)
	} else {
		styled = ctx.Theme.ToolOK.Render("  ▸ ") + ctx.Theme.ToolResult.Render(b.rawResult)
	}
	if b.hasDiff && b.diff != nil {
		// Reserve 3 cols for the tool gutter (`├─ ` / `│  `) so
		// the colored fill terminates flush against the gutter
		// instead of bleeding past it.
		styled += "\n" + diff.Render(b.diff, ctx.Theme, ctx.Width-3)
	}
	return styled
}

// compose decides between full result, folded preview, or head-only
// based on per-block + transcript-wide flags.
func (b *ToolBlock) compose() string {
	if b.resultBody == "" {
		return b.head
	}
	// Diff results always show in full — the diff IS the call's
	// artifact, folding it defeats the purpose. Ctrl+O override
	// also forces full render for every tool.
	if b.expanded || b.hasDiff {
		if b.head == "" {
			return b.resultBody
		}
		return b.head + "\n" + b.resultBody
	}
	// Non-diff results: only show full when trivially short
	// (1 line). Everything else collapses to a single summary
	// line so the transcript stays scannable without drowning
	// in tool output noise.
	if b.resultLines <= 1 {
		if b.head == "" {
			return b.resultBody
		}
		return b.head + "\n" + b.resultBody
	}
	preview := previewLines(b.resultBody, 1)
	hidden := b.resultLines - 1
	marker := fmt.Sprintf("  … +%d more lines · Ctrl+O to expand", hidden)
	if b.head == "" {
		return preview + "\n" + marker
	}
	return b.head + "\n" + preview + "\n" + marker
}

// previewLines returns the first n newline-separated lines of s.
// Preserves embedded ANSI escapes — slices on `\n` only.
func previewLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// lineCount returns the number of `\n`-separated lines in s.
// Includes a trailing empty line when s ends in `\n`.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// compactJSON renders raw JSON as a single-line preview for the
// tool head. Bytes after the 160th are truncated with `…`.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	s := truncateString(string(raw), 160)
	return strings.Join(strings.Fields(s), " ")
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
