package transcript

// TextBlock holds assistant final text (KindText) or the rolling
// accumulator for a streamed turn (KindTextChunk). Markdown rendering
// is deferred to Render time so a streaming chunk only mutates the
// raw string + bumps Rev; glamour runs on the next cache miss.
//
// Concurrency: not safe for concurrent Append. The bubbletea event
// loop is the sole writer.
type TextBlock struct {
	id   uint64
	rev  uint64
	text string
}

func newTextBlock(initial string) *TextBlock {
	return &TextBlock{id: allocID(), rev: 1, text: initial}
}

func (b *TextBlock) ID() uint64  { return b.id }
func (b *TextBlock) Rev() uint64 { return b.rev }
func (b *TextBlock) Kind() Kind  { return KindText }

// PlainText returns the raw markdown source — useful for yank/copy
// (users want the markdown, not the rendered ANSI) and for search
// (search the source, not the post-glamour decorations).
func (b *TextBlock) PlainText() string { return b.text }

// Append adds chunk to the accumulator and bumps Rev so the cache
// re-renders this block on the next View pass. Empty chunks are
// no-ops — they shouldn't bump Rev or the cache thrashes.
func (b *TextBlock) Append(chunk string) {
	if chunk == "" {
		return
	}
	b.text += chunk
	b.rev++
}

// Render builds the gutter-prefixed, glamour-rendered final form.
// Falls back to th.Assistant when no Markdown renderer is attached
// (early boot, before the first WindowSizeMsg).
func (b *TextBlock) Render(ctx RenderContext) string {
	var styled string
	if ctx.Markdown != nil {
		styled = ctx.Markdown.Render(b.text)
	} else {
		styled = ctx.Theme.Assistant.Render(b.text)
	}
	return applyLineGutter(styled, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// ----------------------------------------------------------------------------

// ThinkingBlock mirrors TextBlock for KindThinking / KindThinkingChunk.
// Rendered in muted italic (no glamour) — thinking blocks should read
// as the model's internal aside, not as final content.
type ThinkingBlock struct {
	id   uint64
	rev  uint64
	text string
}

func newThinkingBlock(initial string) *ThinkingBlock {
	return &ThinkingBlock{id: allocID(), rev: 1, text: initial}
}

func (b *ThinkingBlock) ID() uint64       { return b.id }
func (b *ThinkingBlock) Rev() uint64      { return b.rev }
func (b *ThinkingBlock) Kind() Kind       { return KindThinking }
func (b *ThinkingBlock) PlainText() string { return b.text }

func (b *ThinkingBlock) Append(chunk string) {
	if chunk == "" {
		return
	}
	b.text += chunk
	b.rev++
}

func (b *ThinkingBlock) Render(ctx RenderContext) string {
	// `· ` prefix marks the block as thinking even when the user
	// hasn't seen its gutter (search results, copy-mode preview).
	styled := ctx.Theme.Thinking.Render("· " + b.text)
	return applyLineGutter(styled, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}
