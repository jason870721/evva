package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/common"
)

// mcpserve_http.go is the Streamable HTTP transport for `evva mcp-serve`
// (MCP-4) — the persistent, remote-embeddable shape, as opposed to stdio's
// "my parent launched me and owns my lifetime".
//
// Auth follows the RP-15 pattern the swarm webapi established: mint a token at
// startup, write it to a 0600 file under AppHome, require it as a bearer on
// every request, and refuse a non-loopback bind unless the operator explicitly
// opted in. What it deliberately does NOT copy is RP-15's loopback-trust
// convenience — the swarm webapi lets a loopback peer skip the token so a
// browser can bootstrap. There is no browser here, every client is a
// configured program that can hold a token, and an unauthenticated local
// endpoint that runs whole agent turns is a bigger prize than a read-mostly
// dashboard. So the token is required always.

const (
	defaultMCPServeAddr = "127.0.0.1:8899"

	// mcpServeTokenName is the file under <AppHome>/mcp-serve/ clients read
	// the bearer token from.
	mcpServeTokenName = "token"

	// mcpServeShutdownGrace bounds how long a Ctrl-C waits for in-flight
	// calls. A persona call can legitimately run for minutes; the operator
	// asking to stop should not wait that long, and the SDK's session
	// teardown reports the cancellation to the client.
	mcpServeShutdownGrace = 5 * time.Second
)

// serveMCPHTTP runs srv behind the SDK's Streamable HTTP handler on addr until
// ctx is cancelled.
func serveMCPHTTP(ctx context.Context, srv *mcpsdk.Server, addr string, allowRemote bool, appHome string) error {
	loopback, err := addrIsLoopback(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if !loopback && !allowRemote {
		// The same refusal RP-15 makes: binding to the world is a decision the
		// operator states explicitly, never one a default drifts into.
		return fmt.Errorf("--addr %q is not loopback; pass --allow-remote to bind it deliberately", addr)
	}

	token := common.GenUUID()
	tokenFile, err := writeMCPServeToken(appHome, token)
	if err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	// The token file is per-run state, not config: leaving a stale one behind
	// would let a later reader believe a dead server is still reachable.
	defer func() { _ = os.Remove(tokenFile) }()

	handler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv },
		nil,
	)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mcpServeAuth(token, handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "evva mcp-serve: serving on http://%s\n", ln.Addr())
	fmt.Fprintf(os.Stderr, "evva mcp-serve: bearer token written to %s\n", tokenFile)
	if !loopback {
		fmt.Fprintln(os.Stderr, "evva mcp-serve: WARNING: bound to a non-loopback address — every reachable client can run the exposed personas with this token")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), mcpServeShutdownGrace)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
		return nil
	}
}

// mcpServeAuth requires a bearer token on every request.
//
// Comparison is constant-time: a token check that returns early on the first
// wrong byte leaks the token to anyone who can time requests.
func mcpServeAuth(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(mcpServeBearer(r))
		if len(got) == 0 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="evva"`)
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mcpServeBearer extracts the presented token from the Authorization header.
//
// Unlike the swarm webapi's bearer(), a ?token= query fallback is deliberately
// absent: that exists there so a browser WebSocket handshake (which cannot set
// headers) can authenticate. MCP clients are programs that set headers, and a
// token in a URL lands in proxy logs and shell history.
func mcpServeBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// writeMCPServeToken persists the token 0600 under <AppHome>/mcp-serve/ and
// returns the path.
func writeMCPServeToken(appHome, token string) (string, error) {
	if appHome == "" {
		return "", errors.New("no AppHome configured")
	}
	dir := filepath.Join(appHome, "mcp-serve")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, mcpServeTokenName)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// addrIsLoopback reports whether a bind address stays on the local machine.
//
// An empty host (":8899") binds every interface, which is NOT loopback — that
// is the exact footgun the --allow-remote gate exists to catch. A hostname is
// resolved, and counts as loopback only if every address it resolves to is.
func addrIsLoopback(addr string) (bool, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false, err
	}
	if port == "" {
		return false, errors.New("missing port")
	}
	if host == "" {
		return false, nil // ":8899" — all interfaces
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback(), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return false, fmt.Errorf("%q resolved to no addresses", host)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false, nil
		}
	}
	return true, nil
}
