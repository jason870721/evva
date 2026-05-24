package status

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/events"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// SpinnerTickCmd returns a tea.Cmd that fires a SpinnerTickMsg after
// the theme's standard SpinnerInterval (100 ms). The App's Update
// handler returns another SpinnerTickCmd from the SpinnerTickMsg
// branch to keep the cycle going.
func SpinnerTickCmd() tea.Cmd {
	return tea.Tick(theme.SpinnerInterval, func(time.Time) tea.Msg {
		return events.SpinnerTickMsg{}
	})
}

// StatusBar holds the live HUD state and renders the bottom row of
// the screen. It's not a tea.Model in the strict sense — there's no
// Update method — because every input it needs flows through the
// App (state machine + usage + context tokens). It's a pure
// renderer.
type StatusBar struct {
	state *State

	usage        llm.Usage
	contextUsed  int
	contextLimit int

	model     string
	agentID   string
	effort    string
	permMode  string
	agentName string
}

// New constructs a StatusBar bound to the given run-state machine.
// The bar reads State.Current() / State.Frame() on every Compose
// call, so any state transitions reflect on the next render
// without explicit notification.
func New(state *State) *StatusBar {
	return &StatusBar{state: state}
}

// SetUsage stores the cumulative usage from the latest KindUsage
// event. The status bar reads from this on the next Compose; no
// explicit re-render needed.
func (s *StatusBar) SetUsage(u llm.Usage) { s.usage = u }

// SetContext stores the prompt-token / context-limit pair used to
// drive the utilization meter. used is the input-token count from
// the most recent agent turn (Session.LastTurnInputTokens); limit
// is the model's context window from constant.MODEL_CONTEXT_SIZE.
func (s *StatusBar) SetContext(used, limit int) {
	s.contextUsed = used
	s.contextLimit = limit
}

// SetModel installs the model id shown in the status bar's model
// cell. Empty string collapses to "-".
func (s *StatusBar) SetModel(m string) { s.model = m }

// SetAgentID stores the controller's agent id; rendered truncated
// to 8 chars. Empty input collapses the cell entirely.
func (s *StatusBar) SetAgentID(id string) { s.agentID = id }

// SetEffort stores the current effort level for the status bar cell.
func (s *StatusBar) SetEffort(level string) { s.effort = level }

// SetPermissionMode stores the active permission mode for the status bar
// cell. Empty string or "default" collapses the cell so the bar isn't
// cluttered for users in the default stance.
func (s *StatusBar) SetPermissionMode(mode string) { s.permMode = mode }

// SetAgentName stores the active persona's display label (typically
// uppercased: "EVVA", "NONO"). Empty falls back to "EVVA" in Compose so
// the pre-attach state still renders something sensible.
func (s *StatusBar) SetAgentName(name string) { s.agentName = name }

// Compose returns the rendered HUD as one styled line, padded to
// width. Layout (left → right):
//
//	‹⠋ STATE› ◆ EVVA ◆ ▸ model ◆ IN n  OUT m ◆ CTX ▰▰▱…▱ pct ◆ SID 1234abcd
//
// Each cell is separated by a violet diamond. Cells collapse when
// their underlying data is empty.
func (s *StatusBar) Compose(width int, th *theme.Theme) string {
	if width <= 0 {
		return ""
	}
	sep := th.StatusSep.Render(" ◆ ")

	parts := []string{
		renderStatePill(s.state, th),
		th.UserPrompt.Render(agentNameOrDefault(s.agentName)),
		th.StatusKey.Render("▸ ") + th.StatusValue.Render(modelOrDash(s.model)) + renderEffort(s.effort, th),
		th.StatusKey.Render("IN ") + th.StatusValue.Render(humanTokens(s.usage.InputTokens)) +
			th.StatusKey.Render("  OUT ") + th.StatusValue.Render(humanTokens(s.usage.OutputTokens)),
		renderContextBar(s.contextUsed, s.contextLimit, th),
	}
	if cell := renderPermissionMode(s.permMode, th); cell != "" {
		parts = append(parts, cell)
	}
	if id := shortAgentID(s.agentID); id != "" {
		parts = append(parts, th.StatusKey.Render("SID ")+th.StatusValue.Render(id))
	}
	body := strings.Join(parts, sep)
	return th.StatusBar.Width(width).Render(body)
}

// renderStatePill builds the leftmost HUD cell: animated braille
// spinner (or static glyph for terminal states) + uppercase label,
// in the state's accent color. Brackets are bare angle brackets so
// the cell reads as an instrument readout.
func renderStatePill(state *State, th *theme.Theme) string {
	cur := state.Current()
	style := th.StatePill.Foreground(stateColor(cur, th))
	var glyph string
	switch {
	case cur.IsActive():
		glyph = theme.SpinnerFrame(state.Frame())
	case cur == StateError:
		glyph = "✘"
	case cur == StateIterLimit:
		glyph = "⏸"
	default:
		glyph = "●"
	}
	label := strings.ToUpper(cur.String())
	return style.Render("") + style.Render(glyph+" "+label) + style.Render("")
}

