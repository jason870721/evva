package transcript

import (
	"strings"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Transcript is the scrollback model — the slice of Block values
// the user reads in the viewport, plus the bookkeeping that pairs
// streamed events to in-flight blocks.
//
// Concurrency: not safe for concurrent mutation. The bubbletea main
// loop is the only writer.
type Transcript struct {
	blocks []Block
	cache  *blockCache

	// width tracks the most recent terminal column count. Used by
	// View() to construct RenderContext; also used to decide when
	// to rebuild the markdown renderer.
	width int

	// theme is the live theme pointer. Updated by SetTheme; passed
	// through to RenderContext for every block render.
	theme *theme.Theme

	// markdown is the width-keyed glamour renderer. Rebuilt on
	// width change. Nil before SetWidth fires.
	markdown *Markdown

	// banner is the index of the BannerBlock in blocks (or -1).
	// Held separately so SetBanner can update in place.
	bannerIdx int

	// inflightText / inflightThink point to the live streaming
	// blocks (nil when no streaming turn is open). Cleared by
	// resetInflight() on any non-chunk event.
	inflightText  *TextBlock
	inflightThink *ThinkingBlock

	// toolBlocks maps a tool call's ToolID to its block, so
	// KindToolUseResult events can find the matching head. Parallel
	// tool calls in one turn keep entries until the next user
	// prompt resets the map.
	toolBlocks map[string]*ToolBlock

	// compacting is the live CompactingBlock (or nil). Tracked
	// separately so SetSpinnerFrame can find it without walking
	// the blocks slice.
	compacting *CompactingBlock

	// thinkingSprite is the live ThinkingSpriteBlock (or nil).
	// Appended to blocks when the agent enters StateThinking;
	// removed when it leaves. Frame advanced on spinner ticks.
	thinkingSprite *ThinkingSpriteBlock

	// expanded is the transcript-wide Ctrl+O override. true means
	// every tool block renders in full; false means each block
	// decides based on its own state.
	expanded bool

	// focusedID is the Block.ID() of the yank-mode focused block,
	// or 0 when no yank focus is active. View() passes
	// RenderOpts{Focused: true} for the matching block so its
	// gutter renders in the cyan accent style.
	focusedID uint64

	// matches maps Block.ID() to the byte ranges in PlainText()
	// where the current search query matched. View() forwards
	// these via RenderOpts.Highlights so the block's gutter
	// renders in the yellow match-accent style (per-character
	// highlighting is deferred — M9 paints whole-block accent
	// only). Nil / empty when no search is active.
	matches map[uint64][]Range
}

// New constructs a transcript with no blocks. The caller must call
// SetTheme + SetWidth before View; until then the markdown renderer
// is nil and TextBlocks fall back to the plain Assistant style.
func New() *Transcript {
	return &Transcript{
		cache:     newBlockCache(),
		bannerIdx: -1,
	}
}

// SetTheme installs the active theme. Subsequent renders will use
// it; cache invalidation is automatic via the theme's Rev field.
func (t *Transcript) SetTheme(th *theme.Theme) {
	t.theme = th
}

// SetWidth installs (or re-installs) the column count. When it
// changes, the markdown renderer is rebuilt; the cache invalidates
// automatically since the cache key includes width + mdRev.
//
// width < 1 is treated as "unknown yet" and ignored — defends
// against early WindowSizeMsg with zero dims on some terminals.
func (t *Transcript) SetWidth(width int) {
	if width < 1 || width == t.width {
		return
	}
	t.width = width
	mdWidth := width - 2
	if mdWidth < 20 {
		mdWidth = 20
	}
	if t.markdown == nil || t.markdown.Width() != mdWidth {
		t.markdown = NewMarkdown(mdWidth)
	}
}

// SetBanner installs (or replaces) the welcome block at index 0.
// First call appends; subsequent calls mutate in place.
func (t *Transcript) SetBanner(spec BannerSpec) {
	if t.bannerIdx >= 0 && t.bannerIdx < len(t.blocks) {
		if bb, ok := t.blocks[t.bannerIdx].(*BannerBlock); ok {
			bb.SetSpec(spec)
			return
		}
	}
	bb := NewBannerBlock(spec)
	t.blocks = append([]Block{bb}, t.blocks...)
	t.bannerIdx = 0
	// Following blocks' IDs are unchanged; their cache entries
	// remain valid. No Clear needed.
}

// AppendBlock appends a pre-built block verbatim. Used by the App
// for synthetic insertions (e.g. an all-tasks-complete snapshot in
// M6) and by tests. Resets streaming markers so the next chunk
// starts a fresh entry.
func (t *Transcript) AppendBlock(b Block) {
	if b == nil {
		return
	}
	t.resetInflight()
	t.appendKeepingSprite(b)
}

// AppendUserPrompt records a prompt the user just submitted.
// Resets streaming and clears the tool-block map: tool IDs from the
// previous turn are gone, and a stale entry could route the next
// turn's result to the wrong block.
func (t *Transcript) AppendUserPrompt(text string) {
	t.resetInflight()
	t.toolBlocks = nil
	t.appendKeepingSprite(newUserPromptBlock(sanitizeForTranscript(text)))
}

// AppendSynthetic injects a pre-formatted styled block. The text
// is rendered verbatim with the standard line gutter.
func (t *Transcript) AppendSynthetic(text string) {
	if text == "" {
		return
	}
	t.resetInflight()
	t.appendKeepingSprite(newSyntheticBlock(text))
}

// appendKeepingSprite is the canonical append helper for every block
// other than the thinking sprite itself. When the sprite is mounted,
// it stays anchored at the tail of the blocks slice — new content
// slides in just before it so the "walking sprite" reads as sitting
// at the end of the latest output rather than getting stranded above
// streaming text and tool blocks.
//
// When the sprite isn't mounted, this collapses to a plain append.
func (t *Transcript) appendKeepingSprite(b Block) {
	if b == nil {
		return
	}
	n := len(t.blocks)
	if t.thinkingSprite != nil && n > 0 && t.blocks[n-1] == t.thinkingSprite {
		// Insert b at position n-1; push the sprite to position n.
		// Single grow-and-shift — cheaper than two separate slice ops.
		t.blocks = append(t.blocks, t.thinkingSprite)
		t.blocks[n-1] = b
		return
	}
	t.blocks = append(t.blocks, b)
}

// LoadFromMessages rehydrates the transcript from a persisted session.
// Reset() is called first so this replaces the current view rather than
// appending. Each llm.Message maps to one or more blocks:
//
//   - RoleUser{Content}                  → UserPromptBlock
//   - RoleAssistant{Thinking}            → ThinkingBlock (when non-empty)
//   - RoleAssistant{Content}             → TextBlock     (when non-empty)
//   - RoleAssistant{ToolCalls[]}         → one ToolBlock per call
//   - RoleTool{ToolResults[]}            → SetResult on the matching
//                                          ToolBlock (paired by Call.ID)
//
// System messages are skipped (they don't appear in live transcripts
// either). Block IDs are freshly allocated — the persisted file does
// not carry UI-side identifiers.
//
// Called by the /resume flow after Controller.ResumeSession swaps the
// agent's live session with the loaded snapshot.
func (t *Transcript) LoadFromMessages(msgs []llm.Message) {
	t.Reset()
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			text := strings.TrimSpace(m.Content)
			if text == "" {
				continue
			}
			t.appendKeepingSprite(newUserPromptBlock(sanitizeForTranscript(text)))
			// AppendUserPrompt would also reset toolBlocks; we avoid that
			// here because the loop may still need to route tool_results
			// from a *prior* assistant turn (RoleUser messages from
			// system reminders / queued prompts can interleave on resume).
		case llm.RoleAssistant:
			if think := strings.TrimSpace(m.Thinking); think != "" {
				t.appendKeepingSprite(newThinkingBlock(sanitizeForTranscript(think)))
			}
			if text := strings.TrimSpace(m.Content); text != "" {
				t.appendKeepingSprite(newTextBlock(sanitizeForTranscript(text)))
			}
			for _, call := range m.ToolCalls {
				if call == nil {
					continue
				}
				hideResult := call.Name == string(tools.TOOL_SEARCH)
				tb := newToolBlock(call.Name, call.ID, call.Input, hideResult)
				t.appendKeepingSprite(tb)
				if t.toolBlocks == nil {
					t.toolBlocks = map[string]*ToolBlock{}
				}
				t.toolBlocks[call.ID] = tb
			}
		case llm.RoleTool:
			for _, r := range m.ToolResults {
				if r == nil {
					continue
				}
				tb, ok := t.toolBlocks[r.ID]
				if !ok {
					continue
				}
				tb.SetResult(r.Content, r.IsError, nil, r.ContentBlocks)
			}
		}
	}
	// Inflight bookkeeping is purely a streaming concern. Persisted
	// transcripts are already terminal — clear it so the next live
	// turn's first chunk doesn't try to extend a non-existent block.
	t.resetInflight()
}

