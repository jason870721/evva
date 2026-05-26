package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/status"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// StatusLine is lp's slim top instrument line: brand · state · model · cwd on
// the left, tokens · context · mode on the right, padded to width. It reuses
// the bubbletea run-state machine (status.State) and the public spinner
// frames, but renders its own low-profile gold layout — no diamonds, no
// bottom HUD. Accent colors are read off the theme's exported style fields
// so the palette stays in lp/theme.go.
type StatusLine struct {
	state *status.State

	usage    llm.Usage
	ctxUsed  int
	ctxLimit int
	model    string
	effort   string
	permMode string
	workdir  string
}

// NewStatusLine binds the line to lp's run-state machine and captures the
// working directory once at construction.
func NewStatusLine(state *status.State) *StatusLine {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	return &StatusLine{state: state, workdir: wd}
}

func (s *StatusLine) SetUsage(u llm.Usage)       { s.usage = u }
func (s *StatusLine) SetContext(used, limit int) { s.ctxUsed, s.ctxLimit = used, limit }
func (s *StatusLine) SetModel(m string)          { s.model = m }
func (s *StatusLine) SetEffort(e string)         { s.effort = e }
func (s *StatusLine) SetPermissionMode(m string) { s.permMode = m }

// Render composes the one-line HUD, padded to width. When the terminal is
// too narrow for the right cluster, only the left cluster is shown.
func (s *StatusLine) Render(width int, th *theme.Theme) string {
	if width <= 0 {
		return ""
	}
	sep := th.StatusSep.Render(" · ")

	left := " " + th.UserPrompt.Render("lp") + sep + renderState(s.state, th) + sep +
		th.StatusValue.Render(modelOrDash(s.model)) + effortSuffix(s.effort, th)
	if s.workdir != "" {
		left += sep + th.DimText.Render(shortPath(s.workdir))
	}

	right := th.StatusKey.Render("IN ") + th.StatusValue.Render(humanTokens(s.usage.InputTokens)) +
		th.StatusKey.Render(" OUT ") + th.StatusValue.Render(humanTokens(s.usage.OutputTokens)) +
		sep + ctxCell(s.ctxUsed, s.ctxLimit, th)
	if cell := modeCell(s.permMode, th); cell != "" {
		right += sep + cell
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

// renderState builds the state cell: an animated braille spinner (active
// states) or a static glyph, plus the uppercase label, in the state's accent.
func renderState(st *status.State, th *theme.Theme) string {
	cur := st.Current()
	style := lipgloss.NewStyle().Foreground(stateColor(cur, th)).Bold(true)
	var glyph string
	switch {
	case cur.IsActive():
		glyph = theme.SpinnerFrame(st.Frame())
	case cur == status.StateError:
		glyph = "✗"
	case cur == status.StateIterLimit:
		glyph = "⏸"
	default:
		glyph = "●"
	}
	return style.Render(glyph + " " + strings.ToUpper(cur.String()))
}

// stateColor derives the accent for a run-state from the theme's exported
// style fields (the palette itself is private to the theme package).
func stateColor(s status.RunState, th *theme.Theme) lipgloss.TerminalColor {
	switch s {
	case status.StateRunning, status.StateTexting:
		return th.ContextFill.GetForeground() // gold
	case status.StateThinking:
		return th.Thinking.GetForeground() // faint
	case status.StateExecuting:
		return th.ToolCall.GetForeground() // gold-dim
	case status.StateDraining, status.StateCompacting, status.StateIterLimit:
		return th.Compacting.GetForeground() // gold-dim
	case status.StateError:
		return th.ErrorBanner.GetForeground() // red
	default:
		return th.TasksDone.GetForeground() // sage (ready)
	}
}

func ctxCell(used, limit int, th *theme.Theme) string {
	pct := 0.0
	if limit > 0 {
		pct = float64(used) * 100 / float64(limit)
		if pct > 100 {
			pct = 100
		}
	}
	return th.StatusKey.Render("ctx ") + th.StatusValue.Render(fmt.Sprintf("%.0f%%", pct))
}

func effortSuffix(level string, th *theme.Theme) string {
	if level == "" {
		return ""
	}
	return th.StatusKey.Render(" ") + th.DimText.Render(level)
}

func modeCell(mode string, th *theme.Theme) string {
	if mode == "" || mode == "default" {
		return ""
	}
	return th.StatusKey.Render("⛨ ") + th.StatusValue.Render(mode)
}

func modelOrDash(m string) string {
	if m == "" {
		return "-"
	}
	return m
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// humanTokens formats a token count with a k/M suffix once it crosses the
// threshold, keeping the line tight on long sessions.
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
