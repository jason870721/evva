package bubbletea

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// runState enumerates the agent's high-level state from the UI's
// perspective. Drives status-bar text, input-disable logic, and the
// state color in the status pill.
//
// The "active" states (thinking / texting / executing / draining /
// compacting) all render with the rotating braille spinner so the
// user can tell at a glance that work is in flight; idle / paused /
// error use static glyphs.
type runState int

const (
	stateIdle       runState = iota
	stateRunning             // generic "agent loop is alive between sub-phases"
	stateThinking            // model is generating reasoning tokens
	stateTexting             // model is generating response content tokens
	stateExecuting           // a tool call is in flight
	stateDraining            // pulling async subagent results back
	stateCompacting          // micro/full session compaction running
	stateIterLimit
	stateError
)

func (s runState) String() string {
	switch s {
	case stateRunning:
		return "running"
	case stateThinking:
		return "thinking"
	case stateTexting:
		return "texting"
	case stateExecuting:
		return "executing"
	case stateDraining:
		return "draining"
	case stateCompacting:
		return "compacting"
	case stateIterLimit:
		return "paused"
	case stateError:
		return "error"
	default:
		return "ready"
	}
}

// isActive reports whether the state represents work-in-flight, i.e.
// the status pill should animate with the spinner rather than show a
// static dot.
func (s runState) isActive() bool {
	switch s {
	case stateRunning, stateThinking, stateTexting, stateExecuting,
		stateDraining, stateCompacting:
		return true
	}
	return false
}

// stateColor maps a runState onto the palette color used by the status
// pill. Vocabulary in the neon palette:
//   - acid green → idle, work complete
//   - cyan glow  → reasoning (cool contemplation)
//   - cyan       → emitting content / running (fluent output)
//   - brown      → tool execution (matches the ToolCall style)
//   - purple     → draining (subagent results syncing)
//   - yellow     → housekeeping (compacting, iter-limit paused)
//   - glitch red → fault
func stateColor(s runState) lipgloss.Color {
	switch s {
	case stateRunning, stateTexting:
		return paletteCyan
	case stateThinking:
		return paletteLightBlue
	case stateExecuting:
		return paletteBrown
	case stateDraining:
		return palettePurple
	case stateIterLimit, stateCompacting:
		return paletteYellow
	case stateError:
		return paletteRed
	default:
		return paletteGreen
	}
}

// statusBarInput bundles everything the bottom status bar renders so the
// callsite doesn't have to plumb half a dozen positional args.
type statusBarInput struct {
	Width        int
	Model        string
	Usage        llm.Usage
	State        runState
	Frame        int
	ContextUsed  int    // tokens currently in the prompt (last turn's input)
	ContextLimit int    // model's context window from constant.MODEL_CONTEXT_SIZE
	AgentID      string // root agent's id; rendered truncated to 8 chars
}

// renderStatusBar formats the bottom one-liner as a neon HUD: a
// bracketed animated state pill on the left, the brand mark, model
// id, cumulative tokens, a context-utilization meter, and the root
// agent's id — each cell separated by a hot-pink diamond.
//
// Layout: `‹⠋ THINKING› ◆ evva ◆ ▸ model ◆ in X out Y ◆ CTX ▰▰▱▱…▱ 12% ◆ id 1234abcd`.
// Width pads the bar so it fills the terminal. AgentID is rendered
// truncated to 8 chars; an empty id collapses the cell entirely.
func renderStatusBar(in statusBarInput) string {
	sep := styles.StatusSep.Render(" ◆ ")
	parts := []string{
		renderStatePill(in.State, in.Frame),
		styles.UserPrompt.Render("EVVA"),
		styles.StatusKey.Render("▸ ") + styles.StatusValue.Render(in.Model),
		styles.StatusKey.Render("IN ") + styles.StatusValue.Render(humanTokens(in.Usage.InputTokens)) +
			styles.StatusKey.Render("  OUT ") + styles.StatusValue.Render(humanTokens(in.Usage.OutputTokens)),
		renderContextBar(in.ContextUsed, in.ContextLimit),
	}
	if id := shortAgentID(in.AgentID); id != "" {
		parts = append(parts, styles.StatusKey.Render("SID ")+styles.StatusValue.Render(id))
	}
	body := strings.Join(parts, sep)
	return styles.StatusBar.Width(in.Width).Render(body)
}

// shortAgentID truncates the agent's uuid to its first 8 characters
// for status-bar display — matches the banner row format and is
// enough to tell two concurrent sessions apart in logs / screenshots.
// Empty input collapses the cell.
func shortAgentID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// renderStatePill renders the leftmost HUD cell: animated braille
// spinner + uppercase state label, wrapped in single-arrow brackets so
// the cell reads like a sci-fi instrument readout. Brackets and label
// share the state's neon color via the StatePill base style.
//
// Idle / paused / error use a static glyph so the user can distinguish
// "live work" from terminal states at a glance.
func renderStatePill(state runState, frame int) string {
	c := stateColor(state)
	style := styles.StatePill.Foreground(c)
	var glyph string
	switch {
	case state.isActive():
		glyph = spinnerFrame(frame)
	case state == stateError:
		glyph = "✘"
	case state == stateIterLimit:
		glyph = "⏸"
	default:
		glyph = "●"
	}
	label := strings.ToUpper(state.String())
	return style.Render("") + style.Render(glyph+" "+label) + style.Render("")
}

// renderContextBar produces a HUD utilization meter showing how much
// of the model's context window has been consumed. Rendered with
// half-block tally marks (▰ filled, ▱ empty) and bracketed in cyan so
// it reads as an instrument cell rather than a vague progress widget.
//
// Format: `CTX ▰▰▰▱▱▱▱▱▱▱▱▱ 12%`. used==0 collapses gracefully to 0%.
func renderContextBar(used, limit int) string {
	const barWidth = 12
	pct := 0
	if limit > 0 {
		pct = used * 100 / limit
		if pct > 100 {
			pct = 100
		}
	}
	filled := pct * barWidth / 100
	if filled > barWidth {
		filled = barWidth
	}
	bar := styles.ContextFill.Render(strings.Repeat("▰", filled)) +
		styles.ContextRail.Render(strings.Repeat("▱", barWidth-filled))
	return styles.ContextBar.Render("CTX ") +
		bar + " " +
		styles.StatusValue.Render(fmt.Sprintf("%d%%", pct))
}

// contextLimitFor returns the model's context window from the static
// table, or 0 when unknown — renderContextBar treats 0 as "no data" and
// shows 0%.
func contextLimitFor(model string) int {
	return constant.MODEL_CONTEXT_SIZE[constant.Model(model)]
}

// humanTokens formats a raw token count with a `k`/`m` suffix once it
// crosses the threshold. Keeps the status bar tight on long sessions.
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
