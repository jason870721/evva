package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LspServerConfig describes one LSP server to manage.
type LspServerConfig struct {
	Command        string            `yaml:"command"`
	Args           []string          `yaml:"args"`
	Extensions     map[string]string `yaml:"extensions"` // ".go" → "go", ".ts" → "typescript"
	Env            map[string]string `yaml:"env"`
	StartupTimeout string            `yaml:"startupTimeout"` // "30s"
	MaxRestarts    int               `yaml:"maxRestarts"`    // default 3
}

// LspConfig is the top-level config file shape.
type LspConfig struct {
	Servers map[string]LspServerConfig `yaml:"servers"`
}

// LoadConfig reads project-level .evva/lsp_servers.yml with fallback to
// user-level ~/.evva/lsp_servers.yml. Project-level entries override
// user-level for the same server name. Returns a default empty config
// when neither file exists.
func LoadConfig(projectDir, userHome string) (*LspConfig, error) {
	merged := &LspConfig{Servers: make(map[string]LspServerConfig)}

	// Load user-level first (lowest priority).
	if userHome != "" {
		if cfg, err := loadFile(filepath.Join(userHome, ".evva", "lsp_servers.yml")); err == nil && cfg != nil {
			for name, srv := range cfg.Servers {
				merged.Servers[name] = expandConfig(srv)
			}
		}
	}

	// Project-level overrides.
	if projectDir != "" {
		if cfg, err := loadFile(filepath.Join(projectDir, ".evva", "lsp_servers.yml")); err == nil && cfg != nil {
			for name, srv := range cfg.Servers {
				merged.Servers[name] = expandConfig(srv)
			}
		}
	}

	// Auto-detect: if no servers were configured from files, try discovery.
	if len(merged.Servers) == 0 && projectDir != "" {
		merged.Servers = DiscoverServers(projectDir)
	}

	return merged, nil
}

// loadFile reads and unmarshals a single config file. Returns nil config
// when the file does not exist.
func loadFile(path string) (*LspConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg LspConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// expandConfig applies env-var expansion to the command, args, and env values.
func expandConfig(cfg LspServerConfig) LspServerConfig {
	out := cfg
	out.Command = os.ExpandEnv(cfg.Command)
	out.Args = make([]string, len(cfg.Args))
	for i, arg := range cfg.Args {
		out.Args[i] = os.ExpandEnv(arg)
	}
	if cfg.Env != nil {
		out.Env = make(map[string]string, len(cfg.Env))
		for k, v := range cfg.Env {
			out.Env[k] = os.ExpandEnv(v)
		}
	}
	return out
}

// parseDuration parses a duration string with a default fallback.
func parseDuration(s string, def time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	if d <= 0 {
		return def
	}
	return d
}
