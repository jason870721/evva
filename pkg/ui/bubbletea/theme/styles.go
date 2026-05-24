// Package theme owns every styled surface in the v2 TUI. One Theme
// instance is constructed at startup (via Default) and passed by
// pointer to every component that renders. Components read styles off
// the struct rather than rebuilding them per frame.
//
// The Rev field is the cache key for the transcript block cache (M3):
// swap the theme, bump Rev, and every cached block re-renders with
// the new styles. Components never compare Theme pointers — they
// compare Rev values.
package theme

import "github.com/charmbracelet/lipgloss"

// Theme bundles every lipgloss style the v2 TUI needs. Held by
// pointer; mutations through theme-swap should produce a new *Theme
// with a higher Rev, never mutate fields in place.
type Theme struct {
	// Rev is bumped each time the theme is swapped wholesale. Block
	// caches key off this so a theme swap forces a re-render. Default
	// theme starts at Rev = 1 so zero-value Rev (= "uninitialised")
	// stays a discriminator.
	Rev uint64

	// Chat blocks
	UserPrompt lipgloss.Style
	Assistant  lipgloss.Style
	Thinking   lipgloss.Style

	// Tool vocabulary — brown call, green success, RED reserved for
	// real failures only. ToolResult uses soft sky blue so it sits
	// beneath the brown call line as a calm output stream.
	ToolCall   lipgloss.Style
	ToolOK     lipgloss.Style
	ToolErr    lipgloss.Style
	ToolResult lipgloss.Style

	// Diff — M2 change: white text on solid acid-green / glitch-red
	// backgrounds (was neon-fg on pastel-bg in v1). Reads as a filled
	// bar across the transcript column; matches the "pro" diff look
	// the user requested. Fill spans full transcript width via per-row
	// Width() at render time (see components/diff).
	DiffAdd     lipgloss.Style
	DiffRemove  lipgloss.Style
	DiffContext lipgloss.Style
	DiffHeader  lipgloss.Style

	// Side panels — cyan headers, neutral content.
	PanelHeader lipgloss.Style
	PanelRow    lipgloss.Style
	PanelBorder lipgloss.Style

	// Status bar — neon HUD, violet separator.
	StatusBar   lipgloss.Style
	StatusKey   lipgloss.Style
	StatusValue lipgloss.Style
	StatusSep   lipgloss.Style
	StatePill   lipgloss.Style

	// Errors / input / dims — input border cyan.
	ErrorBanner lipgloss.Style
	InputBorder lipgloss.Style
	DimText     lipgloss.Style

	// Welcome banner — cyan ASCII art, violet double-border.
	Banner     lipgloss.Style
	BannerBox  lipgloss.Style
	BannerInfo lipgloss.Style
	Greeting   lipgloss.Style

	// System lifecycle blocks.
	Compacting lipgloss.Style
	Draining   lipgloss.Style
	FooterHint lipgloss.Style

	// Tasks / paste / timeline — timeline sits back as muted grey so
	// every transcript line doesn't read as an error.
	TasksDone      lipgloss.Style
	PasteChip      lipgloss.Style
	Timeline       lipgloss.Style
	TimelineCut    lipgloss.Style
	TimelineAccent lipgloss.Style // cyan-bold variant for yank-mode focus
	TimelineMatch  lipgloss.Style // yellow-bold variant for search-match blocks

	// Context HUD (status-bar context bar).
	ContextBar  lipgloss.Style
	ContextFill lipgloss.Style
	ContextRail lipgloss.Style

	// Turn separator — barely-there chrome between agent loop iterations.
	TurnSep lipgloss.Style

	// Spinner color overrides — paired with status-keyed lookup via
	// SpinnerStyle() so panels and the status bar agree on which
	// state animates in which color.
	spinThink  lipgloss.Style
	spinYellow lipgloss.Style
	spinExec   lipgloss.Style
	spinPurple lipgloss.Style
	spinCyan   lipgloss.Style

	// activeSpinners maps lifecycle status string → spinner style.
	// Populated by Default(); read by SpinnerStyle().
	activeSpinners map[string]lipgloss.Style

	// glyphs maps non-animated lifecycle status → static glyph.
	// Populated by Default(); read by Glyph().
	glyphs map[string]Glyph
}

