package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- auth middleware --------------------------------------------------------

func TestMCPServeAuthRequiresBearer(t *testing.T) {
	var reached bool
	h := mcpServeAuth("secret-token", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		// Prefix-only must not pass: a naive HasPrefix comparison would let a
		// truncated token through.
		{"prefix of token", "Bearer secret", http.StatusUnauthorized},
		{"right token", "Bearer secret-token", http.StatusOK},
		// RFC 7235 says the scheme is case-insensitive.
		{"lowercase scheme", "bearer secret-token", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if got := reached; got != (tc.want == http.StatusOK) {
				t.Errorf("handler reached = %v for status %d", got, rec.Code)
			}
		})
	}
}

func TestMCPServeAuthRejectsQueryToken(t *testing.T) {
	// The swarm webapi accepts ?token= so a browser WebSocket handshake can
	// authenticate. There is no browser here, and a token in a URL lands in
	// proxy logs and shell history — so it must NOT be accepted.
	h := mcpServeAuth("secret-token", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/?token=secret-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("query-string token was accepted (status %d)", rec.Code)
	}
}

func TestMCPServeAuthChallengesOnMissingToken(t *testing.T) {
	h := mcpServeAuth("t", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", got)
	}
}

// --- bind-address gate ------------------------------------------------------

func TestAddrIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
		err  bool
	}{
		{"127.0.0.1:8899", true, false},
		{"[::1]:8899", true, false},
		{"localhost:8899", true, false},
		{"0.0.0.0:8899", false, false},
		{"192.168.1.10:8899", false, false},
		// ":8899" binds every interface. Treating an empty host as loopback is
		// exactly the footgun --allow-remote exists to catch.
		{":8899", false, false},
		{"127.0.0.1", false, true}, // no port
	}
	for _, tc := range cases {
		got, err := addrIsLoopback(tc.addr)
		if tc.err {
			if err == nil {
				t.Errorf("addrIsLoopback(%q): want an error", tc.addr)
			}
			continue
		}
		if err != nil {
			t.Errorf("addrIsLoopback(%q): %v", tc.addr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("addrIsLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestServeMCPHTTPRefusesRemoteBindWithoutOptIn(t *testing.T) {
	err := serveMCPHTTP(context.Background(), nil, "0.0.0.0:0", false, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "--allow-remote") {
		t.Fatalf("err = %v, want a refusal pointing at --allow-remote", err)
	}
}

func TestWriteMCPServeTokenIsPrivate(t *testing.T) {
	home := t.TempDir()
	path, err := writeMCPServeToken(home, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, "mcp-serve", "token") {
		t.Errorf("token path = %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "s3cret" {
		t.Fatalf("read back %q, %v", b, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not model unix permission bits.
	if perm := info.Mode().Perm(); perm&0o077 != 0 && os.Getenv("GOOS") != "windows" {
		t.Errorf("token file mode = %v, want no group/other access", perm)
	}
}

// --- end to end over HTTP ---------------------------------------------------

// TestMCPServeHTTPEndToEnd drives a real MCP client over the real Streamable
// HTTP handler behind the real auth middleware — the MCP-4 acceptance path.
func TestMCPServeHTTPEndToEnd(t *testing.T) {
	srv, err := buildProbeServer()
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv }, nil,
	)
	ts := httptest.NewServer(mcpServeAuth("good-token", handler))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Without a token the transport cannot even initialize.
	noAuth := &mcpsdk.StreamableClientTransport{Endpoint: ts.URL}
	if _, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "t"}, nil).
		Connect(ctx, noAuth, nil); err == nil {
		t.Error("connected without a bearer token")
	}

	// With it, the whole flow works.
	authed := &mcpsdk.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: "good-token"}},
	}
	sess, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "t"}, nil).
		Connect(ctx, authed, nil)
	if err != nil {
		t.Fatalf("connect with token: %v", err)
	}
	defer func() { _ = sess.Close() }()

	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "probe_tool" {
		t.Fatalf("tools = %+v", list.Tools)
	}

	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "probe_tool", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var got string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			got += tc.Text
		}
	}
	if got != "probe ok" {
		t.Errorf("content = %q", got)
	}
}

// buildProbeServer stands up a minimal MCP server so the HTTP test exercises
// transport + auth without depending on evva's tool registry.
func buildProbeServer() (*mcpsdk.Server, error) {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "evva", Version: "test"}, nil)
	s.AddTool(
		&mcpsdk.Tool{Name: "probe_tool", Description: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "probe ok"}}}, nil
		},
	)
	return s, nil
}

type bearerRoundTripper struct{ token string }

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(clone)
}