// Reset wipes all blocks except the banner. Used by /clear and by
// /model after a provider swap — both want a fresh conversation
// view but keep the welcome block at the top.
func (t *Transcript) Reset() {
	var banner Block
	if t.bannerIdx >= 0 && t.bannerIdx < len(t.blocks) {
		banner = t.blocks[t.bannerIdx]
	}
	t.blocks = t.blocks[:0]
	t.inflightText = nil
	t.inflightThink = nil
	t.toolBlocks = nil
	t.compacting = nil
	t.thinkingSprite = nil
	t.cache.Clear()
	if banner != nil {
		t.blocks = append(t.blocks, banner)
		t.bannerIdx = 0
	} else {
		t.bannerIdx = -1
	}
}

// ToggleExpand flips the transcript-wide Ctrl+O override. Walks all
// tool blocks and bumps their Rev so the cache invalidates them.
func (t *Transcript) ToggleExpand() {
	t.expanded = !t.expanded
	for _, b := range t.blocks {
		if tb, ok := b.(*ToolBlock); ok {
			tb.SetExpanded(t.expanded)
		}
	}
}

// Expanded reports the current Ctrl+O override state. The App's
// status bar reads this to show "expanded" / "folded" hint.
func (t *Transcript) Expanded() bool { return t.expanded }