// stateColor reads the theme palette via the existing style fields:
// each style's foreground is the canonical hue for its state cluster.
//
// We can't reach into the unexported palette colors from outside the
// theme package, so we extract them from style fields the theme
// already exports. This keeps the palette private while letting the
// status bar follow the theme.
func stateColor(s RunState, th *theme.Theme) lipgloss.Color {
	var c lipgloss.TerminalColor
	switch s {
	case StateRunning, StateTexting:
		c = th.ContextFill.GetForeground() // cyan
	case StateThinking:
		c = th.Thinking.GetForeground() // cool grey for the italic
	case StateExecuting:
		c = th.ToolCall.GetForeground() // brown
	case StateDraining:
		c = th.Draining.GetForeground() // purple
	case StateIterLimit, StateCompacting:
		c = th.Compacting.GetForeground() // yellow
	case StateError:
		c = th.ErrorBanner.GetForeground() // red
	default:
		c = th.TasksDone.GetForeground() // green
	}
	if col, ok := c.(lipgloss.Color); ok {
		return col
	}
	// Defensive: fall back to a neutral grey if the theme returned
	// a NoColor (shouldn't happen with Default()).
	return lipgloss.Color("#7A7E94")
}

// renderContextBar produces the HUD utilization meter. Half-block
// tally marks: filled ▰, empty ▱. Fill color shifts from green
// (0-20%) through yellow (40-60%) to red (80-100%). Empty rail is
// grey. Percentage is shown to 1 decimal place.
func renderContextBar(used, limit int, th *theme.Theme) string {
	const barWidth = 12
	pct := 0.0
	if limit > 0 {
		pct = float64(used) * 100.0 / float64(limit)
		if pct > 100 {
			pct = 100
		}
	}
	filled := int(pct * float64(barWidth) / 100.0)
	if filled > barWidth {
		filled = barWidth
	}
	fillStyle := lipgloss.NewStyle().Foreground(contextBarColor(pct)).Bold(true)
	bar := fillStyle.Render(strings.Repeat("▰", filled)) +
		th.ContextRail.Render(strings.Repeat("▱", barWidth-filled))
	return th.ContextBar.Render("CTX ") +
		bar + " " +
		th.StatusValue.Render(fmt.Sprintf("%.1f%%", pct))
}

// contextBarColor maps a 0-100 percentage to an RGB color on the
// green→yellow→red spectrum: green ≤20%, yellow at 40-60%, red ≥80%,
// with linear interpolation through the transition bands.
func contextBarColor(pct float64) lipgloss.Color {
	green := [3]uint8{0x39, 0xFF, 0x14}
	yellow := [3]uint8{0xFA, 0xFC, 0x4E}
	red := [3]uint8{0xFF, 0x00, 0x3C}

	var c [3]uint8
	switch {
	case pct <= 20:
		c = green
	case pct <= 40:
		t := (pct - 20) / 20
		c = lerpRGB(green, yellow, t)
	case pct <= 60:
		c = yellow
	case pct <= 80:
		t := (pct - 60) / 20
		c = lerpRGB(yellow, red, t)
	default:
		c = red
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]))
}

func lerpRGB(a, b [3]uint8, t float64) [3]uint8 {
	return [3]uint8{
		uint8(float64(a[0]) + (float64(b[0])-float64(a[0]))*t),
		uint8(float64(a[1]) + (float64(b[1])-float64(a[1]))*t),
		uint8(float64(a[2]) + (float64(b[2])-float64(a[2]))*t),
	}
}

// shortAgentID truncates the agent's UUID to its first 8 characters
// for status-bar display. Empty input collapses the cell.
func shortAgentID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// modelOrDash returns "-" for empty model strings so the status
// cell never reads as "▸ " with nothing after it. Matches v1.
func modelOrDash(m string) string {
	if m == "" {
		return "-"
	}
	return m
}

// agentNameOrDefault returns the active persona label uppercased, or
// "EVVA" when no name has been set. Keeps the pre-attach state rendering
// something sensible.
func agentNameOrDefault(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "EVVA"
	}
	return strings.ToUpper(name)
}

// humanTokens formats a raw token count with a `k`/`M` suffix once
// it crosses the threshold. Keeps the status bar tight on long
// sessions.
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

// renderEffort returns the effort level as a colored label.
// Colors: low→green, medium→blue, high→yellow, ultra→red. Empty returns "".
func renderEffort(level string, th *theme.Theme) string {
	if level == "" {
		return ""
	}
	var color lipgloss.Color
	switch level {
	case "low":
		color = "#39FF14" // green
	case "medium":
		color = "#00BFFF" // blue
	case "high":
		color = "#FAFC4E" // yellow
	case "ultra":
		color = "#FF003C" // red
	default:
		color = "#7A7E94" // grey fallback
	}
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	return " " + th.StatusKey.Render("·") + style.Render(level)
}

// renderPermissionMode returns a status-bar cell for the active mode, or
// "" when the mode is "default" (collapsed cell — no visual noise for the
// stance most users sit in most of the time). Color matches the stance's
// severity: green for accept_edits (helpful), blue for plan (informational),
// red for bypass (danger).
func renderPermissionMode(mode string, th *theme.Theme) string {
	if mode == "" || mode == "default" {
		return ""
	}
	var color lipgloss.Color
	switch mode {
	case "accept_edits":
		color = "#39FF14" // green
	case "plan":
		color = "#00BFFF" // blue
	case "bypass":
		color = "#FF003C" // red
	default:
		color = "#7A7E94"
	}
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	return th.StatusKey.Render("⛨ ") + style.Render(mode)
}

// ContextLimitFor returns the model's context window from the
// constant.MODEL_CONTEXT_SIZE table, or 0 when unknown. Exposed as
// a package-level helper so the App can pass it through to
// SetContext without re-importing constant.
func ContextLimitFor(model string) int {
	return constant.MODEL_CONTEXT_SIZE[constant.Model(model)]
}
