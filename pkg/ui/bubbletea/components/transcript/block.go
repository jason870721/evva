// Package transcript owns the scrollback model of the v2 TUI. It
// stores every logical entry the user reads in the viewport (banner,
// user prompts, assistant text, thinking, tool calls + results,
// system rows, errors, compaction inflight) as a slice of Block
// values, and renders the scrollback as one styled string the App's
// viewport scrolls through.
//
// Design highlights vs. v1 (internal/ui/bubbletea/transcript.go):
//
//   - Blocks are an interface, not a tagged struct. Each Kind has
//     its own concrete type with its own internal state. Adding a
//     new Kind is one new file, not a new branch in five switches.
//
//   - PlainText() is a first-class method on Block — yank-mode (M8)
//     and search (M9) read from it. v1 had no way to recover plain
//     text; it always re-rendered.
//
//   - Each Block exposes a Rev() counter. The transcript's per-block
//     render cache keys on (Width, Theme.Rev, Markdown.Rev, Block.Rev,
//     RenderOpts.Rev) so unchanged blocks skip re-rendering on every
//     frame. v1 re-ran glamour on every TextBlock every frame.
//
//   - Inflight blocks (streaming text/thinking, animating compact
//     row) are tracked by pointer, not by index. Pruning blocks
//     ahead of an inflight pointer can't desync state.
//
//   - Gutter glyphs (│, ├─) are applied inside each Block's Render
//     method, not by a transcript-level wrapping pass. The cache
//     holds final form; the transcript only joins blocks with
//     inter-block spacers.
package transcript

import (
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Kind tags a Block so the transcript-level inter-block spacer can
// decide whether to emit a `│` gutter line or a blank. Components
// outside the transcript (yank mode, search) also switch on Kind.
type Kind int

const (
	KindBanner     Kind = iota // welcome box (no timeline gutter)
	KindUserPrompt             // cuts the timeline with a scanline header
	KindText                   // assistant final text / markdown
	KindThinking               // dim reasoning text
	KindTool                   // tool_use_start + optional result
	KindSystem                 // draining / cancelled / iter-limit
	KindError                  // KindError red banner
	KindSynthetic              // pre-formatted block injected by the UI
	KindCompacting             // animated inflight compact row
)

// Range marks a byte offset span inside PlainText() — used by the
// search overlay (M9) to highlight matches. Start is inclusive, End
// exclusive (Go slice convention).
type Range struct {
	Start, End int
}

// RenderOpts carries ephemeral per-frame flags that influence how a
// block draws. Default (zero-value) means "normal scrollback render
// with no decoration".
//
//   - Focused: yank-mode (M8) has selected this block. Render adds
//     a left-edge accent so the user can see what would be copied.
//   - Highlights: search (M9) match positions inside PlainText().
//     Render decorates those byte ranges with a highlight style.
type RenderOpts struct {
	Focused    bool
	Highlights []Range
}

// optsRev hashes RenderOpts into a uint64 so the cache key can
// detect any change. Stable across builds; collision-safe enough for
// per-block cache invalidation (worst case: a missed cache hit, not
// a wrong render). Default opts (Focused=false, no highlights) hash
// to 0 so the common path is fast.
func optsRev(o RenderOpts) uint64 {
	if !o.Focused && len(o.Highlights) == 0 {
		return 0
	}
	var rev uint64
	if o.Focused {
		rev |= 1
	}
	rev |= uint64(len(o.Highlights)) << 8
	for _, r := range o.Highlights {
		// 31-prime rolling hash — same shape Go's strings.hash uses.
		rev = rev*31 + uint64(r.Start)*7 + uint64(r.End)
	}
	return rev
}

// RenderContext carries everything Block.Render needs from the
// transcript layer: terminal width, the live theme, the active
// markdown renderer (nil if not constructed yet), and the per-frame
// opts.
//
// We pass a struct rather than four positional args so future
// additions (e.g. line-number cursor for M9 search jump-to) don't
// break every Block implementation.
type RenderContext struct {
	Width    int
	Theme    *theme.Theme
	Markdown *Markdown
	Opts     RenderOpts
}

// mdRev returns the markdown renderer's revision, or 0 when no
// renderer is attached. The cache uses this to invalidate TextBlocks
// when the renderer is rebuilt (resize → new glamour instance →
// different wrap width → cached output is stale).
func mdRev(m *Markdown) uint64 {
	if m == nil {
		return 0
	}
	return m.Rev()
}

// Block is the unit the transcript stores. Implementations live in
// blocks_*.go. The interface is deliberately small: an ID for cache
// keying, a Rev for cache invalidation, a Kind tag, a PlainText
// extractor, and a Render method.
//
// Concrete blocks are always passed by pointer — mutation (Append
// during streaming, SetResult for tool pairing, SetFrame for
// compact animation) bumps Rev, and a value-receiver would lose
// those mutations.
type Block interface {
	ID() uint64
	Rev() uint64
	Kind() Kind
	PlainText() string
	Render(ctx RenderContext) string
}

// nextBlockID is the package-level monotonic counter that gives each
// Block a stable identity. The transcript cache keys on Block.ID(),
// so two blocks with the same content but different positions can't
// share a cache entry.
//
// Wraps to zero after 2^64 calls — long after the heat death of any
// terminal session.
var nextBlockID uint64

// allocID returns the next monotonic block ID. Called by the
// constructor in each blocks_*.go file. Not exposed outside the
// package — Block implementations are the only callers.
func allocID() uint64 {
	nextBlockID++
	return nextBlockID
}