// SetFocusedBlock marks one block as the yank-mode cursor target.
// id==0 clears the focus (no block highlighted).
//
// The cache invalidates only the previously-focused and the newly-
// focused entries — every other block stays cached. Cost is one
// render per focus shift, not one full re-render.
func (t *Transcript) SetFocusedBlock(id uint64) {
	if id == t.focusedID {
		return
	}
	t.focusedID = id
}

// FocusedBlock returns the currently focused Block ID, or 0 when
// no yank focus is active. Test-only / yank-mode internal use.
func (t *Transcript) FocusedBlock() uint64 { return t.focusedID }

// SetSearchMatches installs the current search-result map. View()
// passes RenderOpts.Highlights for any block in the map; blocks
// without an entry render normally.
//
// Passing nil clears the search highlight. The cache invalidates
// for each block whose Highlights signature changed (via optsRev),
// which means: every previously-matched block re-renders, every
// newly-matched block re-renders, and untouched blocks stay
// cached.
func (t *Transcript) SetSearchMatches(m map[uint64][]Range) {
	t.matches = m
}

// MatchedBlocks returns the IDs of every block with at least one
// search match, in transcript order. Search overlay uses this as
// the navigation cursor target list.
func (t *Transcript) MatchedBlocks() []uint64 {
	if len(t.matches) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(t.matches))
	for _, b := range t.blocks {
		if _, ok := t.matches[b.ID()]; ok {
			out = append(out, b.ID())
		}
	}
	return out
}

// LineOffsetOf returns the rendered line index where the block
// with the given ID begins, or -1 when the block isn't in the
// scrollback. Used by the View's RevealBlock to scroll the
// viewport so the target is visible.
//
// Walks the cached output by counting newlines in each block's
// rendered string plus the inter-block spacer. Cheap — strings
// are already in the cache, no re-render happens.
func (t *Transcript) LineOffsetOf(id uint64) int {
	if t.width < 1 || t.theme == nil {
		return -1
	}
	baseCtx := RenderContext{
		Width:    t.width,
		Theme:    t.theme,
		Markdown: t.markdown,
	}
	offset := 0
	for i, b := range t.blocks {
		if b.ID() == id {
			return offset
		}
		ctx := baseCtx
		if t.focusedID != 0 && b.ID() == t.focusedID {
			ctx.Opts.Focused = true
		}
		if hits, ok := t.matches[b.ID()]; ok && len(hits) > 0 {
			ctx.Opts.Highlights = hits
		}
		rendered := t.cache.Get(b, ctx)
		offset += strings.Count(rendered, "\n") + 1
		// Inter-block spacer (one line) — every adjacent pair
		// except after a banner-to-X transition where the spacer
		// is "" (still one line via the View's "\n" + spacer
		// pattern).
		if i < len(t.blocks)-1 {
			offset++
		}
	}
	return -1
}

