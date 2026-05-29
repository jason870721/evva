// Package slash renders the autocomplete suggestion panel that pops
// up when the user types "/" at the start of the input. It is NOT
// part of the focus stack — the input keeps focus while the panel
// is visible; the panel is a passive renderer plus a tiny state
// machine for the highlighted row and the "Esc dismissed this
// typing session" flag.
//
// The App owns one *Panel for the lifetime of the program and
// drives it through Match (on every keystroke), MoveSel (on Up/Down
// when the panel is visible), Complete (on Tab), and Dismiss /
// Reset (on Esc and after-submit).
package slash

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// maxSuggestions caps how many rows render. With many skills
// installed the merged catalog can grow long; capping keeps the
// panel compact and the input area large.
const maxSuggestions = 5

// Command is one entry in the suggestion catalog. Name includes
// the leading "/". Builtin commands ship with the binary; skill
// entries are added at runtime from Controller.Skills().
type Command struct {
	Name string
	Desc string
}

// builtins is the static catalog in display order. /compact first
// (most common UX action), then /config, /model, /clear, /exit.
// /quit is a recognised submit alias but omitted from suggestions
// to keep the row count tight.
var builtins = []Command{
	{Name: "/compact", Desc: "compact the transcript · pick micro or full"},
	{Name: "/config", Desc: "edit runtime settings (max_iterations, api keys, …)"},
	{Name: "/effort", Desc: "set thinking effort · low, medium, high, ultra"},
	{Name: "/model", Desc: "switch llm provider / model · clears history"},
	{Name: "/profile", Desc: "switch agent persona · clears history"},
	{Name: "/mcp", Desc: "list configured MCP servers and their status"},
	{Name: "/resume", Desc: "resume a previous session from this workdir"},
	{Name: "/update", Desc: "check for updates and install the latest version"},
	{Name: "/clear", Desc: "clear the transcript"},
	{Name: "/exit", Desc: "quit evva"},
}

// Panel holds the suggestion-overlay's state:
//
//   - selected: the highlighted row, clamped to the current match
//     list each render (a shrinking list could otherwise leave
//     selected dangling past the end).
//   - dismissed: true after Esc, until the input drops back to ""
//     (so re-typing "/" reopens the panel naturally).
type Panel struct {
	selected  int
	dismissed bool
}

// New returns an empty panel. Cheap; safe to create at App
// construction time.
func New() *Panel { return &Panel{} }

// Catalog returns the merged builtin + skills catalog. ctrl may be
// nil — pre-Attach state — in which case only builtins are
// returned.
func Catalog(ctrl ui.Controller) []Command {
	out := make([]Command, 0, len(builtins)+4)
	out = append(out, builtins...)
	if ctrl == nil {
		return out
	}
	for _, s := range ctrl.Skills() {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			desc = "user-installed skill"
		}
		out = append(out, Command{Name: "/" + name, Desc: desc})
	}
	return out
}

// Match returns catalog entries whose name has the trimmed,
// lowercased input as a prefix. Empty / non-"/" input returns nil
// so the caller can collapse the panel.
//
// The result is hard-capped at maxSuggestions.
func Match(input string, catalog []Command) []Command {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}
	lower := strings.ToLower(trimmed)
	out := make([]Command, 0, len(catalog))
	for _, c := range catalog {
		if strings.HasPrefix(c.Name, lower) {
			out = append(out, c)
			if len(out) >= maxSuggestions {
				break
			}
		}
	}
	return out
}

// Visible reports whether the panel should be drawn for the given
// input + overlay state. The App passes overlayOpen=true whenever
// any modal Focusable is on top — slash suggestions stay hidden
// behind overlays where the input box itself is inert.
func (p *Panel) Visible(input string, overlayOpen bool, catalog []Command) bool {
	if overlayOpen {
		return false
	}
	if p.dismissed {
		return false
	}
	return len(Match(input, catalog)) > 0
}

// Dismiss hides the panel until the user clears the input back
// below "/". Bound to Esc in the App when no overlay is open.
func (p *Panel) Dismiss() { p.dismissed = true }

// Reset clears the dismissed flag — called when the user submits
// (so the next /-prefixed input shows suggestions again) and when
// the user blanks the input box.
func (p *Panel) Reset() {
	p.dismissed = false
	p.selected = 0
}

// Selected returns the highlighted index (clamped if the match list
// shrank since the user last navigated).
func (p *Panel) Selected() int { return p.selected }

// MoveSel shifts the highlighted row by delta (+1 / -1). Clamps to
// the bounds of the current match list. Returns true when a move
// happened so the App can swallow the key event.
func (p *Panel) MoveSel(input string, catalog []Command, delta int) bool {
	matches := Match(input, catalog)
	if len(matches) == 0 {
		return false
	}
	next := p.selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(matches) {
		next = len(matches) - 1
	}
	if next == p.selected {
		return false
	}
	p.selected = next
	return true
}

// Complete returns the full name of the currently highlighted
// suggestion (or "" when no matches). The App uses this to replace
// the input's value on Tab.
func (p *Panel) Complete(input string, catalog []Command) string {
	matches := Match(input, catalog)
	if len(matches) == 0 {
		return ""
	}
	idx := p.selected
	if idx >= len(matches) {
		idx = 0
	}
	return matches[idx].Name
}

// View renders the panel as a bordered box, or "" when not visible.
// width is the column count to render within; the panel uses a
// 4-col inner margin so the bordered box sits inside the App's
// layout slot.
func (p *Panel) View(input string, ctrl ui.Controller, width int, th *theme.Theme) string {
	catalog := Catalog(ctrl)
	if !p.Visible(input, false, catalog) {
		return ""
	}
	matches := Match(input, catalog)

	// Clamp selection — match list size changes as user types.
	if p.selected >= len(matches) {
		p.selected = len(matches) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}

	// Column-align names so descriptions line up.
	nameW := 0
	for _, c := range matches {
		if len(c.Name) > nameW {
			nameW = len(c.Name)
		}
	}

	typed := strings.ToLower(strings.TrimSpace(input))

	// We pull the cursor + accent colors via theme styles so the
	// palette stays private to the theme package.
	selStyle := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	exactStyle := lipgloss.NewStyle().Foreground(extractFg(th.TasksDone)).Bold(true)
	dim := th.DimText

	var b strings.Builder
	for i, c := range matches {
		marker := "  "
		style := dim
		isExact := c.Name == typed
		switch {
		case isExact:
			marker = "✓ "
			style = exactStyle
		case i == p.selected:
			marker = "▶ "
			style = selStyle
		}
		line := fmt.Sprintf("%s%-*s   %s", marker, nameW, c.Name, c.Desc)
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render(
		"[Tab] complete · [↑↓] pick · [Enter] submit · [Esc] dismiss",
	))
	return th.InputBorder.Render(b.String())
}

// extractFg recovers a lipgloss.Color from a style. NoColor falls
// back to muted grey so the box renders something even when the
// theme has a gap.
func extractFg(s lipgloss.Style) lipgloss.Color {
	if c, ok := s.GetForeground().(lipgloss.Color); ok {
		return c
	}
	return lipgloss.Color("#7A7E94")
}
