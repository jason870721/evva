package overlays

import (
	"fmt"
	"sort"
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

// resumePreviewMax caps the label rendered per row. Snapshot titles and
// prompt previews store up to PreviewMaxBytes (200); the picker shows
// fewer chars so the column stays scannable.
const resumePreviewMax = 150

// resumeRow is one rendered line: a session plus how deep in the fork tree
// it sits. Depth is derived at open time, not stored.
type resumeRow struct {
	info  ui.SessionInfo
	depth int
}

// Resume is the /resume picker overlay: browse, resume, pin, delete.
//
// Sessions are the only evva artifact the operator accumulates without
// ever being asked to, so the picker is also where they are curated. The
// alternative — a second /sessions overlay listing the same rows — would
// have been two surfaces for one job.
type Resume struct {
	ctrl     ui.Controller
	rows     []resumeRow
	warnings []string
	page     int // 0-indexed
	sel      int // cursor index within the current page (0..resumePageSize-1)
	all      bool
	errMsg   string
	// confirmID holds the session a second 'd' would delete. Deletion is
	// unrecoverable and the key is one row away from the cursor keys.
	confirmID string
}

// NewResume opens the picker. Loads the list synchronously — the JSON
// reads are cheap enough that we don't bother with a spinner (a
// machine-wide listing of ~100 sessions measures around 70 ms because
// listing decodes headers, not message bodies).
func NewResume(ctrl ui.Controller) *Resume {
	if ctrl == nil {
		return nil
	}
	r := &Resume{ctrl: ctrl}
	r.reload()
	return r
}

func (r *Resume) reload() {
	var (
		infos    []ui.SessionInfo
		warnings []string
	)
	if r.all {
		infos, warnings = r.ctrl.ListAllSessions()
	} else {
		infos, warnings = r.ctrl.ListSessions()
	}
	r.rows = arrangeForkTree(infos)
	r.warnings = warnings
	r.confirmID = ""
	if r.page >= r.pageCount() {
		r.page = r.pageCount() - 1
	}
	if r.sel >= len(r.pageRows()) {
		r.sel = maxInt(0, len(r.pageRows())-1)
	}
}

// arrangeForkTree orders sessions so a fork sits directly under the parent
// it branched from, indented. Roots keep their newest-first order; so do
// siblings.
//
// A fork whose parent is not in the current view (pruned, or in another
// workdir while the picker is scoped to this one) is rendered as a root —
// an orphan is still worth showing, and hiding it would make the list lie
// about what exists.
func arrangeForkTree(infos []ui.SessionInfo) []resumeRow {
	present := make(map[string]bool, len(infos))
	for _, s := range infos {
		present[s.ID] = true
	}
	children := map[string][]ui.SessionInfo{}
	var roots []ui.SessionInfo
	for _, s := range infos {
		if s.ParentID != "" && present[s.ParentID] {
			children[s.ParentID] = append(children[s.ParentID], s)
			continue
		}
		roots = append(roots, s)
	}
	byRecency := func(v []ui.SessionInfo) {
		sort.SliceStable(v, func(i, j int) bool { return v[i].UpdatedAt > v[j].UpdatedAt })
	}
	byRecency(roots)
	for k := range children {
		byRecency(children[k])
	}

	out := make([]resumeRow, 0, len(infos))
	// Depth-first so a fork of a fork nests. Recursion is safe here: a
	// child's parent always predates it, so the edges cannot form a cycle,
	// and the depth is bounded by how many times a human branched.
	var walk func(s ui.SessionInfo, depth int)
	walk = func(s ui.SessionInfo, depth int) {
		out = append(out, resumeRow{info: s, depth: depth})
		for _, c := range children[s.ID] {
			walk(c, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return out
}

func (r *Resume) Key() string { return "resume" }
func (r *Resume) Modal() bool { return true }
func (r *Resume) Hint() string {
	base := "[↑↓] cursor · [Enter] resume · [p] pin · [d] delete · [a] all workdirs · [Esc] cancel"
	if r.pageCount() > 1 {
		return "[↑↓] cursor · [←→] page · [Enter] resume · [p] pin · [d] delete · [a] all · [Esc] cancel"
	}
	return base
}

func (r *Resume) pageCount() int {
	if len(r.rows) == 0 {
		return 1
	}
	return (len(r.rows) + resumePageSize - 1) / resumePageSize
}

// pageRows returns the current page's slice into r.rows.
func (r *Resume) pageRows() []resumeRow {
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

// current returns the highlighted session, if any.
func (r *Resume) current() (ui.SessionInfo, bool) {
	page := r.pageRows()
	if len(page) == 0 || r.sel >= len(page) {
		return ui.SessionInfo{}, false
	}
	return page[r.sel].info, true
}

// Update consumes keys while on top of the focus stack. Enter resumes
// the selected session; ←/→ page; ↑/↓ move the cursor.
func (r *Resume) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	// Any key other than a second 'd' cancels a pending delete — the
	// confirmation must not survive the operator moving away from the row.
	if k := key.String(); k != "d" {
		r.confirmID = ""
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
	case "a":
		r.all = !r.all
		r.page, r.sel, r.errMsg = 0, 0, ""
		r.reload()
		return false, nil
	case "p":
		chosen, ok := r.current()
		if !ok {
			return false, nil
		}
		if err := r.ctrl.PinSession(chosen.ID, !chosen.Pinned); err != nil {
			r.errMsg = err.Error()
			return false, nil
		}
		r.reload()
		return false, nil
	case "d":
		chosen, ok := r.current()
		if !ok {
			return false, nil
		}
		if r.confirmID != chosen.ID {
			r.confirmID = chosen.ID
			r.errMsg = ""
			return false, nil
		}
		r.confirmID = ""
		if err := r.ctrl.DeleteSession(chosen.ID); err != nil {
			r.errMsg = err.Error()
			return false, nil
		}
		r.reload()
		return false, nil
	case "enter":
		chosen, ok := r.current()
		if !ok {
			return true, nil
		}
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
	scope := "this workdir"
	if r.all {
		scope = "every workdir on this machine"
	}
	b.WriteString(th.DimText.Render(
		"Reload a previous session — " + scope + ", most recent first. " +
			"Resuming clears the live transcript and replaces it with the saved one.",
	))
	b.WriteString("\n\n")

	if len(r.rows) == 0 {
		empty := "  (no saved sessions for this workdir yet — [a] looks everywhere)"
		if r.all {
			empty = "  (no saved sessions on this machine yet)"
		}
		b.WriteString(th.DimText.Render(empty))
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(th.FooterHint.Render("[a] all workdirs · [Esc] cancel"))
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
		indent := strings.Repeat("  ", row.depth)
		if row.depth > 0 {
			indent += "⑂ "
		}
		if row.info.Pinned {
			marker = strings.TrimSuffix(marker, " ") + "📌"
			if i == r.sel {
				marker = "▶📌"
			}
		}
		label := row.info.Label
		if label == "" {
			label = "(no user prompt yet)"
		}
		// Runes, not bytes: a byte cut lands mid-character on any CJK
		// prompt and renders as a replacement glyph.
		if lr := []rune(label); len(lr) > resumePreviewMax {
			label = string(lr[:resumePreviewMax]) + "…"
		}
		when := relativeTime(row.info.UpdatedAt)
		meta := fmt.Sprintf("%s · %s · %d msgs · %s", when, row.info.Profile, row.info.MessageCount, row.info.Model)
		if r.all && row.info.Workdir != "" {
			meta += " · " + row.info.Workdir
		}
		b.WriteString(style.Render(marker + indent + label))
		b.WriteByte('\n')
		b.WriteString(dim.Render("    " + indent + meta))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if pageCount := r.pageCount(); pageCount > 1 {
		b.WriteString(dim.Render(fmt.Sprintf("page %d / %d", r.page+1, pageCount)))
		b.WriteByte('\n')
	}
	if r.confirmID != "" {
		b.WriteString(th.ErrorBanner.Render("press [d] again to delete this session — it cannot be undone"))
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
