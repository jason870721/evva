package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findServer(cfg *Config, name string) *ServerConfig {
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == name {
			return &cfg.Servers[i]
		}
	}
	return nil
}

func TestLoad_MissingFiles(t *testing.T) {
	cfg, warns := Load(t.TempDir(), t.TempDir())
	if cfg == nil || len(cfg.Servers) != 0 {
		t.Fatalf("missing files should yield empty cfg, got %+v", cfg)
	}
	if len(warns) != 0 {
		t.Fatalf("missing files should not warn, got %v", warns)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{ this is not json `)
	cfg, warns := Load("", home)
	if len(cfg.Servers) != 0 {
		t.Fatalf("malformed file should yield no servers")
	}
	if len(warns) == 0 {
		t.Fatalf("malformed file should produce a warning")
	}
}

func TestLoad_BothTransports(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{
	  "mcpServers": {
	    "fs":  {"type":"stdio","command":"echo","args":["hi"]},
	    "api": {"type":"http","url":"https://example.com/mcp","headers":{"X-K":"v"}}
	  }
	}`)
	cfg, warns := Load("", home)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	fs := findServer(cfg, "fs")
	if fs == nil || fs.Type != TransportStdio || fs.Command != "echo" || len(fs.Args) != 1 {
		t.Fatalf("fs server parse: %+v", fs)
	}
	api := findServer(cfg, "api")
	if api == nil || api.Type != TransportStreamableHTTP || api.URL != "https://example.com/mcp" {
		t.Fatalf("api server parse: %+v", api)
	}
	if api.Headers["X-K"] != "v" {
		t.Fatalf("headers not parsed: %+v", api.Headers)
	}
}

func TestLoad_TypeInference(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{
	  "mcpServers": {
	    "implied_stdio": {"command":"foo"},
	    "implied_http":  {"url":"https://x/mcp"}
	  }
	}`)
	cfg, _ := Load("", home)
	if s := findServer(cfg, "implied_stdio"); s == nil || s.Type != TransportStdio {
		t.Fatalf("stdio inference: %+v", s)
	}
	if s := findServer(cfg, "implied_http"); s == nil || s.Type != TransportStreamableHTTP {
		t.Fatalf("http inference: %+v", s)
	}
}

func TestLoad_EnvExpansion(t *testing.T) {
	t.Setenv("EVVA_CFG_TEST", "/work")
	home := t.TempDir()
	writeSettings(t, home, `{
	  "mcpServers": {
	    "fs": {"command":"server","args":["${EVVA_CFG_TEST}/dir"],"env":{"ROOT":"${EVVA_CFG_TEST}"}}
	  }
	}`)
	cfg, warns := Load("", home)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	fs := findServer(cfg, "fs")
	if fs.Args[0] != "/work/dir" {
		t.Fatalf("arg env not expanded: %q", fs.Args[0])
	}
	if fs.Env["ROOT"] != "/work" {
		t.Fatalf("env value not expanded: %q", fs.Env["ROOT"])
	}
}

func TestLoad_BadTimeoutWarns(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServers":{"fs":{"command":"x","timeout":9999}}}`)
	cfg, warns := Load("", home)
	if len(warns) == 0 {
		t.Fatalf("out-of-range timeout should warn")
	}
	// Entry still loads, with the default timeout applied.
	fs := findServer(cfg, "fs")
	if fs == nil || fs.Timeout != 30*1e9 {
		t.Fatalf("bad-timeout entry should fall back to 30s default: %+v", fs)
	}
}

func TestLoad_StdioRequiresCommand(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServers":{"bad":{"type":"stdio"}}}`)
	cfg, warns := Load("", home)
	if findServer(cfg, "bad") != nil {
		t.Fatalf("stdio with no command should be dropped")
	}
	if len(warns) == 0 {
		t.Fatalf("missing command should warn")
	}
}

func TestLoad_ProjectOverridesUser(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	writeSettings(t, home, `{"mcpServers":{"shared":{"command":"user-cmd"}}}`)
	writeSettings(t, filepath.Join(work, ".evva"), `{"mcpServers":{"shared":{"command":"project-cmd"}}}`)
	cfg, _ := Load(work, home)
	s := findServer(cfg, "shared")
	if s == nil || s.Command != "project-cmd" {
		t.Fatalf("project scope should win on name collision: %+v", s)
	}
	if s.Scope != ScopeProject {
		t.Fatalf("scope should be project, got %q", s.Scope)
	}
}

func TestLoad_Disabled(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServers":{"off":{"command":"x","disabled":true}}}`)
	cfg, _ := Load("", home)
	s := findServer(cfg, "off")
	if s == nil || !s.Disabled {
		t.Fatalf("disabled flag should parse: %+v", s)
	}
}

func TestLoad_DockerRunWithoutRmWarns(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServers":{"github":{"command":"docker","args":["run","-i","github-mcp-server:latest"]}}}`)
	cfg, warns := Load("", home)
	// The server still loads — the warning is advisory, not fatal.
	if s := findServer(cfg, "github"); s == nil {
		t.Fatalf("docker server should still load")
	}
	if len(warns) == 0 {
		t.Fatalf("docker run without --rm should warn")
	}
	if !strings.Contains(warns[0].Error(), "--rm") {
		t.Fatalf("warning should mention --rm, got %q", warns[0].Error())
	}
}

func TestLoad_DockerRunWithRmNoWarn(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServers":{"github":{"command":"docker","args":["run","-i","--rm","github-mcp-server:latest"]}}}`)
	_, warns := Load("", home)
	if len(warns) != 0 {
		t.Fatalf("docker run with --rm should not warn, got %v", warns)
	}
}

func TestContainerRunWithoutRm(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{"docker run no rm", "docker", []string{"run", "-i", "img"}, true},
		{"docker run with rm", "docker", []string{"run", "-i", "--rm", "img"}, false},
		{"docker run rm=true", "docker", []string{"run", "--rm=true", "img"}, false},
		{"podman run no rm", "podman", []string{"run", "img"}, true},
		{"nerdctl run no rm", "nerdctl", []string{"run", "img"}, true},
		{"full path docker", "/usr/bin/docker", []string{"run", "img"}, true},
		{"non-container command", "github-mcp-server", []string{"stdio"}, false},
		{"docker but not run", "docker", []string{"ps"}, false},
		{"npx (not a container)", "npx", []string{"-y", "some-server"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containerRunWithoutRm(c.command, c.args); got != c.want {
				t.Fatalf("containerRunWithoutRm(%q, %v) = %v, want %v", c.command, c.args, got, c.want)
			}
		})
	}
}
