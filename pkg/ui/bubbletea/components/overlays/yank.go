package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/transcript"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/mouse"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// YankCursorChangedMsg is dispatched after every navigation inside
// yank mode. The App reads it to update transcript.SetFocusedBlock
// so the cyan-gutter accent moves to the new cursor location.
type YankCursorChangedMsg struct {
	BlockID uint64
}

// Yank is the Ctrl+Y block-yank-mode overlay. Visually it has no
// "panel" — View() returns "" so no overlay slot is consumed; the
// affordance is the cyan gutter on the focused block + the hint
// row over the input.
//
// Navigation:
//   - j / ↓ : next block (newer)
//   - k / ↑ : previous block (older)
//   - g     : jump to first
//   - G     : jump to last (newest)
//   - Enter or c : copy focused block's PlainText to clipboard via OSC52
//   - e     : toggle expand for the focused tool block (per-block,
//             independent of the global Ctrl+O state)
//   - q / Esc : exit yank mode (clears focus accent)
//   - Ctrl+C  : exit and quit (handled by App)
//
// Blocks that don't contribute meaningful copyable content
// (BannerBlock when empty, the in-flight compacting row) are still
// navigable — yank mode doesn't filter them so cursor position
// math stays simple. Their PlainText() returns whatever they have;
// copying an empty string is harmless.
type Yank struct {
	tr     *transcript.Transcript
	cursor int    // index into tr.Blocks()
	last   string // last copy status; rendered in Hint()
}

// NewYank constructs the overlay, starts the cursor at the last
// block (newest content), and installs the focus marker on the
// transcript so the App's next render reflects it.
func NewYank(tr *transcript.Transcript) *Yank {
	if tr == nil {
		return nil
	}
	blocks := tr.Blocks()
	if len(blocks) == 0 {
		return nil
	}
	y := &Yank{tr: tr, cursor: len(blocks) - 1}
	tr.SetFocusedBlock(blocks[y.cursor].ID())
	return y
}

func (y *Yank) Key() string { return "yank" }
func (y *Yank) Modal() bool { return true }

// Hint composes the yank-mode status row: position, last action,
// and the key map. Visible above the status bar while the overlay
// is on top.
func (y *Yank) Hint() string {
	blocks := y.tr.Blocks()
	pos := fmt.Sprintf("yank %d/%d", y.cursor+1, len(blocks))
	keys := "j/k move · g/G first/last · Enter copy · e expand · q exit"
	if y.last != "" {
		return pos + " · " + y.last + " · " + keys
	}
	return pos + " · " + keys
}

// View returns "" — yank mode is a tooltip-style overlay; the
// affordance is the focused-block gutter accent + the Hint() row.
// The App's overlay-slot renderer handles "" by skipping the slot
// entirely.
func (y *Yank) View(width int, th *theme.Theme) string { return "" }

// Update consumes keys while yank mode is on top of the focus
// stack. close==true on q/Esc (and Ctrl+C, though the App
// intercepts that for the quit path).
func (y *Yank) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return false, nil
	}
	blocks := y.tr.Blocks()
	if len(blocks) == 0 {
		return true, nil
	}

	switch key.String() {
	case "esc", "q":
		y.tr.SetFocusedBlock(0)
		return true, nil

	case "ctrl+c":
		y.tr.SetFocusedBlock(0)
		return true, nil

	case "down", "j":
		if y.cursor < len(blocks)-1 {
			y.cursor++
			y.tr.SetFocusedBlock(blocks[y.cursor].ID())
			y.last = ""
			return false, emitCursorChanged(blocks[y.cursor].ID())
		}
		return false, nil

	case "up", "k":
		if y.cursor > 0 {
			y.cursor--
			y.tr.SetFocusedBlock(blocks[y.cursor].ID())
			y.last = ""
			return false, emitCursorChanged(blocks[y.cursor].ID())
		}
		return false, nil

	case "g":
		y.cursor = 0
		y.tr.SetFocusedBlock(blocks[y.cursor].ID())
		y.last = ""
		return false, emitCursorChanged(blocks[y.cursor].ID())

	case "G":
		y.cursor = len(blocks) - 1
		y.tr.SetFocusedBlock(blocks[y.cursor].ID())
		y.last = ""
		return false, emitCursorChanged(blocks[y.cursor].ID())

	case "enter", "c":
		text := strings.TrimSpace(blocks[y.cursor].PlainText())
		if text == "" {
			y.last = "empty"
			return false, nil
		}
		y.last = fmt.Sprintf("copying %d chars…", len(text))
		// The actual write returns a ClipboardMsg that the App
		// surfaces in the status hint. Yank mode stays open so the
		// user can copy another block immediately.
		return false, mouse.Copy(text)

	case "e":
		// Per-block expand toggle — only meaningful for tool
		// blocks. We look up the focused block via Blocks() rather
		// than caching a typed pointer so a re-ingest (which
		// preserves IDs) doesn't dangle.
		if tb, ok := blocks[y.cursor].(*transcript.ToolBlock); ok {
			// SetExpanded takes the absolute next value; the
			// transcript-wide flag isn't observable from here, so
			// we just flip whatever the block last had.
			tb.SetExpanded(!tb.Expanded())
			y.last = "toggled"
		}
		return false, nil
	}
	return false, nil
}

// emitCursorChanged returns a tea.Cmd that emits a
// YankCursorChangedMsg for the given block. The App's handler is
// responsible for calling view.MarkDirty so the cyan-gutter accent
// repaints; piping through a message keeps Update side-effect-free
// in the overlay package.
func emitCursorChanged(id uint64) tea.Cmd {
	return func() tea.Msg {
		return YankCursorChangedMsg{BlockID: id}
	}
}
