package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpserve_test.go is MCP-3's acceptance test: `evva mcp-serve --transport
// stdio` launched as a real subprocess and driven by a real MCP client, the
// way Claude Desktop would launch it. Everything below the CLI is covered by
// pkg/mcp's own tests; what only a subprocess can prove is that the binary
// dispatches, that the allowlist is read from disk, that stdout carries
// nothing but JSON-RPC, and that a bad allowlist stops startup.

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// evvaBinary builds cmd/evva once per test run.
func evvaBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping: builds the evva binary")
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "evva-mcpserve-bin")
		if err != nil {
			buildErr = err
			return
		}
		name := "evva"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		out := filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", out, ".")
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("build output: %s", b)
			return
		}
		builtBin = out
	})
	if buildErr != nil {
		t.Fatalf("build evva: %v", buildErr)
	}
	return builtBin
}

// mcpServeEnv points the binary's AppHome at a temp dir (AppHome defaults to
// $HOME/.evva) and returns that home plus a project workdir.
func mcpServeEnv(t *testing.T, settings string) (env []string, workdir string) {
	t.Helper()
	home := t.TempDir()
	workdir = t.TempDir()
	if settings != "" {
		evvaHome := filepath.Join(home, ".evva")
		if err := os.MkdirAll(evvaHome, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evvaHome, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env = append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home, // windows
	)
	return env, workdir
}

func TestMCPServeStdioExposesAllowlistedTool(t *testing.T) {
	bin := evvaBinary(t)
	env, workdir := mcpServeEnv(t, `{"mcpServe":{"expose":[{"kind":"tool","name":"tree"}]}}`)

	// A file the exposed tool can actually find, so the call proves real
	// execution rather than an empty success.
	if err := os.WriteFile(filepath.Join(workdir, "marker.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.Command(bin, "mcp-serve", "--transport", "stdio")
	cmd.Env = env
	cmd.Dir = workdir
	cmd.Stderr = os.Stderr // diagnostics must not be on stdout; prove it by using stdout for JSON-RPC only

	sess, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "test"}, nil).
		Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to mcp-serve subprocess: %v", err)
	}
	defer func() { _ = sess.Close() }()

	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "tree" {
		var names []string
		for _, tool := range list.Tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("exposed %v, want exactly [tree]", names)
	}

	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "tree",
		Arguments: map[string]any{"path": workdir},
	})
	if err != nil {
		t.Fatalf("call tree: %v", err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if res.IsError || !strings.Contains(text.String(), "marker.txt") {
		t.Errorf("tree did not really run: IsError=%v content=%q", res.IsError, text.String())
	}
}

func TestMCPServeRefusesBadConfig(t *testing.T) {
	bin := evvaBinary(t)
	cases := []struct {
		name     string
		settings string
		want     string
	}{
		{
			// A server listening with nothing behind it is indistinguishable
			// from a misconfigured one.
			name: "nothing configured", settings: "",
			want: "nothing configured to expose",
		},
		{
			// Fails at startup, not at whatever hour someone first connects.
			name: "unknown persona", settings: `{"mcpServe":{"expose":[{"kind":"persona","name":"nope-not-real"}]}}`,
			want: "unknown persona",
		},
		{
			name: "unknown tool", settings: `{"mcpServe":{"expose":[{"kind":"tool","name":"grep_but_wrong"}]}}`,
			want: "read-oriented",
		},
		{
			// The v1 trust boundary: a persona may use bash under its own
			// permission gate; an external caller may not hold it directly.
			name: "dangerous tool", settings: `{"mcpServe":{"expose":[{"kind":"tool","name":"bash"}]}}`,
			want: "read-oriented",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, workdir := mcpServeEnv(t, tc.settings)
			cmd := exec.Command(bin, "mcp-serve", "--transport", "stdio")
			cmd.Env = env
			cmd.Dir = workdir
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("server started despite a bad allowlist; output:\n%s", out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("output = %q, want it to mention %q", out, tc.want)
			}
		})
	}
}

func TestMCPServeRejectsUnknownTransport(t *testing.T) {
	bin := evvaBinary(t)
	env, workdir := mcpServeEnv(t, `{"mcpServe":{"expose":[{"kind":"tool","name":"tree"}]}}`)
	cmd := exec.Command(bin, "mcp-serve", "--transport", "carrier-pigeon")
	cmd.Env = env
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("want a failure, got:\n%s", out)
	}
	if !strings.Contains(string(out), "unknown --transport") {
		t.Errorf("output = %q", out)
	}
}
