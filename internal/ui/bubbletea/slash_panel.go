package bubbletea

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// slashCommand is one entry in the autocomplete catalog. name includes
// the leading "/"; desc is the short label shown next to it. Order in
// defaultSlashCommands is the display order when the user has typed only
// "/" — put the commands they'll reach for most first.
type slashCommand struct {
	name string
	desc string
}

// slashMaxSuggestions caps the number of rows the suggestion panel
// renders. With skills installed the merged list can grow long; capping
// keeps the panel compact and the input area large.
const slashMaxSuggestions = 5

// defaultSlashCommands is the built-in command catalog, in display order.
// /compact is intentionally listed first even though the implementation
// is still pending — surfacing the affordance is what the user wants
// while we ship it. /quit remains an alias the submit handler recognizes
// but is omitted from the suggestion list to keep the row count tight.
var defaultSlashCommands = []slashCommand{
	{"/compact", "compact the transcript · pick micro or full"},
	{"/config", "edit runtime settings (max_iterations, api keys, …)"},
	{"/model", "switch llm provider / model · clears history"},
	{"/clear", "clear the transcript"},
	{"/exit", "quit evva"},
}

// matchSlashCommands returns the entries from all whose name has the
// trimmed input as a prefix. An exact match (input == name) collapses to
// that single entry — once the user has typed the full command there's
// nothing left to suggest. Empty / non-"/" input returns nil so the
// caller can collapse the panel.
//
// The result is hard-capped at slashMaxSuggestions so the panel stays
// short even with many installed skills.
func matchSlashCommands(input string, all []slashCommand) []slashCommand {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}
	lower := strings.ToLower(trimmed)
	out := make([]slashCommand, 0, len(all))
	for _, c := range all {
		if strings.HasPrefix(c.name, lower) {
			out = append(out, c)
			if len(out) >= slashMaxSuggestions {
				break
			}
		}
	}
	return out
}

// availableSlashCommands returns the catalog matched against the current
// input: built-in commands first, then user-installed skills. Skills are
// rendered as `/<name>` with the registry's description so the user can
// tell them apart from built-ins at a glance.
//
// The controller is consulted at call time so newly-installed skills (a
// possible future feature) show up without restart; today the registry
// is fixed at startup and the controller returns a stable list.
func (m *rootModel) availableSlashCommands() []slashCommand {
	out := make([]slashCommand, 0, len(defaultSlashCommands)+4)
	out = append(out, defaultSlashCommands...)
	if m.controller == nil {
		return out
	}
	for _, s := range m.controller.Skills() {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			desc = "user-installed skill"
		}
		out = append(out, slashCommand{name: "/" + name, desc: desc})
	}
	return out
}

// slashVisible reports whether the suggestion panel should be drawn
// this tick. Suppressed when an overlay is up (the main input is
// behind them and can't be edited anyway), when the user has dismissed
// the panel with Esc for this typing session, or when there are no
// matches.
func (m *rootModel) slashVisible() bool {
	if m.pendingConfig != nil || m.pendingModel != nil || m.pendingCompact != nil {
		return false
	}
	if m.slashDismissed {
		return false
	}
	return len(matchSlashCommands(m.input.Value(), m.availableSlashCommands())) > 0
}

// completeSlash replaces the current input with the highlighted
// suggestion. Used on Tab — the user can then press Enter to submit
// or keep typing arguments (today no slash command takes args, but
// the trailing-space hook is here for when one does).
func (m *rootModel) completeSlash() tea.Cmd {
	matches := matchSlashCommands(m.input.Value(), m.availableSlashCommands())
	if len(matches) == 0 {
		return nil
	}
	idx := m.slashSel
	if idx >= len(matches) {
		idx = 0
	}
	m.input.SetValue(matches[idx].name)
	m.input.CursorEnd()
	m.slashSel = 0
	return nil
}

// slashMoveSel shifts the highlighted suggestion. delta is +1 / -1;
// clamps to the bounds of the current match list. Returns true when a
// move happened so the caller can swallow the key event.
func (m *rootModel) slashMoveSel(delta int) bool {
	matches := matchSlashCommands(m.input.Value(), m.availableSlashCommands())
	if len(matches) == 0 {
		return false
	}
	next := m.slashSel + delta
	if next < 0 {
		next = 0
	}
	if next >= len(matches) {
		next = len(matches) - 1
	}
	if next == m.slashSel {
		return false
	}
	m.slashSel = next
	return true
}

// slashPanel renders the suggestion overlay or "" when not visible.
func (m *rootModel) slashPanel(width int) string {
	if !m.slashVisible() {
		return ""
	}
	matches := matchSlashCommands(m.input.Value(), m.availableSlashCommands())
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}

	// Clamp selection — match list size changes as the user types more.
	if m.slashSel >= len(matches) {
		m.slashSel = len(matches) - 1
	}
	if m.slashSel < 0 {
		m.slashSel = 0
	}

	// Column-align names so descriptions line up.
	nameW := 0
	for _, c := range matches {
		if len(c.name) > nameW {
			nameW = len(c.name)
		}
	}

	// A 100%-match row (input is exactly this command, modulo case &
	// whitespace) means pressing Enter will execute it now — paint it
	// in acid green so the user has a visual confirmation that the
	// command is "live" before they commit.
	typed := strings.ToLower(strings.TrimSpace(m.input.Value()))

	sel := lipgloss.NewStyle().Foreground(paletteCyan).Bold(true)
	exact := lipgloss.NewStyle().Foreground(paletteGreen).Bold(true)
	dim := styles.DimText

	var b strings.Builder
	for i, c := range matches {
		marker := "  "
		style := dim
		isExact := c.name == typed
		switch {
		case isExact:
			marker = "✓ "
			style = exact
		case i == m.slashSel:
			marker = "▶ "
			style = sel
		}
		line := fmt.Sprintf("%s%-*s   %s", marker, nameW, c.name, c.desc)
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}
	b.WriteString(styles.FooterHint.Render(
		"[Tab] complete · [↑↓] pick · [Enter] submit · [Esc] dismiss",
	))
	return styles.InputBorder.Render(b.String())
}
