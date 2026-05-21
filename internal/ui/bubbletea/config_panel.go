package bubbletea

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
)

// pendingConfig is the /config form's in-flight state. A single overlay
// slot, navigation via up/down, Enter to edit the focused field, Esc to
// close. Persistence happens per-edit (every Apply call writes the YAML)
// so closing the panel is a no-op — there's nothing to commit at the end.
type pendingConfig struct {
	fields   []configField
	selected int
	editor   *textinput.Model // non-nil while editing; nil in list mode
	// errMsg is shown in the footer when an edit fails validation; cleared
	// on next navigation or successful edit.
	errMsg string
	// liveMsg shows the most recent applied edit ("max_iterations: 30 → 50")
	// so the user sees their change took.
	liveMsg string
}

// configField describes one editable row. Get returns the current value
// rendered for display (secrets are masked). Apply parses raw user input,
// validates, applies it (in-memory + persisted), and returns an error
// that surfaces in the footer when validation fails.
type configField struct {
	Label  string
	Kind   configFieldKind
	Get    func() string
	Apply  func(string) error
}

type configFieldKind int

const (
	cfgKindString configFieldKind = iota
	cfgKindInt
	cfgKindFloat
	cfgKindBool
	cfgKindSecret
)

// buildConfigFields returns the editable field list bound to the given
// AppConfig + Controller. Order is the display order; grouping is
// implicit (loop tunables first, web second, providers last).
func buildConfigFields(cfg *config.Config, ctrl interface {
	SetMaxIterations(int)
}) []configField {
	return []configField{
		{
			Label: "max_iterations",
			Kind:  cfgKindInt,
			Get:   func() string { return strconv.Itoa(cfg.DefaultMaxIterations) },
			Apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not an integer: %s", s)
				}
				if err := cfg.SetMaxIterations(n); err != nil {
					return err
				}
				ctrl.SetMaxIterations(n)
				return nil
			},
		},
		{
			Label: "max_tokens",
			Kind:  cfgKindInt,
			Get:   func() string { return strconv.Itoa(cfg.DefaultMaxTokens) },
			Apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not an integer: %s", s)
				}
				return cfg.SetMaxTokens(n)
			},
		},
		{
			Label: "auto_compact_threshold",
			Kind:  cfgKindFloat,
			Get:   func() string { return strconv.FormatFloat(cfg.AutoCompactThreshold, 'g', -1, 64) },
			Apply: func(s string) error {
				f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
				if err != nil {
					return fmt.Errorf("not a number: %s", s)
				}
				return cfg.SetAutoCompactThreshold(f)
			},
		},
		{
			Label: "display_thinking",
			Kind:  cfgKindBool,
			Get:   func() string { return strconv.FormatBool(cfg.GetDisplayThinking()) },
			Apply: func(s string) error {
				b, err := strconv.ParseBool(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not a bool: %s", s)
				}
				return cfg.SetDisplayThinking(b)
			},
		},
		{
			Label: "fetch_max_bytes",
			Kind:  cfgKindInt,
			Get:   func() string { return strconv.Itoa(cfg.FetchMaxBytes) },
			Apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not an integer: %s", s)
				}
				return cfg.SetFetchMaxBytes(n)
			},
		},
		{
			Label: "tavily_api_key",
			Kind:  cfgKindSecret,
			Get:   func() string { return cfg.TavilyAPIKey },
			Apply: func(s string) error { return cfg.SetTavilyAPIKey(strings.TrimSpace(s)) },
		},
		providerKeyField(cfg, constant.ANTHROPIC.Name),
		providerURLField(cfg, constant.ANTHROPIC.Name),
		providerKeyField(cfg, constant.DEEPSEEK.Name),
		providerURLField(cfg, constant.DEEPSEEK.Name),
		providerKeyField(cfg, constant.OPENAI.Name),
		providerURLField(cfg, constant.OPENAI.Name),
		providerURLField(cfg, constant.OLLAMA.Name),
	}
}

func providerKeyField(cfg *config.Config, name string) configField {
	return configField{
		Label: name + ".api_key",
		Kind:  cfgKindSecret,
		Get:   func() string { return cfg.LLMProviderConfig[name].ApiSecret },
		Apply: func(s string) error { return cfg.SetProviderAPIKey(name, strings.TrimSpace(s)) },
	}
}

func providerURLField(cfg *config.Config, name string) configField {
	return configField{
		Label: name + ".api_url",
		Kind:  cfgKindString,
		Get: func() string {
			p, ok := cfg.LLMProviderConfig[name]
			if !ok {
				return ""
			}
			return p.ApiURL
		},
		Apply: func(s string) error { return cfg.SetProviderAPIURL(name, strings.TrimSpace(s)) },
	}
}

