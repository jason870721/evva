package overlays

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// SessionResumedMsg signals a successful /resume. The App handles it by
// resetting the transcript (the rehydrated session replaces the live
// one), refreshing the banner, and putting up a "resumed" hint.
type SessionResumedMsg struct {
	ID string
}

// resumePageSize caps each picker page. Mirrors the user's spec: 10/page,
// left/right keys page through, up/down move the cursor within the page.
const resumePageSize = 10

// resumePreviewMax caps the first-user-prompt preview rendered per row.
// Snapshot.FirstUserPrompt stores up to PreviewMaxBytes (200); the
// picker shows fewer chars so the column stays scannable.
const resumePreviewMax = 150

// Resume is the /resume picker overlay. Paginated 10/page; rows are
// sorted by mtime desc (Controller.ListSessions handles the sort).
type Resume struct {
	ctrl       ui.Controller
	rows       []ui.SessionInfo
	warnings   []string
	page       int // 0-indexed
	sel        int // cursor index within the current page (0..resumePageSize-1)
	errMsg     string
}

// NewResume opens the picker. Loads the list synchronously — the JSON
// reads are cheap enough that we don't bother with a spinner.
func NewResume(ctrl ui.Controller) *Resume {
	if ctrl == nil {
		return nil
	}
	rows, warnings := ctrl.ListSessions()
	return &Resume{ctrl: ctrl, rows: rows, warnings: warnings}
}

func (r *Resume) Key() string { return "resume" }
func (r *Resume) Modal() bool { return true }
func (r *Resume) Hint() string {
	if r.pageCount() > 1 {
		return "[↑↓] cursor · [←→] page · [Enter] resume · [Esc] cancel"
	}
	return "[↑↓] cursor · [Enter] resume · [Esc] cancel"
}

func (r *Resume) pageCount() int {
	if len(r.rows) == 0 {
		return 1
	}
	return (len(r.rows) + resumePageSize - 1) / resumePageSize
}

// pageRows returns the current page's slice into r.rows.
func (r *Resume) pageRows() []ui.SessionInfo {
	if len(r.rows) == 0 {
		return nil
	}
	start := r.page * resumePageSize
	if start >= len(r.rows) {
		return nil
	}
	end := start + resumePageSize
	if end > len(r.rows) {
		end = len(r.rows)
	}
	return r.rows[start:end]
}

// Update consumes keys while on top of the focus stack. Enter resumes
// the selected session; ←/→ page; ↑/↓ move the cursor.
func (r *Resume) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return true, nil
	case "up", "k":
		if r.sel > 0 {
			r.sel--
			r.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if r.sel < len(r.pageRows())-1 {
			r.sel++
			r.errMsg = ""
		}
		return false, nil
	case "left", "h":
		if r.page > 0 {
			r.page--
			r.sel = 0
			r.errMsg = ""
		}
		return false, nil
	case "right", "l":
		if r.page < r.pageCount()-1 {
			r.page++
			r.sel = 0
			r.errMsg = ""
		}
		return false, nil
	case "enter":
		page := r.pageRows()
		if len(page) == 0 {
			return true, nil
		}
		chosen := page[r.sel]
		if err := r.ctrl.ResumeSession(chosen.ID); err != nil {
			r.errMsg = err.Error()
			return false, nil
		}
		return true, func() tea.Msg {
			return SessionResumedMsg{ID: chosen.ID}
		}
	}
	return false, nil
}

func (r *Resume) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 40 {
		innerWidth = 40
	}

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /RESUME"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render(
		"Reload a previous session — same workdir only, most recent first. " +
			"Resuming clears the live transcript and replaces it with the saved one.",
	))
	b.WriteString("\n\n")

	if len(r.rows) == 0 {
		b.WriteString(th.DimText.Render("  (no saved sessions for this workdir yet)"))
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(th.FooterHint.Render("[Esc] cancel"))
		return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
	}

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	page := r.pageRows()
	for i, row := range page {
		marker := "  "
		style := dim
		if i == r.sel {
			marker = "▶ "
			style = sel
		}
		when := relativeTime(row.UpdatedAt)
		preview := row.FirstUserPrompt
		if preview == "" {
			preview = "(no user prompt yet)"
		} else if len(preview) > resumePreviewMax {
			preview = preview[:resumePreviewMax] + "…"
		}
		meta := fmt.Sprintf("%s · %s · %d msgs · %s", when, row.Profile, row.MessageCount, row.Model)
		b.WriteString(style.Render(marker + preview))
		b.WriteByte('\n')
		b.WriteString(dim.Render("    " + meta))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if pageCount := r.pageCount(); pageCount > 1 {
		b.WriteString(dim.Render(fmt.Sprintf("page %d / %d", r.page+1, pageCount)))
		b.WriteByte('\n')
	}
	if r.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + r.errMsg))
		b.WriteByte('\n')
	}
	for _, w := range r.warnings {
		b.WriteString(dim.Render("! " + w))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render(r.Hint()))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// relativeTime renders unix-nano timestamps as "5m ago", "3h ago",
// "2d ago", or falls back to the absolute date past one week. The
// resume picker calls this once per visible row — cheap enough.
func relativeTime(unixNano int64) string {
	if unixNano == 0 {
		return "?"
	}
	t := time.Unix(0, unixNano)
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