// Default returns the production NEON TOKYO theme. Construct once at
// program start and reuse for the session — Rev tracks identity for
// cache invalidation.
func Default() *Theme {
	t := &Theme{Rev: 1}

	// Chat blocks
	t.UserPrompt = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	t.Assistant = lipgloss.NewStyle().Foreground(fg)
	t.Thinking = lipgloss.NewStyle().Foreground(think).Italic(true)

	// Tools
	t.ToolCall = lipgloss.NewStyle().Foreground(brown).Bold(true)
	t.ToolOK = lipgloss.NewStyle().Foreground(green).Bold(true)
	t.ToolErr = lipgloss.NewStyle().Foreground(red).Bold(true)
	t.ToolResult = lipgloss.NewStyle().Foreground(sky)

	// Diff — white on solid bg (M2 change)
	t.DiffAdd = lipgloss.NewStyle().Foreground(fg).Background(diffAddBg).Bold(true)
	t.DiffRemove = lipgloss.NewStyle().Foreground(fg).Background(diffRemoveBg).Bold(true)
	t.DiffContext = lipgloss.NewStyle().Foreground(muted)
	t.DiffHeader = lipgloss.NewStyle().Foreground(purple).Italic(true)

	// Panels
	t.PanelHeader = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	t.PanelRow = lipgloss.NewStyle().Foreground(fg)
	t.PanelBorder = lipgloss.NewStyle().Foreground(dim)

	// Status bar
	t.StatusBar = lipgloss.NewStyle().Foreground(fg).Background(dim).Padding(0, 1)
	t.StatusKey = lipgloss.NewStyle().Foreground(muted)
	t.StatusValue = lipgloss.NewStyle().Foreground(fg).Bold(true)
	t.StatusSep = lipgloss.NewStyle().Foreground(purple).Bold(true)
	t.StatePill = lipgloss.NewStyle().Bold(true)

	// Errors / input / dims
	t.ErrorBanner = lipgloss.NewStyle().Foreground(red).Bold(true)
	t.InputBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cyan).
		Padding(0, 1)
	t.DimText = lipgloss.NewStyle().Foreground(muted)

	// Banner
	t.Banner = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	t.BannerBox = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(purple).
		Padding(1, 4)
	t.BannerInfo = lipgloss.NewStyle().Foreground(cyan)
	t.Greeting = lipgloss.NewStyle().Foreground(cyan).Bold(true).Italic(true)

	// Lifecycle
	t.Compacting = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	t.Draining = lipgloss.NewStyle().Foreground(purple).Bold(true)
	t.FooterHint = lipgloss.NewStyle().Foreground(muted)
	t.TurnSep = lipgloss.NewStyle().Foreground(think)

	// Tasks / paste / timeline
	t.TasksDone = lipgloss.NewStyle().Foreground(green).Bold(true)
	t.PasteChip = lipgloss.NewStyle().Foreground(purple).Italic(true)
	t.Timeline = lipgloss.NewStyle().Foreground(muted)
	t.TimelineCut = lipgloss.NewStyle().Foreground(purple).Bold(true)
	t.TimelineAccent = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	t.TimelineMatch = lipgloss.NewStyle().Foreground(yellow).Bold(true)

	// Context HUD
	t.ContextBar = lipgloss.NewStyle().Foreground(muted)
	t.ContextFill = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	t.ContextRail = lipgloss.NewStyle().Foreground(muted)

	// Spinner color overrides
	t.spinThink = lipgloss.NewStyle().Foreground(lightBlue).Bold(true)
	t.spinYellow = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	t.spinExec = lipgloss.NewStyle().Foreground(brown).Bold(true)
	t.spinPurple = lipgloss.NewStyle().Foreground(purple).Bold(true)
	t.spinCyan = lipgloss.NewStyle().Foreground(cyan).Bold(true)

	t.activeSpinners = map[string]lipgloss.Style{
		"thinking":    t.spinThink,
		"in_progress": t.spinYellow,
		"executing":   t.spinExec,
		"draining":    t.spinPurple,
		"compacting":  t.spinYellow,
		"texting":     t.spinCyan,
		"saving":      t.spinPurple,
	}

	t.glyphs = map[string]Glyph{
		// task lifecycle
		"pending":     {Symbol: "▢", Color: muted},
		"in_progress": {Symbol: "▶", Color: yellow},
		"completed":   {Symbol: "▣", Color: green},
		"deleted":     {Symbol: "·", Color: dim},

		// subagent lifecycle (mirrors constant.AgentStatus values)
		"init":         {Symbol: "◇", Color: muted},
		"thinking":     {Symbol: "◆", Color: lightBlue},
		"executing":    {Symbol: "▶", Color: brown},
		"draining":     {Symbol: "◈", Color: purple},
		"compacting":   {Symbol: "↻", Color: yellow},
		"ready_report": {Symbol: "✔", Color: green},
		"crushed":      {Symbol: "✘", Color: red},
		"max_iters":    {Symbol: "⊘", Color: yellow},
		"idle":         {Symbol: "✔", Color: green},
		"interrupted":  {Symbol: "✘", Color: red},
		"saving":       {Symbol: "◇", Color: purple},
		"shutdown":     {Symbol: "■", Color: dim},
		"texting":      {Symbol: "◆", Color: cyan},
	}

	return t
}