// SetSpinnerFrame updates the live compaction row's animation
// frame, if one exists. No-op when no compaction is in flight.
func (t *Transcript) SetSpinnerFrame(frame int) {
	if t.compacting != nil {
		t.compacting.SetFrame(frame)
	}
}

// HasInflightCompacting reports whether a CompactingBlock is
// currently mounted. App uses this to decide whether to schedule a
// spinner tick — no compaction means no animation needed.
func (t *Transcript) HasInflightCompacting() bool {
	return t.compacting != nil
}

// ShowThinkingSprite appends the animated thinking sprite to the end
// of the blocks slice. No-op when already shown.
func (t *Transcript) ShowThinkingSprite() {
	if t.thinkingSprite != nil {
		return
	}
	sb := newThinkingSpriteBlock()
	t.thinkingSprite = sb
	t.blocks = append(t.blocks, sb)
}

// HideThinkingSprite removes the animated thinking sprite from the
// blocks slice. No-op when not shown.
func (t *Transcript) HideThinkingSprite() {
	if t.thinkingSprite == nil {
		return
	}
	t.removeBlock(t.thinkingSprite)
	t.thinkingSprite = nil
}

// HasThinkingSprite reports whether the thinking sprite is currently
// mounted. App uses this to advance the frame on spinner ticks.
func (t *Transcript) HasThinkingSprite() bool {
	return t.thinkingSprite != nil
}

// SetThinkingSpriteFrame advances the thinking sprite's animation
// frame. No-op when the sprite isn't mounted.
func (t *Transcript) SetThinkingSpriteFrame(frame int) {
	if t.thinkingSprite != nil {
		t.thinkingSprite.SetFrame(frame)
	}
}

// resetInflight closes the active streaming text/thinking blocks
// so the next chunk starts a fresh entry. Does NOT clear the
// tool-block map — tool pairing must survive intra-turn streaming
// resets.
func (t *Transcript) resetInflight() {
	t.inflightText = nil
	t.inflightThink = nil
}

