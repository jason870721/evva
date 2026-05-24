package overlays

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/transcript"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// SearchRevealMsg signals the App that the search cursor has moved
// to a new block. The App's handler calls
// view.RevealBlock(BlockID) so the viewport scrolls to it.
type SearchRevealMsg struct {
	BlockID uint64
}

// Search is the Ctrl+F transcript-search overlay. Visually it
// renders a compact one-line input + status row above the input
// box. Modal — consumes every key while open.
//
// Navigation:
//   - typing       : re-scans transcript on every change
//   - Enter / n    : next match (wraps)
//   - shift+n / p  : previous match (wraps)
//   - Esc / q      : close (clears search highlight)
//   - Ctrl+C       : close + App quits
//
// Search is case-insensitive. Empty query clears the highlight.
type Search struct {
	tr      *transcript.Transcript
	input   textinput.Model
	matches []uint64 // ordered block IDs containing matches
	cursor  int      // index into matches; -1 when empty
}

// NewSearch constructs the overlay. tr is the transcript whose
// blocks will be scanned; the overlay holds a reference so it can
// call SetSearchMatches and MatchedBlocks.
func NewSearch(tr *transcript.Transcript) *Search {
	if tr == nil {
		return nil
	}
	ti := textinput.New()
	ti.Placeholder = "search transcript…"
	ti.Prompt = "/ "
	ti.CharLimit = 0
	ti.Focus()
	return &Search{tr: tr, input: ti, cursor: -1}
}

func (s *Search) Key() string { return "search" }
func (s *Search) Modal() bool { return true }

// Hint renders the position + key-map row.
func (s *Search) Hint() string {
	keys := "Enter/n next · shift+n prev · Esc close"
	if s.input.Value() == "" {
		return "search · " + keys
	}
	if len(s.matches) == 0 {
		return "search · no matches · " + keys
	}
	return fmt.Sprintf("search · %d/%d matches · %s", s.cursor+1, len(s.matches), keys)
}

// Update consumes keys while the overlay is on top. Esc/q close
// the overlay AND clear the transcript-side search marker (so the
// gutter accent goes away).
func (s *Search) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return false, nil
	}
	switch key.String() {
	case "esc":
		s.tr.SetSearchMatches(nil)
		return true, nil
	case "ctrl+c":
		s.tr.SetSearchMatches(nil)
		return true, nil
	case "enter", "ctrl+n":
		return false, s.advance(+1)
	case "shift+n", "ctrl+p":
		return false, s.advance(-1)
	}

	// Pre-rescan input value so we can detect change.
	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() != before {
		s.rescan()
		// Auto-reveal the first match on first keystroke that
		// produces one — gives the user immediate visual feedback.
		if rev := s.currentBlockID(); rev != 0 {
			cmd = tea.Batch(cmd, s.reveal(rev))
		}
	}
	return false, cmd
}

// rescan recomputes the per-block match index from the current
// query and pushes it to the transcript. Empty queries clear the
// matches.
func (s *Search) rescan() {
	q := strings.ToLower(strings.TrimSpace(s.input.Value()))
	if q == "" {
		s.tr.SetSearchMatches(nil)
		s.matches = nil
		s.cursor = -1
		return
	}
	hits := map[uint64][]transcript.Range{}
	for _, b := range s.tr.Blocks() {
		plain := strings.ToLower(b.PlainText())
		ranges := findAllRanges(plain, q)
		if len(ranges) > 0 {
			hits[b.ID()] = ranges
		}
	}
	s.tr.SetSearchMatches(hits)
	s.matches = s.tr.MatchedBlocks()
	if len(s.matches) == 0 {
		s.cursor = -1
	} else {
		s.cursor = 0
	}
}

// findAllRanges returns every non-overlapping match position of q
// inside haystack as byte ranges. q must be non-empty.
func findAllRanges(haystack, q string) []transcript.Range {
	if q == "" {
		return nil
	}
	var out []transcript.Range
	i := 0
	for {
		j := strings.Index(haystack[i:], q)
		if j < 0 {
			return out
		}
		out = append(out, transcript.Range{Start: i + j, End: i + j + len(q)})
		i += j + len(q)
	}
}

// advance moves the match cursor by delta (+1 / -1), wrapping at
// the ends. Returns the reveal cmd for the new target.
func (s *Search) advance(delta int) tea.Cmd {
	if len(s.matches) == 0 {
		return nil
	}
	next := s.cursor + delta
	if next < 0 {
		next = len(s.matches) - 1
	}
	if next >= len(s.matches) {
		next = 0
	}
	s.cursor = next
	return s.reveal(s.matches[s.cursor])
}

// currentBlockID returns the Block.ID() at the cursor, or 0 when
// no matches.
func (s *Search) currentBlockID() uint64 {
	if s.cursor < 0 || s.cursor >= len(s.matches) {
		return 0
	}
	return s.matches[s.cursor]
}

// reveal returns a Cmd that emits SearchRevealMsg so the App's
// view-side handler scrolls the viewport.
func (s *Search) reveal(id uint64) tea.Cmd {
	return func() tea.Msg { return SearchRevealMsg{BlockID: id} }
}

// View renders the search input. width is the available column
// count; the panel uses an inset margin so the bordered box sits
// inside the layout slot.
func (s *Search) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}
	s.input.Width = innerWidth - 4

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ SEARCH"))
	b.WriteByte('\n')
	b.WriteString(s.input.View())
	b.WriteByte('\n')
	if s.input.Value() != "" {
		if len(s.matches) == 0 {
			b.WriteString(th.DimText.Render("no matches"))
		} else {
			b.WriteString(th.DimText.Render(fmt.Sprintf("match %d of %d", s.cursor+1, len(s.matches))))
		}
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[Enter] next · [shift+n] prev · [Esc] close"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// BlinkCmd is the cursor-blink tick for the search input. The App
// batches it with its Init / overlay-open path so the cursor
// animates from the first frame.
func (s *Search) BlinkCmd() tea.Cmd { return textinput.Blink }
