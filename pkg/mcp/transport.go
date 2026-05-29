package mcp

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildStdioTransport returns an SDK transport that spawns the
// configured subprocess. Env is merged with the parent process env
// (so things like PATH stay available) — explicit entries override
// inherited ones.
func buildStdioTransport(c ServerConfig) (mcpsdk.Transport, error) {
	if c.Command == "" {
		return nil, fmt.Errorf("stdio transport: command is empty")
	}
	cmd := exec.Command(c.Command, c.Args...)
	cmd.Env = mergeEnv(c.Env)
	return &mcpsdk.CommandTransport{Command: cmd}, nil
}

// buildStreamableHTTPTransport returns an SDK transport configured for
// the 2025-03-26 Streamable HTTP transport. The OAuth handler is nil on
// the first (boot) connect — an unauthenticated 401 lands the server in
// StatusNeedsAuth and the per-server authenticate tool drives a second
// connect with a live handler (see manager.go authenticate path).
func buildStreamableHTTPTransport(c ServerConfig, oauth auth.OAuthHandler) (mcpsdk.Transport, error) {
	if c.URL == "" {
		return nil, fmt.Errorf("http transport: url is empty")
	}
	t := &mcpsdk.StreamableClientTransport{Endpoint: c.URL}
	if oauth != nil {
		t.OAuthHandler = oauth
	}
	if len(c.Headers) > 0 {
		t.HTTPClient = &http.Client{Transport: headerRoundTripper{headers: c.Headers, base: http.DefaultTransport}}
	}
	return t, nil
}

// headerRoundTripper attaches static headers to every outgoing request.
// Used for HTTP MCP servers that authenticate via a fixed header (API
// keys, bearer tokens supplied in settings.json) rather than OAuth.
type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	return h.base.RoundTrip(req)
}

func mergeEnv(extra map[string]string) []string {
	base := os.Environ()
	for k, v := range extra {
		base = append(base, k+"="+v)
	}
	return base
}
