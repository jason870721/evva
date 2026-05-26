package lp

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Black + gold palette. Gold is the only saturated accent — reserved for
// prompts, active state, headers, and key labels. Everything else is warm
// grey; red appears for faults only. Low profile by construction: no neon,
// no second accent hue competing for attention.
const (
	gold    lipgloss.Color = "#D4AF37" // metallic gold — the one accent
	goldDim lipgloss.Color = "#9C7A3C" // muted gold — secondary accent / gutters
	fg      lipgloss.Color = "#D8D2C4" // warm parchment — primary text
	muted   lipgloss.Color = "#8A8576" // warm grey — chrome, hints, metadata
	faint   lipgloss.Color = "#5A564C" // dim — rules, gutters, rails
	sage    lipgloss.Color = "#A3B18A" // muted sage — tool success (distinct from gold)
	red     lipgloss.Color = "#C85A54" // brick red — faults only
	white   lipgloss.Color = "#F2EEE6" // warm white — text on solid diff blocks

	diffAddBg    lipgloss.Color = "#16210F" // dark olive
	diffRemoveBg lipgloss.Color = "#2A1512" // dark maroon
)

// goldTheme builds the black+gold *theme.Theme that lp passes to every
// reused bubbletea renderer. It sets every EXPORTED style field the
// renderers read; the theme's private glyph/spinner maps stay nil, which
// is safe because lp never calls Glyph()/SpinnerStyle() (its status line
// and panels own their own gold glyphs).
func goldTheme() *theme.Theme {
	t := &theme.Theme{Rev: 1}

	// Chat blocks
	t.UserPrompt = lipgloss.NewStyle().Foreground(gold).Bold(true)
	t.Assistant = lipgloss.NewStyle().Foreground(fg)
	t.Thinking = lipgloss.NewStyle().Foreground(faint).Italic(true)

	// Tools — gold-dim call, sage success, red for real failures only.
	t.ToolCall = lipgloss.NewStyle().Foreground(goldDim).Bold(true)
	t.ToolOK = lipgloss.NewStyle().Foreground(sage).Bold(true)
	t.ToolErr = lipgloss.NewStyle().Foreground(red).Bold(true)
	t.ToolResult = lipgloss.NewStyle().Foreground(muted)

	// Diff — warm white on solid dark olive / maroon. Restrained, readable.
	t.DiffAdd = lipgloss.NewStyle().Foreground(white).Background(diffAddBg)
	t.DiffRemove = lipgloss.NewStyle().Foreground(white).Background(diffRemoveBg)
	t.DiffContext = lipgloss.NewStyle().Foreground(muted)
	t.DiffHeader = lipgloss.NewStyle().Foreground(goldDim).Italic(true)

	// Side panels
	t.PanelHeader = lipgloss.NewStyle().Foreground(gold).Bold(true)
	t.PanelRow = lipgloss.NewStyle().Foreground(fg)
	t.PanelBorder = lipgloss.NewStyle().Foreground(faint)

	// Status surfaces (lp owns its own status line, but overlays read these)
	t.StatusBar = lipgloss.NewStyle().Foreground(fg)
	t.StatusKey = lipgloss.NewStyle().Foreground(muted)
	t.StatusValue = lipgloss.NewStyle().Foreground(fg).Bold(true)
	t.StatusSep = lipgloss.NewStyle().Foreground(faint)
	t.StatePill = lipgloss.NewStyle().Bold(true)

	// Errors / input / dim. Input frame is a thin bottom rule (underline),
	// not a box — the low-profile signature.
	t.ErrorBanner = lipgloss.NewStyle().Foreground(red).Bold(true)
	// Borderless input — the App draws a single thin rule above it, so the
	// prompt reads as a clean command line (the low-profile signature). Left
	// pad aligns the ❯ with the status line and transcript.
	t.InputBorder = lipgloss.NewStyle().PaddingLeft(1)
	t.DimText = lipgloss.NewStyle().Foreground(muted)

	// Banner styles are unused (lp shows no banner) but set so nothing reads
	// a zero-value style if a reused path touches them.
	t.Banner = lipgloss.NewStyle().Foreground(gold)
	t.BannerBox = lipgloss.NewStyle()
	t.BannerInfo = lipgloss.NewStyle().Foreground(goldDim)
	t.Greeting = lipgloss.NewStyle().Foreground(gold).Italic(true)

	// Lifecycle
	t.Compacting = lipgloss.NewStyle().Foreground(goldDim).Bold(true)
	t.Draining = lipgloss.NewStyle().Foreground(goldDim).Bold(true)
	t.FooterHint = lipgloss.NewStyle().Foreground(muted)
	t.TurnSep = lipgloss.NewStyle().Foreground(faint)

	// Tasks / paste / timeline
	t.TasksDone = lipgloss.NewStyle().Foreground(sage).Bold(true)
	t.PasteChip = lipgloss.NewStyle().Foreground(goldDim).Italic(true)
	t.Timeline = lipgloss.NewStyle().Foreground(faint)
	t.TimelineCut = lipgloss.NewStyle().Foreground(goldDim).Bold(true)
	t.TimelineAccent = lipgloss.NewStyle().Foreground(gold).Bold(true)
	t.TimelineMatch = lipgloss.NewStyle().Foreground(gold).Bold(true)

	// Context meter
	t.ContextBar = lipgloss.NewStyle().Foreground(muted)
	t.ContextFill = lipgloss.NewStyle().Foreground(gold).Bold(true)
	t.ContextRail = lipgloss.NewStyle().Foreground(faint)

	return t
}