// IngestEvent translates an agent event into a transcript mutation
// (or updates an in-flight block). Returns true if anything
// changed and the App should refresh the viewport.
//
// Mirrors v1's foldEvent semantics but operates on typed blocks.
// Events that don't concern the transcript (RunStart/End, Usage,
// StoreUpdate) return false silently.
func (t *Transcript) IngestEvent(e event.Event) bool {
	switch e.Kind {
	case event.KindThinking:
		t.resetInflight()
		if e.Thinking != nil && e.Thinking.Text != "" {
			t.appendKeepingSprite(newThinkingBlock(sanitizeForTranscript(e.Thinking.Text)))
			return true
		}
	case event.KindText:
		t.resetInflight()
		if e.Text != nil && e.Text.Text != "" {
			t.appendKeepingSprite(newTextBlock(sanitizeForTranscript(e.Text.Text)))
			return true
		}
	case event.KindThinkingChunk:
		if e.Thinking == nil || e.Thinking.Text == "" {
			return false
		}
		chunk := sanitizeForTranscript(e.Thinking.Text)
		if t.inflightThink != nil {
			t.inflightThink.Append(chunk)
		} else {
			b := newThinkingBlock(chunk)
			t.inflightThink = b
			t.appendKeepingSprite(b)
		}
		return true
	case event.KindTextChunk:
		if e.Text == nil || e.Text.Text == "" {
			return false
		}
		chunk := sanitizeForTranscript(e.Text.Text)
		if t.inflightText != nil {
			t.inflightText.Append(chunk)
		} else {
			b := newTextBlock(chunk)
			t.inflightText = b
			t.appendKeepingSprite(b)
		}
		return true
	case event.KindToolUseStart:
		if e.ToolUseStart != nil {
			t.resetInflight()
			hideResult := e.ToolUseStart.Name == string(tools.TOOL_SEARCH)
			tb := newToolBlock(e.ToolUseStart.Name, e.ToolUseStart.ToolID, e.ToolUseStart.Input, hideResult)
			t.appendKeepingSprite(tb)
			if t.toolBlocks == nil {
				t.toolBlocks = map[string]*ToolBlock{}
			}
			t.toolBlocks[e.ToolUseStart.ToolID] = tb
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
			if t.compacting != nil {
				// Re-use the existing inflight row — the agent only
				// ever runs one compaction at a time; a second
				// Compacting before CompactingEnd means the auto
				// path picked up after the manual chooser fired,
				// or vice versa.
				t.compacting.SetKind(e.Compacting.Type)
				return true
			}
			cb := newCompactingBlock(e.Compacting.Type)
			t.compacting = cb
			t.appendKeepingSprite(cb)
			return true
		}
	case event.KindCompactingEnd:
		if e.CompactingEnd != nil && t.compacting != nil {
			cb := t.compacting
			if e.CompactingEnd.OK {
				// Drop the animated row — the visible effect is
				// the context bar dropping in the status HUD.
				t.removeBlock(cb)
			} else {
				// Swap the spinner row for an error block so the
				// user knows compact didn't actually run.
				msg := strings.TrimSpace(e.CompactingEnd.Err)
				if msg == "" {
					msg = "compact failed"
				}
				stage := "COMPACT [" + strings.ToUpper(e.CompactingEnd.Type) + "]"
				t.replaceBlock(cb, newErrorBlock(stage, msg))
			}
			t.compacting = nil
			return true
		}
	case event.KindDrainingInfo:
		t.resetInflight()
		t.appendKeepingSprite(newDrainingBlock())
		return true
	case event.KindError:
		if e.Error != nil {
			t.resetInflight()
			t.appendKeepingSprite(newErrorBlock(e.Error.Stage, e.Error.Err.Error()))
			return true
		}
	case event.KindRunCancelled:
		t.resetInflight()
		t.appendKeepingSprite(newCancelledBlock())
		return true
	case event.KindTurnEnd:
		if e.Turn != nil {
			t.resetInflight()
			t.appendKeepingSprite(newTurnEndBlock(e.Turn.Iteration))
			return true
		}
	case event.KindIterLimit:
		if e.IterLimit != nil {
			t.resetInflight()
			t.appendKeepingSprite(newIterLimitBlock(e.IterLimit.Iters))
			return true
		}
	case event.KindBgResult:
		if e.BgResult != nil {
			t.resetInflight()
			t.appendKeepingSprite(newBgResultBlock(e.BgResult))
			return true
		}
	case event.KindMonitorEvent:
		if e.MonitorEvent != nil {
			t.resetInflight()
			t.appendKeepingSprite(newMonitorEventBlock(e.MonitorEvent))
			return true
		}
	}
	return false
}

// attachToolResult finds the matching ToolBlock by ToolID and
// attaches the result body. Falls back to appending a standalone
// block when the ToolID is unknown (defensive — the agent should
// always emit a start before the result).
func (t *Transcript) attachToolResult(r *event.ToolUseResultPayload) bool {
	content := sanitizeForTranscript(r.Content)

	// Web tools dump voluminous content (page text, search
	// snippets) that the model already summarises for the user.
	// Collapse the result to its first non-empty line on success;
	// errors stay verbose so the user sees what went wrong.
	if !r.IsError {
		if tb, ok := t.toolBlocks[r.ToolID]; ok && isWebSummaryTool(tb.Name()) {
			content = firstNonEmptyLine(content)
		}
	}

	var diffMeta *fs.FileDiff
	if d, ok := r.Metadata.(*fs.FileDiff); ok && d != nil {
		diffMeta = d
	}

	if tb, ok := t.toolBlocks[r.ToolID]; ok {
		tb.SetResult(content, r.IsError, diffMeta, r.ContentBlocks)
		return true
	}

	// No matching head — synthesise a bare ToolBlock carrying just
	// the result. ToolBlock.compose handles head=="" by emitting
	// the result body alone.
	stub := newToolBlock("?", r.ToolID, nil, false)
	stub.SetResult(content, r.IsError, diffMeta, r.ContentBlocks)
	t.appendKeepingSprite(stub)
	if t.toolBlocks == nil {
		t.toolBlocks = map[string]*ToolBlock{}
	}
	t.toolBlocks[r.ToolID] = stub
	return true
}

