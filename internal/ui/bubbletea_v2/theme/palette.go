package theme

import "github.com/charmbracelet/lipgloss"

// NEON TOKYO palette — 24-bit truecolor. Primary chrome is electric
// cyan + cool grey + electric violet; red is reserved exclusively for
// real fault signals (tool errors, removed diff lines, error banners).
// Truecolor is unconditional — evva targets modern terminals.
//
// One change vs. v1: `white` is now true `#FFFFFF` so the new diff
// styling (white text on solid green / red) reads with maximum
// contrast. v1 had `paletteWhite` aliased to the cool fog white
// (#E2E2FF), which doubled as the primary fg; v2 keeps that fog white
// as `fg` and reserves `white` for the diff-on-solid-block use case.
const (
	// Surface
	bg  lipgloss.Color = "#0A0A14" // abyssal navy — terminal bg
	fg  lipgloss.Color = "#E2E2FF" // cool fog white — primary text
	dim lipgloss.Color = "#1B1B2F" // panel dim

	// Cool grey scale — chrome that should sit back, not shout.
	muted lipgloss.Color = "#7A7E94" // dim chrome / gutter
	think lipgloss.Color = "#5E627A" // thinking block — quiet aside

	// Primary accents (duotone — cyan-led, violet-supported)
	cyan   lipgloss.Color = "#05D9E8" // electric cyan — primary chrome
	purple lipgloss.Color = "#B967FF" // electric violet — secondary accent

	// Supporting hues — used only for the lifecycle vocabulary they
	// represent, never as plain chrome.
	brown     lipgloss.Color = "#B87333" // copper brown — tool calls / executing
	sky       lipgloss.Color = "#69B4FF" // soft sky blue — tool result content
	yellow    lipgloss.Color = "#FAFC4E" // sulfur — compacting / paused
	green     lipgloss.Color = "#39FF14" // acid green — success / diff add bg
	red       lipgloss.Color = "#FF003C" // glitch red — errors / diff remove bg
	lightBlue lipgloss.Color = "#7DF9FF" // cyan glow — thinking spinner

	// Diff text foreground: true white on the solid green / red blocks.
	// This is the M2 visual change vs. v1 — v1 used neon green / red fg
	// on a desaturated pale bg, which read as "outlined block"; v2 uses
	// solid bg with white fg, which reads as "filled bar" and is the
	// look the user asked for ("more pro feeling").
	white lipgloss.Color = "#FFFFFF"

	// Hot pink — kept on the palette for any future accent that must
	// genuinely pop (greeting flourish). NOT on chrome surfaces.
	magenta lipgloss.Color = "#FF2A6D"

	// Cursor — cyan glow.
	cursor lipgloss.Color = "#05D9E8"

	// Reserved for any future "info" cell that wants a distinct blue.
	blue lipgloss.Color = "#5D5FEF"
)
