package overlays

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// ConfigField describes one editable row in the form.
type ConfigField struct {
	Label string
	Kind  ConfigFieldKind
	Get   func() string
	Apply func(string) error
}

type ConfigFieldKind int

const (
	cfgKindString ConfigFieldKind = iota
	cfgKindInt
	cfgKindFloat
	cfgKindBool
	cfgKindSecret
)

// Config is the /config form. Two modes:
//
//   - list mode: cursor moves between fields with Up/Down; Enter
//     toggles bools in place or opens the editor.
//   - editor mode: a textinput is focused; Enter applies + saves,
//     Esc cancels the edit, all other keys flow through to the
//     textinput.
//
// Persistence happens per-Apply (every successful edit writes the
// YAML), so closing the panel is a no-op.
type Config struct {
	ctrl    ui.Controller
	fields  []ConfigField
	sel     int
	editor  *textinput.Model // nil in list mode
	errMsg  string
	liveMsg string
}

// NewConfig opens the form, building the field list bound to the
// current AppConfig + Controller. Returns nil when ctrl is nil so
// the App can hint "no controller attached" instead of opening an
// empty form.
func NewConfig(ctrl ui.Controller) *Config {
	if ctrl == nil {
		return nil
	}
	return &Config{
		ctrl:   ctrl,
		fields: buildConfigFields(config.Get(), ctrl),
	}
}

func (c *Config) Key() string { return "config" }
func (c *Config) Modal() bool { return true }
func (c *Config) Hint() string {
	if c.editor != nil {
		return "[Enter] apply & save · [Esc] cancel edit"
	}
	return "[↑↓] navigate · [Enter] edit/toggle · [Esc] close"
}

// Update consumes keys while on top of the focus stack.
//
// Returns close=true on Esc in list mode (or Ctrl+C anywhere).
// Editor-mode Esc clears the editor and stays open; editor-mode
// Enter applies + clears the editor + stays open with the success
// message.
func (c *Config) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)

	// Editor mode: textinput is focused.
	if c.editor != nil {
		if isKey {
			switch key.String() {
			case "esc":
				c.editor = nil
				c.errMsg = ""
				return false, nil
			case "enter":
				val := c.editor.Value()
				if err := c.fields[c.sel].Apply(val); err != nil {
					c.errMsg = err.Error()
					return false, nil
				}
				c.liveMsg = fmt.Sprintf("%s saved", c.fields[c.sel].Label)
				c.errMsg = ""
				c.editor = nil
				return false, nil
			case "ctrl+c":
				return true, nil
			}
		}
		// Other keys flow to the textinput (including bracketed
		// pastes via the Paste flag).
		ti, cmd := c.editor.Update(msg)
		c.editor = &ti
		return false, cmd
	}

	// List mode.
	if !isKey {
		return false, nil
	}
	switch key.String() {
	case "esc":
		return true, nil
	case "ctrl+c":
		return true, nil
	case "up", "k":
		if c.sel > 0 {
			c.sel--
			c.errMsg = ""
		}
		return false, nil
	case "down", "j":
		if c.sel < len(c.fields)-1 {
			c.sel++
			c.errMsg = ""
		}
		return false, nil
	case "enter":
		f := c.fields[c.sel]
		// Bools toggle in place — no editor needed.
		if f.Kind == cfgKindBool {
			cur := strings.TrimSpace(f.Get())
			next := "true"
			if cur == "true" {
				next = "false"
			}
			if err := f.Apply(next); err != nil {
				c.errMsg = err.Error()
				return false, nil
			}
			c.liveMsg = fmt.Sprintf("%s: %s → %s", f.Label, cur, next)
			c.errMsg = ""
			return false, nil
		}
		// Everything else opens a textinput pre-filled with the
		// current value (or blank for secrets).
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
		c.editor = &ti
		c.errMsg = ""
		return false, textinput.Blink
	}
	return false, nil
}

func (c *Config) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 30 {
		innerWidth = 30
	}
	if c.editor != nil {
		return th.InputBorder.Render(c.renderEditor(innerWidth, th))
	}
	return th.InputBorder.Render(c.renderList(innerWidth, th))
}

