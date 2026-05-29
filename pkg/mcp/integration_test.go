package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// buildEchoServer compiles testdata/stdio-echo-server into a temp binary
// and returns its path. Skips the test if the go toolchain is unavailable.
func buildEchoServer(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available; skipping stdio MCP integration test")
	}
	bin := filepath.Join(t.TempDir(), "echo-server")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "build", "-o", bin, "./testdata/stdio-echo-server")
	if out, berr := cmd.CombinedOutput(); berr != nil {
		t.Fatalf("build echo server: %v\n%s", berr, out)
	}
	return bin
}

func openEcho(t *testing.T) *Manager {
	t.Helper()
	bin := buildEchoServer(t)
	cfg := &Config{Servers: []ServerConfig{{
		Name:    "echo",
		Type:    TransportStdio,
		Command: bin,
		Timeout: 30 * time.Second,
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr, warns := Open(ctx, cfg, OpenOptions{EvvaHome: t.TempDir()})
	for _, w := range warns {
		t.Logf("open warning: %v", w)
	}
	t.Cleanup(mgr.Shutdown)
	return mgr
}

func TestStdioIntegration_Discovery(t *testing.T) {
	mgr := openEcho(t)

	states := mgr.Status()
	if len(states) != 1 {
		t.Fatalf("Status: got %d servers, want 1", len(states))
	}
	if states[0].Status != StatusConnected {
		t.Fatalf("server status = %q (err=%q), want connected", states[0].Status, states[0].Error)
	}
	if states[0].ToolCount != 1 {
		t.Fatalf("ToolCount = %d, want 1", states[0].ToolCount)
	}

	names := mgr.DiscoveredToolNames()
	want := "mcp__echo__echo"
	if len(names) != 1 || names[0] != want {
		t.Fatalf("DiscoveredToolNames = %v, want [%s]", names, want)
	}
}

func TestStdioIntegration_ToolRoundTrip(t *testing.T) {
	mgr := openEcho(t)

	reg := pubtoolset.NewRegistry()
	mgr.RegisterFactories(reg)

	name := tools.ToolName("mcp__echo__echo")
	if !reg.Has(name) {
		t.Fatalf("registry missing %q after RegisterFactories; has %v", name, reg.Names())
	}
	tool, err := reg.Build(name, nil)
	if err != nil {
		t.Fatalf("build %q: %v", name, err)
	}

	// Schema must be valid JSON the model can consume.
	var schema map[string]any
	if uerr := json.Unmarshal(tool.Schema(), &schema); uerr != nil {
		t.Fatalf("tool schema is not valid JSON object: %v", uerr)
	}

	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("execute returned IsError; content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "echo: hello") {
		t.Fatalf("result content = %q, want it to contain %q", res.Content, "echo: hello")
	}
}

func TestStdioIntegration_Resources(t *testing.T) {
	mgr := openEcho(t)

	// list_mcp_resources
	listTool := NewListResourcesTool(mgr)
	listRes, err := listTool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{}`))
	if err != nil || listRes.IsError {
		t.Fatalf("list_mcp_resources: err=%v isErr=%v content=%q", err, listRes.IsError, listRes.Content)
	}
	if !strings.Contains(listRes.Content, "echo://greeting") {
		t.Fatalf("list result missing resource uri; got %q", listRes.Content)
	}
	if !strings.Contains(listRes.Content, `"server": "echo"`) {
		t.Fatalf("list result missing server field; got %q", listRes.Content)
	}

	// read_mcp_resource
	readTool := NewReadResourceTool(mgr)
	readRes, err := readTool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"server":"echo","uri":"echo://greeting"}`))
	if err != nil || readRes.IsError {
		t.Fatalf("read_mcp_resource: err=%v isErr=%v content=%q", err, readRes.IsError, readRes.Content)
	}
	if !strings.Contains(readRes.Content, "hello from the echo server") {
		t.Fatalf("read result missing resource text; got %q", readRes.Content)
	}
}
