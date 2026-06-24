package overlays

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// CheckpointRestoredMsg signals a completed /rewind. The App re-renders the
// transcript when the restore touched the conversation (mode chat/both) and
// surfaces the summary as a hint.
type CheckpointRestoredMsg struct {
	Summary     string
	ChangedChat bool // conversation was rewound — the transcript must reload
}

const (
	rewindPageSize   = 10
	rewindPreviewMax = 150
)

type rewindPhase int

const (
	phaseList    rewindPhase = iota // choosing a checkpoint
	phaseMode                       // choosing code / chat / both
	phaseConfirm                    // confirming a destructive (code) restore
)

// Rewind is the /rewind overlay. It walks three phases: pick a checkpoint
// (newest first, paginated like /resume), pick a restore mode, then — for any
// mode that rewrites files — confirm before applying. A code restore overwrites
// the working tree, so it never fires without that confirm step.
type Rewind struct {
	ctrl    ui.Controller
	rows    []ui.CheckpointInfo
	page    int
	sel     int
	phase   rewindPhase
	chosen  ui.CheckpointInfo
	modes   []string
	modeSel int
	errMsg  string
}

// NewRewind opens the picker, loading the checkpoint list synchronously (the
// metadata reads are cheap).
func NewRewind(ctrl ui.Controller) *Rewind {
	if ctrl == nil {
		return nil
	}
	return &Rewind{ctrl: ctrl, rows: ctrl.Checkpoints()}
}

func (r *Rewind) Key() string { return "rewind" }
func (r *Rewind) Modal() bool { return true }
func (r *Rewind) Hint() string {
	switch r.phase {
	case phaseMode:
		return "[↑↓] mode · [Enter] select · [Esc] back"
	case phaseConfirm:
		return "[y] confirm · [n/Esc] back"
	default:
		if r.pageCount() > 1 {
			return "[↑↓] cursor · [←→] page · [Enter] choose · [Esc] cancel"
		}
		return "[↑↓] cursor · [Enter] choose · [Esc] cancel"
	}
}

func (r *Rewind) pageCount() int {
	if len(r.rows) == 0 {
		return 1
	}
	return (len(r.rows) + rewindPageSize - 1) / rewindPageSize
}

func (r *Rewind) pageRows() []ui.CheckpointInfo {
	if len(r.rows) == 0 {
		return nil
	}
	start := r.page * rewindPageSize
	if start >= len(r.rows) {
		return nil
	}
	end := min(start+rewindPageSize, len(r.rows))
	return r.rows[start:end]
}

// rewindModesFor returns the restore modes offered for c. When a compaction
// since the checkpoint invalidated its conversation cut-point, only code
// restore is offered (rewind PRD §5.2).
func rewindModesFor(c ui.CheckpointInfo) []string {
	if c.ChatRestoreOK {
		return []string{"both", "code", "chat"}
	}
	return []string{"code"}
}

func (r *Rewind) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch r.phase {
	case phaseMode:
		return r.updateMode(key)
	case phaseConfirm:
		return r.updateConfirm(key)
	default:
		return r.updateList(key)
	}
}

func (r *Rewind) updateList(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "esc", "ctrl+c":
		return true, nil
	case "up", "k":
		if r.sel > 0 {
			r.sel--
		}
		return false, nil
	case "down", "j":
		if r.sel < len(r.pageRows())-1 {
			r.sel++
		}
		return false, nil
	case "left", "h":
		if r.page > 0 {
			r.page--
			r.sel = 0
		}
		return false, nil
	case "right", "l":
		if r.page < r.pageCount()-1 {
			r.page++
			r.sel = 0
		}
		return false, nil
	case "enter":
		page := r.pageRows()
		if len(page) == 0 {
			return true, nil
		}
		r.chosen = page[r.sel]
		r.modes = rewindModesFor(r.chosen)
		r.modeSel = 0
		r.errMsg = ""
		r.phase = phaseMode
		return false, nil
	}
	return false, nil
}

func (r *Rewind) updateMode(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "esc":
		r.phase = phaseList
		r.errMsg = ""
		return false, nil
	case "ctrl+c":
		return true, nil
	case "up", "k":
		if r.modeSel > 0 {
			r.modeSel--
		}
		return false, nil
	case "down", "j":
		if r.modeSel < len(r.modes)-1 {
			r.modeSel++
		}
		return false, nil
	case "enter":
		// A chat-only rewind doesn't touch the working tree, so it skips the
		// confirm. Any mode that includes code requires the confirm step.
		if r.modes[r.modeSel] == "chat" {
			return r.execute("chat")
		}
		r.phase = phaseConfirm
		return false, nil
	}
	return false, nil
}

