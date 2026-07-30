package main

import (
	"bufio"
	"context"
	"net/http"
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

// TestMCPServeHTTPSubprocess is MCP-4's acceptance path end to end: the real
// binary, the real HTTP transport, the real minted token, driven by a real MCP
// client. Port 0 lets the OS pick, and the bind address is read back from the
// process's own stderr announcement.
func TestMCPServeHTTPSubprocess(t *testing.T) {
	bin := evvaBinary(t)
	env, workdir := mcpServeEnv(t, `{"mcpServe":{"expose":[{"kind":"tool","name":"tree"}]}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "mcp-serve", "--transport", "http", "--addr", "127.0.0.1:0")
	cmd.Env = env
	cmd.Dir = workdir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// The server announces its real bind address and token path on stderr.
	//
	// The condition is checked BEFORE scanning, not after: the server goes
	// quiet once it is listening, so a trailing Scan() would block until the
	// process died and silently consume the whole test deadline.
	var endpoint, tokenPath string
	sc := bufio.NewScanner(stderr)
	for endpoint == "" || tokenPath == "" {
		if !sc.Scan() {
			break
		}
		line := sc.Text()
		t.Logf("server: %s", line)
		if _, rest, ok := strings.Cut(line, "serving on "); ok {
			endpoint = strings.TrimSpace(rest)
		}
		if _, rest, ok := strings.Cut(line, "token written to "); ok {
			tokenPath = strings.TrimSpace(rest)
		}
	}
	if endpoint == "" || tokenPath == "" {
		t.Fatalf("server did not announce its endpoint/token (endpoint=%q token=%q)", endpoint, tokenPath)
	}
	go func() {
		for sc.Scan() { // drain, so the pipe never blocks the server
		}
		_ = sc.Err() // the pipe closing when we kill the process is expected
	}()

	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read minted token: %v", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		t.Fatal("minted token file is empty")
	}

	// Unauthenticated first: the endpoint must not serve a stranger. This gets
	// its own short budget — the SDK client retries a rejected handshake, so
	// sharing the outer deadline would let the probe consume it all.
	probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
	_, unauthErr := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "t"}, nil).
		Connect(probeCtx, &mcpsdk.StreamableClientTransport{Endpoint: endpoint}, nil)
	probeCancel()
	if unauthErr == nil {
		t.Error("connected to the HTTP transport without a token")
	}

	sess, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "t"}, nil).
		Connect(ctx, &mcpsdk.StreamableClientTransport{
			Endpoint:   endpoint,
			HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
		}, nil)
	if err != nil {
		t.Fatalf("connect with the minted token: %v", err)
	}
	defer func() { _ = sess.Close() }()

	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "tree" {
		t.Errorf("tools = %+v, want exactly [tree]", list.Tools)
	}
}

// TestMCPServeHTTPRefusesRemoteBind proves the gate holds in the real binary,
// not just in the unit test of the helper.
func TestMCPServeHTTPRefusesRemoteBind(t *testing.T) {
	bin := evvaBinary(t)
	env, workdir := mcpServeEnv(t, `{"mcpServe":{"expose":[{"kind":"tool","name":"tree"}]}}`)
	cmd := exec.Command(bin, "mcp-serve", "--transport", "http", "--addr", "0.0.0.0:0")
	cmd.Env = env
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bound a non-loopback address without --allow-remote:\n%s", out)
	}
	if !strings.Contains(string(out), "--allow-remote") {
		t.Errorf("output = %q", out)
	}
}