// removeBlock deletes b from blocks and drops its cache entry.
// Adjusts bannerIdx and toolBlocks map if affected.
func (t *Transcript) removeBlock(b Block) {
	id := b.ID()
	for i, x := range t.blocks {
		if x.ID() == id {
			t.blocks = append(t.blocks[:i], t.blocks[i+1:]...)
			t.cache.Drop(id)
			// bannerIdx shifts if we removed something ahead.
			if t.bannerIdx > i {
				t.bannerIdx--
			} else if t.bannerIdx == i {
				t.bannerIdx = -1
			}
			break
		}
	}
	// Also drop from tool map if it was a ToolBlock.
	if tb, ok := b.(*ToolBlock); ok {
		delete(t.toolBlocks, tb.ToolID())
	}
}

// replaceBlock swaps b for replacement at the same index, dropping
// the old cache entry. Used by KindCompactingEnd OK=false to
// substitute the spinner with an ErrorBlock.
func (t *Transcript) replaceBlock(b, replacement Block) {
	id := b.ID()
	for i, x := range t.blocks {
		if x.ID() == id {
			t.blocks[i] = replacement
			t.cache.Drop(id)
			return
		}
	}
}

// View renders the whole scrollback as one newline-joined string,
// honouring the cache. Called from the viewport wrapper.
//
// Empty width returns "" — callers shouldn't render until SetWidth
// has been called.
func (t *Transcript) View() string {
	if t.width < 1 || t.theme == nil || len(t.blocks) == 0 {
		return ""
	}
	baseCtx := RenderContext{
		Width:    t.width,
		Theme:    t.theme,
		Markdown: t.markdown,
	}
	var out strings.Builder
	for i, b := range t.blocks {
		if i > 0 {
			out.WriteByte('\n')
		}
		ctx := baseCtx
		// Per-block opts: focus flag for the yank-mode cursor;
		// highlights slice for search-match highlighting.
		if t.focusedID != 0 && b.ID() == t.focusedID {
			ctx.Opts.Focused = true
		}
		if hits, ok := t.matches[b.ID()]; ok && len(hits) > 0 {
			ctx.Opts.Highlights = hits
		}
		out.WriteString(t.cache.Get(b, ctx))
		// Inter-block spacer: empty line with optional gutter pipe.
		if i < len(t.blocks)-1 {
			out.WriteByte('\n')
			out.WriteString(interBlockSpacer(b.Kind(), t.blocks[i+1].Kind(), t.theme))
		}
	}
	return out.String()
}

// Blocks returns a snapshot of the current block slice. Read-only
// access for M8 yank mode + M9 search. The returned slice shares
// backing storage with the transcript; callers must not mutate.
func (t *Transcript) Blocks() []Block {
	return t.blocks
}

// Width / Theme / Markdown accessors — used by the App when
// constructing RenderContext for outside-Transcript renders (e.g.
// the M10 permission overlay's diff preview).
func (t *Transcript) Width() int            { return t.width }
func (t *Transcript) Theme() *theme.Theme   { return t.theme }
func (t *Transcript) Markdown() *Markdown   { return t.markdown }

// CacheSize is a test hook reporting how many entries the cache
// holds. Not exported beyond this package.
func (t *Transcript) cacheSize() int { return t.cache.Size() }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isWebSummaryTool reports whether the named tool's successful
// results should collapse to a one-line summary. The raw payload
// (page text, search snippets) is voluminous and the model
// summarises it for the user anyway.
func isWebSummaryTool(name string) bool {
	return name == string(tools.WEB_FETCH) || name == string(tools.WEB_SEARCH)
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
// Used to extract the header line web_fetch / web_search prepend to
// their payloads ("[Fetched: ...]", 'Search results for "..."').
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return s
}