func (r *Rewind) updateConfirm(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "y", "Y", "enter":
		return r.execute(r.modes[r.modeSel])
	case "n", "N", "esc":
		r.phase = phaseMode
		return false, nil
	case "ctrl+c":
		return true, nil
	}
	return false, nil
}

func (r *Rewind) execute(mode string) (bool, tea.Cmd) {
	summary, err := r.ctrl.RestoreCheckpoint(r.chosen.ID, mode)
	if err != nil {
		r.errMsg = err.Error()
		r.phase = phaseMode
		return false, nil
	}
	changedChat := mode == "chat" || mode == "both"
	return true, func() tea.Msg {
		return CheckpointRestoredMsg{Summary: summary, ChangedChat: changedChat}
	}
}

func (r *Rewind) View(width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /REWIND"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"Time-travel undo — restore files, the conversation, or both to a prior turn. " +
			"A code restore overwrites your working tree.",
	))
	b.WriteString("\n\n")

	if len(r.rows) == 0 {
		b.WriteString(th.DimText.Render("  (no checkpoints in this session)"))
		b.WriteByte('\n')
		b.WriteString(th.DimText.Render("  checkpoint/rewind is opt-in — enable with enable_checkpoints: true"))
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(th.FooterHint.Render("[Esc] cancel"))
		return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
	}

	switch r.phase {
	case phaseMode:
		r.viewMode(&b, th)
	case phaseConfirm:
		r.viewConfirm(&b, th)
	default:
		r.viewList(&b, th)
	}

	if r.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + r.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render(r.Hint()))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

func (r *Rewind) viewList(b *strings.Builder, th *theme.Theme) {
	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	for i, row := range r.pageRows() {
		marker := "  "
		style := dim
		if i == r.sel {
			marker = "▶ "
			style = sel
		}
		chat := "conversation ✓"
		if !row.ChatRestoreOK {
			chat = "conversation ✗ (compacted)"
		}
		meta := fmt.Sprintf("%s · %d file(s) · %s", relativeTime(row.CreatedAt), row.FileCount, chat)
		b.WriteString(style.Render(marker + clipPreview(row.PromptPreview)))
		b.WriteByte('\n')
		b.WriteString(dim.Render("    " + meta))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if pc := r.pageCount(); pc > 1 {
		b.WriteString(dim.Render(fmt.Sprintf("page %d / %d", r.page+1, pc)))
		b.WriteByte('\n')
	}
}

func (r *Rewind) viewMode(b *strings.Builder, th *theme.Theme) {
	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	b.WriteString(dim.Render("Checkpoint: " + clipPreview(r.chosen.PromptPreview)))
	b.WriteString("\n\n")
	for i, m := range r.modes {
		marker := "  "
		style := dim
		if i == r.modeSel {
			marker = "▶ "
			style = sel
		}
		b.WriteString(style.Render(marker + rewindModeLabel(m)))
		b.WriteByte('\n')
	}
	if !r.chosen.ChatRestoreOK {
		b.WriteByte('\n')
		b.WriteString(dim.Render("conversation rewind unavailable — the session was compacted after this checkpoint"))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func (r *Rewind) viewConfirm(b *strings.Builder, th *theme.Theme) {
	mode := r.modes[r.modeSel]
	b.WriteString(th.DimText.Render("Checkpoint: " + clipPreview(r.chosen.PromptPreview)))
	b.WriteString("\n\n")
	b.WriteString(th.ErrorBanner.Render(
		fmt.Sprintf("⚠ restore %q will OVERWRITE files in your working tree", mode)))
	b.WriteString("\n\n")
	b.WriteString(th.DimText.Render(
		fmt.Sprintf("Restore %d captured file(s) to their state before this turn?", r.chosen.FileCount)))
	b.WriteByte('\n')
}

func rewindModeLabel(m string) string {
	switch m {
	case "both":
		return "both — restore files and rewind the conversation"
	case "code":
		return "code — restore files only (overwrites the working tree)"
	case "chat":
		return "chat — rewind the conversation only"
	}
	return m
}

func clipPreview(s string) string {
	if s == "" {
		return "(no prompt)"
	}
	if len(s) > rewindPreviewMax {
		return s[:rewindPreviewMax] + "…"
	}
	return s
}
