// Package configtool implements the `config` tool: a single model-facing
// handle on evva's runtime settings, mirroring what the interactive
// /config overlay exposes to the user.
//
// The package name is `configtool` (not `config`) so it doesn't collide
// with the pkg/config import it leans on for every read and write.
//
// SUPPORTED_SETTINGS is the single source of truth: the tool's behaviour,
// its prompt body (prompt.go), and the permission posture all derive from
// this one table. Adding a setting here grows all three at once.
package configtool

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/tools"
)

// SettingType discriminates how a value is coerced from JSON and rendered
// back for display.
type SettingType int

const (
	TypeString SettingType = iota
	TypeBool
	TypeInt
	TypeFloat
	TypeSecret // string, but masked on read
)

// SettingConfig describes one tunable setting the tool exposes.
//
// Get and Set are deliberately untyped (any in / out) so one map can mix
// integers, floats, booleans, strings, and provider secrets. Each entry's
// Type tells the dispatch code how to coerce the incoming JSON value
// before handing it to Set.
type SettingConfig struct {
	Type        SettingType
	Description string
	// Options, when non-nil, restricts the accepted set of values.
	// Applies to TypeString only.
	Options []string
	// Get reads the current value off cfg via a typed accessor so the read
	// is race-free against a concurrent Set.
	Get func(cfg *config.Config) any
	// Set persists the new value. Implementations coerce then delegate to
	// the typed setter on cfg (SetMaxIterations, …) so validation, locking,
	// and SaveFile all happen exactly once, in the setter that owns the field.
	Set func(cfg *config.Config, value any) error
	// FormatOnRead, when non-nil, transforms the raw value for display.
	// Used by TypeSecret to mask the value (see maskSecret).
	FormatOnRead func(value any) any
}

// SUPPORTED_SETTINGS is the model-facing setting catalog. It mirrors the
// /config overlay's buildConfigFields
// (pkg/ui/bubbletea/components/overlays/config.go), minus the session-only
// sampling knobs (temperature/top_k/top_p, which are never persisted and
// reset every boot), plus a small set of model-relevant settings the
// overlay doesn't surface (default_effort, default_profile).
//
// Adding a new setting? Update buildConfigFields too — the two surfaces
// describe the same user-visible matrix from different angles (interactive
// vs. LLM-callable). TestSupportedSettingsKeys pins the key set so drift is
// caught in review.
var SUPPORTED_SETTINGS = map[string]SettingConfig{
	"max_iterations": {
		Type:        TypeInt,
		Description: "Agent loop iteration cap; hitting it pauses for user continue",
		Get:         func(c *config.Config) any { return c.GetMaxIterations() },
		Set: func(c *config.Config, v any) error {
			n, err := coerceInt(v)
			if err != nil {
				return err
			}
			return c.SetMaxIterations(n)
		},
	},
	"max_tokens": {
		Type:        TypeInt,
		Description: "Per-completion output token cap; 0 lets the provider apply its default",
		Get:         func(c *config.Config) any { return c.GetMaxTokens() },
		Set: func(c *config.Config, v any) error {
			n, err := coerceInt(v)
			if err != nil {
				return err
			}
			return c.SetMaxTokens(n)
		},
	},
	"auto_compact_threshold": {
		Type:        TypeFloat,
		Description: "Fraction of context (0,1] at which auto-compaction triggers",
		Get:         func(c *config.Config) any { return c.GetAutoCompactThreshold() },
		Set: func(c *config.Config, v any) error {
			f, err := coerceFloat(v)
			if err != nil {
				return err
			}
			return c.SetAutoCompactThreshold(f)
		},
	},
	"display_thinking": {
		Type:        TypeBool,
		Description: "Show the model's reasoning trace in the TUI",
		Get:         func(c *config.Config) any { return c.GetDisplayThinking() },
		Set: func(c *config.Config, v any) error {
			b, err := coerceBool(v)
			if err != nil {
				return err
			}
			return c.SetDisplayThinking(b)
		},
	},
	"enable_auto_memory": {
		Type:        TypeBool,
		Description: "Enable the typed-memory directory: the prompt's memory guidance + MEMORY.md index, the write carve-out, and per-turn recall (next boot)",
		Get:         func(c *config.Config) any { return c.GetEnableAutoMemory() },
		Set: func(c *config.Config, v any) error {
			b, err := coerceBool(v)
			if err != nil {
				return err
			}
			return c.SetEnableAutoMemory(b)
		},
	},
	"enable_memory_recall": {
		Type:        TypeBool,
		Description: "Run the per-turn relevance side-query that surfaces stored memories (cost lever; turn off to keep the index but drop the extra completion per turn)",
		Get:         func(c *config.Config) any { return c.GetEnableMemoryRecall() },
		Set: func(c *config.Config, v any) error {
			b, err := coerceBool(v)
			if err != nil {
				return err
			}
			return c.SetEnableMemoryRecall(b)
		},
	},
	"memory_recall_model": {
		Type:        TypeString,
		Description: "Model id for the recall side-query; empty = a cheap model within the active provider (anthropic: sonnet, deepseek: flash, openai: gpt-5.4-mini at medium effort; ollama/other: the active model + effort)",
		Get:         func(c *config.Config) any { return c.GetMemoryRecallModel() },
		Set:         func(c *config.Config, v any) error { return c.SetMemoryRecallModel(toString(v)) },
	},
	"fetch_max_bytes": {
		Type:        TypeInt,
		Description: "Cap on the text web_fetch returns from one URL",
		Get:         func(c *config.Config) any { return c.GetFetchMaxBytes() },
		Set: func(c *config.Config, v any) error {
			n, err := coerceInt(v)
			if err != nil {
				return err
			}
			return c.SetFetchMaxBytes(n)
		},
	},
	"tavily_api_key": {
		Type:         TypeSecret,
		Description:  "Tavily API key for web_search; empty disables the tool",
		Get:          func(c *config.Config) any { return c.GetTavilyAPIKey() },
		Set:          func(c *config.Config, v any) error { return c.SetTavilyAPIKey(toString(v)) },
		FormatOnRead: maskSecret,
	},
	"default_effort": {
		Type:        TypeString,
		Description: "Thinking effort level used at boot; overridden at runtime by /effort",
		Options:     []string{"low", "medium", "high", "ultra"},
		Get:         func(c *config.Config) any { return c.Effort() },
		Set:         func(c *config.Config, v any) error { return c.SetDefaultEffort(toString(v)) },
	},
	"default_profile": {
		Type:        TypeString,
		Description: "Persona that boots on launch; must match a registered agent name. Empty = evva",
		Get:         func(c *config.Config) any { return c.GetDefaultProfile() },
		Set:         func(c *config.Config, v any) error { return c.SetDefaultProfile(toString(v)) },
	},
	// Provider settings (<provider>.api_key + <provider>.api_url) are added
	// by registerProviderSettings in init so the 8 near-identical entries
	// aren't hand-duplicated.
}

