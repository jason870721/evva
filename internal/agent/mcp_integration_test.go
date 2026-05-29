package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/mcp"
	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// openEchoManager builds the pkg/mcp echo-server fixture, opens an MCP
// manager against it under the given server name, and registers its
// factories on the default registry (mirroring what a host does). Returns
// the connected manager and the qualified echo tool name. A distinct
// serverName per test keeps the process-global DefaultRegistry from
// aliasing one test's (closed) client into another's factory.
func openEchoManager(t *testing.T, serverName string) (*mcp.Manager, string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available; skipping agent MCP integration test")
	}
	bin := filepath.Join(t.TempDir(), "echo-server")
	src := filepath.FromSlash("../../pkg/mcp/testdata/stdio-echo-server")
	if _, statErr := os.Stat(src); statErr != nil {
		t.Skipf("echo server fixture not found at %s: %v", src, statErr)
	}
	bctx, bcancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer bcancel()
	if out, berr := exec.CommandContext(bctx, goBin, "build", "-o", bin, src).CombinedOutput(); berr != nil {
		t.Fatalf("build echo server: %v\n%s", berr, out)
	}

	cfg := &mcp.Config{Servers: []mcp.ServerConfig{{
		Name: serverName, Type: mcp.TransportStdio, Command: bin, Timeout: 30 * time.Second,
	}}}
	octx, ocancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ocancel()
	mgr, warns := mcp.Open(octx, cfg, mcp.OpenOptions{EvvaHome: t.TempDir()})
	for _, w := range warns {
		t.Logf("mcp open warning: %v", w)
	}
	mgr.RegisterFactories(pubtoolset.DefaultRegistry())
	t.Cleanup(mgr.Shutdown)
	return mgr, mcp.BuildToolName(serverName, "echo")
}

// TestAgentMcp_DiscoveryAndPrompt covers A1–A4 at the agent layer: the
// discovered MCP tool lands in the deferred allowlist AND the rendered
// MAIN system prompt, tool_search can describe it, and resolving +
// executing it round-trips through the live server.
func TestAgentMcp_DiscoveryAndPrompt(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()
	mgr, echoToolName := openEchoManager(t, "echodisc")

	reg, _ := BuildAgentRegistry("")
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof,
		WithName("test"),
		WithAgentRegistry(reg),
		WithPersona("evva"),
		WithMcpManager(mgr),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	// A1/A3: the MCP tool is in the deferred allowlist (DeferredNames).
	if !slices.Contains(a.DeferredNames(), tools.ToolName(echoToolName)) {
		t.Fatalf("DeferredNames missing %q; got %v", echoToolName, a.DeferredNames())
	}

	// A2: the rendered MAIN prompt advertises it in the deferred block.
	if !strings.Contains(a.profile.SystemPrompt, echoToolName) {
		t.Fatalf("system prompt does not advertise %q", echoToolName)
	}

	// A3: tool_search can describe it (schema is fetchable).
	d, err := a.Describe(tools.ToolName(echoToolName))
	if err != nil {
		t.Fatalf("Describe(%q): %v", echoToolName, err)
	}
	if d.Name != echoToolName || len(d.Schema) == 0 {
		t.Fatalf("descriptor incomplete: %+v", d)
	}

	// A4: resolve + execute round-trips through the live server.
	tool, err := a.ResolveTool(tools.ToolName(echoToolName))
	if err != nil {
		t.Fatalf("ResolveTool: %v", err)
	}
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"text":"ping"}`))
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v isErr=%v content=%q", err, res.IsError, res.Content)
	}
	if !strings.Contains(res.Content, "echo: ping") {
		t.Fatalf("round-trip content = %q", res.Content)
	}
}

// TestAgentMcp_SwitchProfilePreserves pins acceptance A16: a /profile
// switch to a different main-tier persona keeps the MCP tools reachable —
// the manager lives on the reused ToolState, so no re-connect happens and
// DeferredNames still admits the MCP name.
func TestAgentMcp_SwitchProfilePreserves(t *testing.T) {
	seedDeepseek(t)
	cfg := config.Get()
	mgr, echoToolName := openEchoManager(t, "echoswitch")

	// Seed a second main-tier disk persona "nono".
	home := t.TempDir()
	nonoDir := filepath.Join(home, "agents", "nono")
	if err := os.MkdirAll(nonoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(nonoDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("system_prompt.md", "You are nono.\n")
	write("tools.yml", "active: [read]\ndeferred: []\n")
	write("meta.yml", "as: [main, subagent]\nwhen_to_use: finance.\n")

	reg, warns := BuildAgentRegistry(home)
	if len(warns) != 0 {
		t.Fatalf("registry warnings: %v", warns)
	}
	prof, err := ResolveMainProfile(cfg, reg, "evva", nil, memdir.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("ResolveMainProfile: %v", err)
	}
	a, err := New(nil, prof, WithName("test"), WithAgentRegistry(reg), WithPersona("evva"), WithMcpManager(mgr))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	prevDefault := cfg.DefaultProfile
	t.Cleanup(func() { _ = cfg.SetDefaultProfile(prevDefault) })

	if err := a.SwitchProfile("nono"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}
	if a.ProfileName() != "nono" {
		t.Fatalf("ProfileName = %q, want nono", a.ProfileName())
	}

	// A16: MCP tool still admitted after the switch (the deferred set
	// re-overlays MCP names onto whatever the new persona declared).
	// Disk personas don't render a deferred-tools block in their prompt
	// (ComposeDiskMainPrompt omits it by design — pre-existing), so the
	// load-bearing checks are DeferredNames + Describe + Execute.
	if !slices.Contains(a.DeferredNames(), tools.ToolName(echoToolName)) {
		t.Fatalf("post-switch DeferredNames missing %q; got %v", echoToolName, a.DeferredNames())
	}
	if _, err := a.Describe(tools.ToolName(echoToolName)); err != nil {
		t.Fatalf("post-switch Describe(%q): %v", echoToolName, err)
	}
	// And it still resolves + executes without a re-connect.
	tool, err := a.ResolveTool(tools.ToolName(echoToolName))
	if err != nil {
		t.Fatalf("post-switch ResolveTool: %v", err)
	}
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"text":"again"}`))
	if err != nil || res.IsError || !strings.Contains(res.Content, "echo: again") {
		t.Fatalf("post-switch execute: err=%v isErr=%v content=%q", err, res.IsError, res.Content)
	}
}
