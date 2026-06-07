package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Warning is a non-fatal load issue. Mirrors hooks.Warning shape so
// callers surface MCP warnings the same way they surface hook ones.
type Warning struct {
	Path string
	Err  error
}

func (w Warning) Error() string {
	if w.Path == "" {
		return w.Err.Error()
	}
	return fmt.Sprintf("%s: %v", w.Path, w.Err)
}

// Config is the merged + normalized server list ready for Open.
type Config struct {
	Servers []ServerConfig
}

// fileShape is the JSON shape under the "mcpServers" key. Each map
// entry is one server; the key is the server name. We accept both:
//
//	{ "mcpServers": { "fs": { "command": "...", "args": [...] } } }
//
// and (Claude Code-compatible) per-server "type":
//
//	{ "mcpServers": { "fs": { "type": "stdio", "command": "...", ... } } }
type fileShape struct {
	McpServers map[string]rawServer `json:"mcpServers"`
}

type rawServer struct {
	Type     string            `json:"type"`
	Disabled bool              `json:"disabled"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Timeout  int               `json:"timeout"` // seconds
}

// Load reads .evva/settings.json (project) and <evvaHome>/settings.json
// (user), merges the mcpServers blocks (project wins on name collision),
// expands env vars, and returns the normalized config + non-fatal
// warnings. Missing files are not errors. Malformed entries become
// Warnings; the rest of the file still loads.
func Load(workdir, evvaHome string) (*Config, []Warning) {
	cfg := &Config{}
	var warns []Warning
	byName := map[string]ServerConfig{}

	if evvaHome != "" {
		path := filepath.Join(evvaHome, "settings.json")
		warns = append(warns, loadOne(path, ScopeUser, byName)...)
	}
	if workdir != "" {
		path := filepath.Join(workdir, ".evva", "settings.json")
		warns = append(warns, loadOne(path, ScopeProject, byName)...)
	}

	for _, s := range byName {
		cfg.Servers = append(cfg.Servers, s)
	}
	return cfg, warns
}

// loadOne parses one settings.json, validates each server entry, expands
// env vars, and writes resulting ServerConfig values into byName.
// Project scope overwrites User scope entries by name (called second).
func loadOne(path string, scope ConfigScope, byName map[string]ServerConfig) []Warning {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []Warning{{Path: path, Err: err}}
	}
	var shape fileShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return []Warning{{Path: path, Err: fmt.Errorf("invalid json: %w", err)}}
	}

	var warns []Warning
	for name, rs := range shape.McpServers {
		cfg, ws := normalizeServer(path, name, scope, rs)
		warns = append(warns, ws...)
		if cfg == nil {
			continue
		}
		byName[name] = *cfg
	}
	return warns
}

// normalizeServer validates one rawServer entry. Returns nil and a
// Warning when the entry is unusable (missing required fields, bad
// type, invalid timeout). Env-var expansion failures are warnings but
// don't drop the entry — the server starts with the literal value and
// is likely to fail at connect, which is a more actionable error.
func normalizeServer(path, name string, scope ConfigScope, rs rawServer) (*ServerConfig, []Warning) {
	var warns []Warning
	cfg := &ServerConfig{
		Name:     name,
		Disabled: rs.Disabled,
		Scope:    scope,
		Headers:  rs.Headers,
	}

	t := strings.ToLower(strings.TrimSpace(rs.Type))
	// Default: if command is set → stdio; if url is set → http.
	if t == "" {
		if rs.Command != "" {
			t = "stdio"
		} else if rs.URL != "" {
			t = "http"
		}
	}
	switch t {
	case "stdio":
		if rs.Command == "" {
			warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: stdio requires command", name)})
			return nil, warns
		}
		cfg.Type = TransportStdio
		cfg.Env = map[string]string{}
		for k, v := range rs.Env {
			exp, missing := ExpandEnv(v)
			if len(missing) > 0 {
				warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s.env.%s: missing %v", name, k, missing)})
			}
			cfg.Env[k] = exp
		}
		// Expand command + args; keep literal on miss (connect will error
		// if it actually mattered — a more actionable failure).
		if expCmd, missing := ExpandEnv(rs.Command); len(missing) == 0 {
			cfg.Command = expCmd
		} else {
			cfg.Command = rs.Command
		}
		cfg.Args = make([]string, len(rs.Args))
		for i, a := range rs.Args {
			ea, _ := ExpandEnv(a)
			cfg.Args[i] = ea
		}
		// Container-leak footgun: `docker run` (or podman/nerdctl) without --rm
		// leaves an exited container behind on every connect, and a stdio server
		// reconnects on every evva launch — so they pile up by the hundreds. We
		// don't rewrite the user's invocation (it may be intentional or wrapped);
		// just flag it so the cause is visible in the startup warnings.
		if containerRunWithoutRm(cfg.Command, cfg.Args) {
			warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: %q runs a container without --rm — each evva launch leaves an exited container behind; add \"--rm\" to args", name, cfg.Command)})
		}
	case "http":
		if rs.URL == "" {
			warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: http requires url", name)})
			return nil, warns
		}
		cfg.Type = TransportStreamableHTTP
		cfg.URL = rs.URL
	default:
		warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: unknown type %q (want \"stdio\" or \"http\")", name, rs.Type)})
		return nil, warns
	}

	if rs.Timeout != 0 {
		if rs.Timeout < 1 || rs.Timeout > 600 {
			warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServers.%s: timeout %d out of range [1,600]", name, rs.Timeout)})
		} else {
			cfg.Timeout = time.Duration(rs.Timeout) * time.Second
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return cfg, warns
}

// containerRunWithoutRm reports whether a stdio command launches a container
// engine with `run` but no `--rm`. Matches docker / podman / nerdctl by the
// command's base name so a full path (e.g. /usr/bin/docker) still triggers.
// This is the single most common MCP container-leak cause, so it's worth a
// dedicated warning rather than leaving the user to discover it via `docker ps`.
func containerRunWithoutRm(command string, args []string) bool {
	switch filepath.Base(command) {
	case "docker", "podman", "nerdctl":
	default:
		return false
	}
	var hasRun, hasRm bool
	for _, a := range args {
		switch {
		case a == "run":
			hasRun = true
		case a == "--rm" || strings.HasPrefix(a, "--rm="):
			hasRm = true
		}
	}
	return hasRun && !hasRm
}
