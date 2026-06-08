package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnny1110/evva/pkg/constant"
	"gopkg.in/yaml.v3"
)

// FileConfig is the on-disk schema for $EvvaHome/config/evva-config.yml.
// It owns the user-tunable subset of configuration; deployment knobs
// (LOG_LEVEL, APP_ENV, ...) stay in .env.
type FileConfig struct {
	MaxIterations        int     `yaml:"max_iterations"`
	MaxTokens            int     `yaml:"max_tokens"`
	AutoCompactThreshold float64 `yaml:"auto_compact_threshold"`
	DisplayThinking      bool    `yaml:"display_thinking"`

	// DefaultProvider / DefaultModel are the (provider, model) pair the
	// agent boots with. Phase 3's /model switch will mutate these and call
	// Save to persist across launches.
	DefaultProvider string `yaml:"default_provider"`
	DefaultModel    string `yaml:"default_model"`

	DefaultEffort string `yaml:"default_effort"`

	// DefaultProfile is the persona the root agent boots into. Phase 6's
	// /profile switch mutates this and calls Save to persist across launches.
	// Empty falls back to "evva" at bootstrap.
	DefaultProfile string `yaml:"default_profile"`

	// PermissionMode is the agent's startup stance. One of:
	// "default" | "accept_edits" | "plan" | "bypass" | "auto". Defaults to
	// "default" when omitted. The -permission-mode CLI flag overrides this.
	PermissionMode string `yaml:"permission_mode"`

	FetchMaxBytes int    `yaml:"fetch_max_bytes"`
	TavilyAPIKey  string `yaml:"tavily_api_key"`

	// EnableAutoMemory gates the typed-memory subsystem (the per-session prompt
	// guidance + MEMORY.md index, the write carve-out, and per-turn recall).
	// Default true; users opt out via /config or by setting this to false.
	// Pointer so a missing key in YAML preserves the default rather than zeroing.
	EnableAutoMemory *bool `yaml:"enable_auto_memory,omitempty"`

	// EnableMemoryRecall gates the per-turn relevance side-query that surfaces
	// stored memories matching the current prompt. Default true (when auto-memory
	// is on); the cost-sensitive escape hatch — set false to keep the MEMORY.md
	// index but drop the extra completion per turn. Pointer to preserve the
	// default on a missing key.
	EnableMemoryRecall *bool `yaml:"enable_memory_recall,omitempty"`

	// MemoryRecallModel optionally pins the model used for the recall side-query.
	// Empty → a cheap model within the active provider (anthropic: sonnet,
	// deepseek: flash, openai: gpt-5.4-mini at medium effort; ollama/other: the
	// active model + the main agent's effort). Set a specific model here to
	// override, e.g. a different cost lever or any model whose provider you have a
	// key for. Unknown values are ignored (fall back to the default resolution).
	MemoryRecallModel string `yaml:"memory_recall_model,omitempty"`

	Providers map[string]FileProviderConfig `yaml:"providers"`

	// Custom is the downstream-app extension slot. Values round-trip through
	// YAML as the `custom:` section. Empty / nil produces no `custom:` key in
	// the output. Decoded as map[string]any — consumers cast at use-site.
	Custom map[string]any `yaml:"custom,omitempty"`
}

// FileProviderConfig carries per-provider credentials. Empty ApiURL falls
// back to the constant's built-in default.
type FileProviderConfig struct {
	APIKey string `yaml:"api_key"`
	APIURL string `yaml:"api_url"`
}

// defaultFileConfig returns first-launch defaults stamped with the
// caller's AppName as default_profile. friday-flavoured config writes
// `default_profile: friday` instead of leaking evva's persona name into
// a sibling app's YAML. Empty appName falls back to "evva".
func defaultFileConfig(appName string) FileConfig {
	if appName == "" {
		appName = "evva"
	}
	enableAutoMem := true
	enableMemRecall := true
	return FileConfig{
		MaxIterations:        30,
		MaxTokens:            4096,
		AutoCompactThreshold: 0.8,
		DisplayThinking:      true,

		DefaultProvider: constant.DEEPSEEK.Name,
		DefaultModel:    string(constant.DEEPSEEK_V4_PRO),
		DefaultEffort:   "medium",
		DefaultProfile:  appName,
		PermissionMode:  "default",

		FetchMaxBytes: 100000,
		TavilyAPIKey:  "",

		EnableAutoMemory:   &enableAutoMem,
		EnableMemoryRecall: &enableMemRecall,

		Providers: map[string]FileProviderConfig{
			constant.ANTHROPIC.Name: {},
			constant.DEEPSEEK.Name:  {},
			constant.OPENAI.Name:    {},
			constant.OLLAMA.Name:    {},
		},
	}
}

// LoadFileConfig reads the YAML at path. On first launch (file absent)
// it writes a default YAML whose default_profile is the caller's
// appName — so a friday-flavoured Load writes "default_profile: friday"
// instead of bleeding evva's persona into a sibling app's config.
//
// Returns (cfg, created, err):
//   - created=true means the file didn't exist and was just written with
//     defaults; callers can use this to surface a one-time first-launch
//     notice.
//   - Missing keys in an existing file fall back to defaultFileConfig
//     values via pre-population, so partial YAML never crashes startup.
func LoadFileConfig(path, appName string) (FileConfig, bool, error) {
	cfg := defaultFileConfig(appName)

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return cfg, false, fmt.Errorf("config: read %s: %w", path, err)
		}
		// Brand new install. Create the directory and seed the file.
		if mkerr := os.MkdirAll(filepath.Dir(path), 0o755); mkerr != nil {
			return cfg, false, fmt.Errorf("config: mkdir %s: %w", filepath.Dir(path), mkerr)
		}
		if werr := SaveFileConfig(path, cfg); werr != nil {
			return cfg, false, werr
		}
		return cfg, true, nil
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, false, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, false, nil
}

// SaveFileConfig writes cfg to path atomically (temp file + rename) so a
// crash mid-write never leaves a truncated YAML behind.
func SaveFileConfig(path string, cfg FileConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".evva-config-*.yml")
	if err != nil {
		return fmt.Errorf("config: temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("config: write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("config: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("config: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// ResolveDefaultModel parses the (provider name, model name) pair from
// the YAML and returns the typed constants. Validates that the model is
// actually one the provider lists — a typo or a model/provider mismatch
// fails fast at startup with a clear message rather than a confusing
// runtime "unknown model" from the LLM API.
func ResolveDefaultModel(provider, model string) (constant.LLMProvider, constant.Model, error) {
	pvd, ok := constant.GetProvider(provider)
	if !ok {
		names := make([]string, 0, len(constant.GetAllProviders()))
		for _, p := range constant.GetAllProviders() {
			names = append(names, p.Name)
		}
		return constant.LLMProvider{}, "", fmt.Errorf(
			"config: unknown default_provider %q; valid: %v", provider, names)
	}
	m, ok := constant.GetModel(model)
	if !ok {
		return pvd, "", fmt.Errorf("config: unknown default_model %q", model)
	}
	for _, mm := range pvd.Models {
		if mm == m {
			return pvd, m, nil
		}
	}
	offered := make([]string, len(pvd.Models))
	for i, mm := range pvd.Models {
		offered[i] = string(mm)
	}
	return pvd, "", fmt.Errorf(
		"config: model %q not offered by provider %q; valid: %v",
		model, provider, offered)
}
