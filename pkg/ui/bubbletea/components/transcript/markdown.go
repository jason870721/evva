package transcript

import (
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/glamour"
)

// Markdown wraps glamour with a width-aware constructor so the
// transcript can re-render TextBlock content when the terminal
// resizes.
//
// glamour's TermRenderer parses its style once at construction, so
// we cache one per width — recreate-on-resize keeps line wrapping in
// sync with the viewport without re-parsing styles on every chunk.
//
// Rev is bumped each time a new instance is constructed; the
// transcript's block cache keys on it so a width change invalidates
// every TextBlock without per-block bookkeeping. Atomic so the rev
// is safe to read from any goroutine, though in practice only the
// bubbletea main loop touches it.
type Markdown struct {
	width int
	term  *glamour.TermRenderer
	rev   uint64
}

// markdownRevCounter is a package-level monotonic counter that
// supplies a fresh Rev value each time NewMarkdown is called. Using
// a counter rather than a hash-of-width keeps successive resizes to
// the same width invalidating the cache (the previous renderer
// instance is gone; even if width is identical the renderer object
// changed, so caller expects a re-render).
var markdownRevCounter uint64

// NewMarkdown builds a renderer keyed to width. Returns a non-nil
// *Markdown even when glamour init fails — Render falls back to
// passthrough so callers don't need to nil-check.
//
// width < 20 is clamped to 20 — too narrow to wrap usefully, and
// glamour throws on tiny widths.
func NewMarkdown(width int) *Markdown {
	if width < 20 {
		width = 20
	}
	rev := atomic.AddUint64(&markdownRevCounter, 1)
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err != nil {
		return &Markdown{width: width, rev: rev}
	}
	return &Markdown{width: width, term: r, rev: rev}
}

// Width reports the column count this renderer was built for.
// Transcript uses this to decide whether to rebuild on resize.
func (m *Markdown) Width() int { return m.width }

// Rev is the cache-invalidation token. Bumped per construction.
func (m *Markdown) Rev() uint64 { return m.rev }

// Render returns the markdown-rendered version of s, falling back to
// the raw text if glamour is unavailable or the render errors.
// Trailing newlines are trimmed so blocks join cleanly in the
// transcript.
func (m *Markdown) Render(s string) string {
	if m == nil || m.term == nil {
		return s
	}
	out, err := m.term.Render(s)
	if err != nil {
		return s
	}
	return strings.TrimRight(out, "\n")
}