// maskSecret renders a secret value for safe display. Empty → "(empty)";
// short values → "****"; long values → "****" + last 4 chars so the user
// can spot-check which key is loaded without leaking it to a screen
// recording.
func maskSecret(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// displayValue returns the user-facing string for a field's current
// value, applying mask for secrets and a friendly "(empty)" for blanks.
func (f configField) displayValue() string {
	v := f.Get()
	if f.Kind == cfgKindSecret {
		return maskSecret(v)
	}
	if v == "" {
		return "(empty)"
	}
	return v
}

// openConfig builds the field list and pushes the form into the
// pendingConfig slot. Layout recompute happens at the call site.
func (m *rootModel) openConfig() {
	if m.controller == nil {
		m.hintText = "no controller attached"
		return
	}
	m.pendingConfig = &pendingConfig{
		fields:   buildConfigFields(config.Get(), m.controller),
		selected: 0,
	}
}

// closeConfig clears the form. Edits have already been persisted per-Apply,
// so this is just a UI dismissal.
func (m *rootModel) closeConfig() {
	m.pendingConfig = nil
	m.layoutSizes()
}

// handleConfigKey routes keystrokes while the /config form is on screen.
// A list mode for navigation, an editor mode while the user is typing
// into a focused textinput.
func (m *rootModel) handleConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pc := m.pendingConfig
	if msg.Type == tea.KeyCtrlC {
		m.closeConfig()
		m.cancelRunIfAny()
		return m, tea.Quit
	}

	// Editor mode: textinput is focused. Enter applies + closes, Esc
	// cancels the edit, everything else flows to the textinput.
	if pc.editor != nil {
		switch msg.Type {
		case tea.KeyEsc:
			pc.editor = nil
			pc.errMsg = ""
			return m, nil
		case tea.KeyEnter:
			val := pc.editor.Value()
			if err := pc.fields[pc.selected].Apply(val); err != nil {
				pc.errMsg = err.Error()
				return m, nil
			}
			pc.liveMsg = fmt.Sprintf("%s saved", pc.fields[pc.selected].Label)
			pc.errMsg = ""
			pc.editor = nil
			m.layoutSizes()
			return m, nil
		}
		ti, cmd := pc.editor.Update(msg)
		pc.editor = &ti
		return m, cmd
	}

	// List mode.
	switch msg.Type {
	case tea.KeyEsc:
		m.closeConfig()
		return m, nil
	case tea.KeyUp:
		if pc.selected > 0 {
			pc.selected--
			pc.errMsg = ""
		}
		return m, nil
	case tea.KeyDown:
		if pc.selected < len(pc.fields)-1 {
			pc.selected++
			pc.errMsg = ""
		}
		return m, nil
	case tea.KeyEnter:
		f := pc.fields[pc.selected]
		// Bools toggle in place — no editor needed.
		if f.Kind == cfgKindBool {
			cur := strings.TrimSpace(f.Get())
			next := "true"
			if cur == "true" {
				next = "false"
			}
			if err := f.Apply(next); err != nil {
				pc.errMsg = err.Error()
				return m, nil
			}
			pc.liveMsg = fmt.Sprintf("%s: %s → %s", f.Label, cur, next)
			pc.errMsg = ""
			return m, nil
		}
		// Everything else opens a textinput pre-filled with the current
		// value. Secrets start blank (no display of the masked form
		// inside an editable field).
		ti := textinput.New()
		ti.CharLimit = 0
		ti.Width = 48
		ti.Prompt = "> "
		if f.Kind != cfgKindSecret {
			ti.SetValue(f.Get())
		}
		if f.Kind == cfgKindSecret {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		ti.Focus()
		pc.editor = &ti
		pc.errMsg = ""
		return m, textinput.Blink
	}
	return m, nil
}

// configPanel renders the /config overlay, or "" when no form is open
// (so layoutSizes collapses the slot to zero).
func (m *rootModel) configPanel(width int) string {
	if m.pendingConfig == nil {
		return ""
	}
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}
	if m.pendingConfig.editor != nil {
		return styles.InputBorder.Render(m.renderConfigEditor(innerWidth))
	}
	return styles.InputBorder.Render(m.renderConfigList(innerWidth))
}

func (m *rootModel) renderConfigList(innerWidth int) string {
	pc := m.pendingConfig

	var b strings.Builder
	b.WriteString(styles.PanelHeader.Render("▰ /CONFIG"))
	b.WriteByte('\n')

	// Compute label column width so values align.
	labelW := 0
	for _, f := range pc.fields {
		if len(f.Label) > labelW {
			labelW = len(f.Label)
		}
	}
	labelW += 2

	sel := lipgloss.NewStyle().Foreground(paletteCyan).Bold(true)
	dim := styles.DimText
	for i, f := range pc.fields {
		marker := "  "
		style := dim
		if i == pc.selected {
			marker = "▶ "
			style = sel
		}
		line := fmt.Sprintf("%s%-*s  %s", marker, labelW, f.Label, f.displayValue())
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	if pc.errMsg != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(paletteMagenta).Render("✗ " + pc.errMsg))
		b.WriteByte('\n')
	} else if pc.liveMsg != "" {
		b.WriteString(styles.DimText.Render("✓ " + pc.liveMsg))
		b.WriteByte('\n')
	}
	b.WriteString(styles.FooterHint.Render(
		"[↑↓] navigate · [Enter] edit/toggle · [Esc] close"))
	return strings.TrimRight(b.String(), "\n")
}

func (m *rootModel) renderConfigEditor(innerWidth int) string {
	pc := m.pendingConfig
	f := pc.fields[pc.selected]

	var b strings.Builder
	b.WriteString(styles.PanelHeader.Render(fmt.Sprintf("▰ EDIT %s", f.Label)))
	b.WriteByte('\n')

	hint := ""
	switch f.Kind {
	case cfgKindInt:
		hint = "integer"
	case cfgKindFloat:
		hint = "float (e.g. 0.8)"
	case cfgKindSecret:
		hint = "secret — input is masked"
	default:
		hint = "string"
	}
	b.WriteString(styles.DimText.Render(fmt.Sprintf("type: %s · current: %s", hint, f.displayValue())))
	b.WriteString("\n\n")
	b.WriteString(pc.editor.View())
	b.WriteByte('\n')

	if pc.errMsg != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(paletteMagenta).Render("✗ " + pc.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(styles.FooterHint.Render("[Enter] apply & save · [Esc] cancel"))
	return b.String()
}