func init() {
	registerProviderSettings()
}

// registerProviderSettings adds <provider>.api_key + <provider>.api_url
// entries for every constant.GetAllProviders() entry. Ollama is local and
// unauthenticated, so it gets only an api_url entry.
func registerProviderSettings() {
	for _, p := range constant.GetAllProviders() {
		name := p.Name // per-iteration copy captured by the closures below
		if name != constant.OLLAMA.Name {
			SUPPORTED_SETTINGS[name+".api_key"] = SettingConfig{
				Type:         TypeSecret,
				Description:  fmt.Sprintf("%s API key; empty removes the provider from the active set", name),
				Get:          func(c *config.Config) any { return c.GetProviderAPIKey(name) },
				Set:          func(c *config.Config, v any) error { return c.SetProviderAPIKey(name, toString(v)) },
				FormatOnRead: maskSecret,
			}
		}
		SUPPORTED_SETTINGS[name+".api_url"] = SettingConfig{
			Type:        TypeString,
			Description: fmt.Sprintf("Override the %s API base URL; empty resets to the built-in default", name),
			Get:         func(c *config.Config) any { return c.GetProviderAPIURL(name) },
			Set:         func(c *config.Config, v any) error { return c.SetProviderAPIURL(name, toString(v)) },
		}
	}
}

// Names is the set of tool names this family contributes to a profile's
// ActiveTools. Currently just CONFIG.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.CONFIG}
}

// IsSupported reports whether key is a recognized setting.
func IsSupported(key string) bool {
	_, ok := SUPPORTED_SETTINGS[key]
	return ok
}

// Get returns the config for key, or the zero value + false.
func Get(key string) (SettingConfig, bool) {
	c, ok := SUPPORTED_SETTINGS[key]
	return c, ok
}

// AllKeys returns every supported setting key, sorted, for the prompt
// generator and stable iteration.
func AllKeys() []string {
	keys := make([]string, 0, len(SUPPORTED_SETTINGS))
	for k := range SUPPORTED_SETTINGS {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// coerceBool accepts true/false and "true"/"false" (case-insensitive),
// rejecting everything else with a clear error. Mirrors the ref
// ConfigTool's boolean coercion.
func coerceBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return false, fmt.Errorf("requires true or false")
}

// coerceInt accepts int, float64 (JSON numbers decode to float64 through
// any), and parseable strings. Non-integer floats are rejected rather than
// silently truncated.
func coerceInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case float64:
		if x != float64(int(x)) {
			return 0, fmt.Errorf("requires an integer, got %g", x)
		}
		return int(x), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return 0, fmt.Errorf("not an integer: %s", x)
		}
		return n, nil
	}
	return 0, fmt.Errorf("requires an integer, got %T", v)
}

// coerceFloat accepts float64, int, and parseable strings.
func coerceFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, fmt.Errorf("not a number: %s", x)
		}
		return f, nil
	}
	return 0, fmt.Errorf("requires a number, got %T", v)
}

// toString coerces any JSON-decoded scalar to its string representation.
// Used by string + secret settings.
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	return fmt.Sprint(v)
}

// maskSecret renders a secret value for safe display. Same shape as the
// /config overlay's maskSecret.
func maskSecret(v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return "(empty)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}
