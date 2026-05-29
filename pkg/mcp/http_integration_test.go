package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

type httpEchoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

// newHTTPEchoServer wraps an in-process echo MCP server in the SDK's
// Streamable HTTP handler and serves it via httptest, returning the URL.
func newHTTPEchoServer(t *testing.T) string {
	t.Helper()
	getServer := func(*http.Request) *mcpsdk.Server {
		s := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "echo", Version: "0.0.1"}, nil)
		mcpsdk.AddTool(s, &mcpsdk.Tool{Name: "echo", Description: "Echoes back the supplied text."},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, a httpEchoArgs) (*mcpsdk.CallToolResult, any, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + a.Text}}}, nil, nil
			})
		return s
	}
	handler := mcpsdk.NewStreamableHTTPHandler(getServer, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestHTTPIntegration covers acceptance A5: the full A1–A4 flow
// (discover → register → resolve → call) against a Streamable HTTP MCP
// server instead of a stdio one.
func TestHTTPIntegration(t *testing.T) {
	url := newHTTPEchoServer(t)

	cfg := &Config{Servers: []ServerConfig{{
		Name: "httpecho", Type: TransportStreamableHTTP, URL: url, Timeout: 30 * time.Second,
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr, warns := Open(ctx, cfg, OpenOptions{EvvaHome: t.TempDir()})
	for _, w := range warns {
		t.Logf("open warning: %v", w)
	}
	t.Cleanup(mgr.Shutdown)

	// A1: connected with one discovered tool.
	states := mgr.Status()
	if len(states) != 1 || states[0].Status != StatusConnected {
		t.Fatalf("HTTP server status = %+v, want one connected", states)
	}
	names := mgr.DiscoveredToolNames()
	want := "mcp__httpecho__echo"
	if len(names) != 1 || names[0] != want {
		t.Fatalf("DiscoveredToolNames = %v, want [%s]", names, want)
	}

	// A2/A3: factory registers; schema is fetchable.
	reg := pubtoolset.NewRegistry()
	mgr.RegisterFactories(reg)
	tool, err := reg.Build(tools.ToolName(want), nil)
	if err != nil {
		t.Fatalf("build %q: %v", want, err)
	}

	// A4: round-trip over HTTP.
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"text":"over-http"}`))
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v isErr=%v content=%q", err, res.IsError, res.Content)
	}
	if !strings.Contains(res.Content, "echo: over-http") {
		t.Fatalf("HTTP round-trip content = %q", res.Content)
	}
}