func (c *Config) renderList(innerWidth int, th *theme.Theme) string {
	_ = innerWidth
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /CONFIG"))
	b.WriteByte('\n')

	labelW := 0
	for _, f := range c.fields {
		if len(f.Label) > labelW {
			labelW = len(f.Label)
		}
	}
	labelW += 2

	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	for i, f := range c.fields {
		marker := "  "
		style := dim
		if i == c.sel {
			marker = "▶ "
			style = sel
		}
		line := fmt.Sprintf("%s%-*s  %s", marker, labelW, f.Label, displayValue(f))
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if c.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + c.errMsg))
		b.WriteByte('\n')
	} else if c.liveMsg != "" {
		b.WriteString(th.DimText.Render("✓ " + c.liveMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render(
		"[↑↓] navigate · [Enter] edit/toggle · [Esc] close"))
	return strings.TrimRight(b.String(), "\n")
}

func (c *Config) renderEditor(innerWidth int, th *theme.Theme) string {
	_ = innerWidth
	f := c.fields[c.sel]

	var b strings.Builder
	b.WriteString(th.PanelHeader.Render(fmt.Sprintf("▰ EDIT %s", f.Label)))
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
	b.WriteString(th.DimText.Render(fmt.Sprintf("type: %s · current: %s", hint, displayValue(f))))
	b.WriteString("\n\n")
	b.WriteString(c.editor.View())
	b.WriteByte('\n')

	if c.errMsg != "" {
		b.WriteString(th.ErrorBanner.Render("✗ " + c.errMsg))
		b.WriteByte('\n')
	}
	b.WriteString(th.FooterHint.Render("[Enter] apply & save · [Esc] cancel"))
	return b.String()
}

// ----------------------------------------------------------------------------
// Field catalog
// ----------------------------------------------------------------------------

func buildConfigFields(cfg *config.Config, ctrl ui.Controller) []ConfigField {
	return []ConfigField{
		{
			Label: "max_iterations", Kind: cfgKindInt,
			Get: func() string { return strconv.Itoa(cfg.DefaultMaxIterations) },
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
			Label: "max_tokens", Kind: cfgKindInt,
			Get: func() string { return strconv.Itoa(cfg.DefaultMaxTokens) },
			Apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not an integer: %s", s)
				}
				return cfg.SetMaxTokens(n)
			},
		},
		{
			Label: "temperature", Kind: cfgKindFloat,
			Get: func() string {
				t := cfg.LLMTemperature()
				if t == nil {
					return "(default)"
				}
				return strconv.FormatFloat(*t, 'g', -1, 64)
			},
			Apply: func(s string) error {
				s = strings.TrimSpace(s)
				if s == "" || s == "default" {
					return ctrl.SetLLMTemperature(nil)
				}
				v, err := strconv.ParseFloat(s, 64)
				if err != nil {
					return fmt.Errorf("not a number: %s", s)
				}
				return ctrl.SetLLMTemperature(&v)
			},
		},
		{
			Label: "top_k", Kind: cfgKindInt,
			Get: func() string {
				k := cfg.LLMTopK()
				if k == nil {
					return "(default)"
				}
				return strconv.Itoa(*k)
			},
			Apply: func(s string) error {
				s = strings.TrimSpace(s)
				if s == "" || s == "default" {
					return ctrl.SetLLMTopK(nil)
				}
				v, err := strconv.Atoi(s)
				if err != nil {
					return fmt.Errorf("not an integer: %s", s)
				}
				return ctrl.SetLLMTopK(&v)
			},
		},
		{
			Label: "top_p", Kind: cfgKindFloat,
			Get: func() string {
				p := cfg.LLMTopP()
				if p == nil {
					return "(default)"
				}
				return strconv.FormatFloat(*p, 'g', -1, 64)
			},
			Apply: func(s string) error {
				s = strings.TrimSpace(s)
				if s == "" || s == "default" {
					return ctrl.SetLLMTopP(nil)
				}
				v, err := strconv.ParseFloat(s, 64)
				if err != nil {
					return fmt.Errorf("not a number: %s", s)
				}
				return ctrl.SetLLMTopP(&v)
			},
		},
		{
			Label: "auto_compact_threshold", Kind: cfgKindFloat,
			Get: func() string { return strconv.FormatFloat(cfg.AutoCompactThreshold, 'g', -1, 64) },
			Apply: func(s string) error {
				f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
				if err != nil {
					return fmt.Errorf("not a number: %s", s)
				}
				return cfg.SetAutoCompactThreshold(f)
			},
		},
		{
			Label: "display_thinking", Kind: cfgKindBool,
			Get: func() string { return strconv.FormatBool(cfg.GetDisplayThinking()) },
			Apply: func(s string) error {
				b, err := strconv.ParseBool(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not a bool: %s", s)
				}
				return cfg.SetDisplayThinking(b)
			},
		},
		{
			Label: "enable_auto_memory", Kind: cfgKindBool,
			Get: func() string { return strconv.FormatBool(cfg.GetEnableAutoMemory()) },
			Apply: func(s string) error {
				b, err := strconv.ParseBool(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not a bool: %s", s)
				}
				return cfg.SetEnableAutoMemory(b)
			},
		},
		{
			Label: "enable_memory_recall", Kind: cfgKindBool,
			Get: func() string { return strconv.FormatBool(cfg.GetEnableMemoryRecall()) },
			Apply: func(s string) error {
				b, err := strconv.ParseBool(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not a bool: %s", s)
				}
				return cfg.SetEnableMemoryRecall(b)
			},
		},
		{
			Label: "memory_recall_model", Kind: cfgKindString,
			Get:   func() string { return cfg.GetMemoryRecallModel() },
			Apply: func(s string) error { return cfg.SetMemoryRecallModel(strings.TrimSpace(s)) },
		},
		{
			Label: "fetch_max_bytes", Kind: cfgKindInt,
			Get: func() string { return strconv.Itoa(cfg.FetchMaxBytes) },
			Apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("not an integer: %s", s)
				}
				return cfg.SetFetchMaxBytes(n)
			},
		},
		{
			Label: "tavily_api_key", Kind: cfgKindSecret,
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

func providerKeyField(cfg *config.Config, name string) ConfigField {
	return ConfigField{
		Label: name + ".api_key", Kind: cfgKindSecret,
		Get:   func() string { return cfg.LLMProviderConfig[name].ApiSecret },
		Apply: func(s string) error { return cfg.SetProviderAPIKey(name, strings.TrimSpace(s)) },
	}
}

func providerURLField(cfg *config.Config, name string) ConfigField {
	return ConfigField{
		Label: name + ".api_url", Kind: cfgKindString,
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

// maskSecret renders a secret value for safe display.
func maskSecret(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// displayValue returns the user-facing string for a field's
// current value, applying mask for secrets and "(empty)" for blanks.
func displayValue(f ConfigField) string {
	v := f.Get()
	if f.Kind == cfgKindSecret {
		return maskSecret(v)
	}
	if v == "" {
		return "(empty)"
	}
	return v
}
