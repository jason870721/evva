package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// OAuthPrompt is the data an MCP-driven OAuth flow needs to surface to the
// end user. The host receives this struct via OAuthPromptFn, shows it to
// the user however it sees fit (TUI dialog, ask_user_question, custom UI,
// headless allow-list), and returns whether the user completed the
// in-browser flow or cancelled.
//
// pkg/mcp deliberately does NOT import internal/question. Callers adapt
// the OAuthPromptFn signature into whatever question/broker shape their
// host uses; the bundled cmd/evva does this in
// internal/agent/mcp_wiring.go.
type OAuthPrompt struct {
	Server  string // MCP server name from settings.json
	AuthURL string // URL the user must open in their browser
}

// OAuthPromptResult is the user's decision after seeing an OAuthPrompt.
type OAuthPromptResult int

const (
	// OAuthCompleted means the user reports the browser flow finished; the
	// local callback listener has captured the code+state.
	OAuthCompleted OAuthPromptResult = iota
	// OAuthCancelled aborts the connect — the server stays needs-auth until
	// the user retries via the per-server authenticate tool.
	OAuthCancelled
)

// OAuthPromptFn is the seam the host installs to surface auth URLs to the
// user. Returning OAuthCancelled aborts the auth. Returning a non-nil
// error is treated as a transport failure — the connect fails and the
// server's status moves to failed/needs-auth.
type OAuthPromptFn func(ctx context.Context, prompt OAuthPrompt) (OAuthPromptResult, error)

// OAuthHandler wraps the SDK's auth.AuthorizationCodeHandler with one
// piece of evva-specific glue: the URL the SDK derives from the server's
// OAuth metadata is routed through promptFn so a human (or any other
// prompt sink the host chose) can confirm completion. A local HTTP
// listener captures the authorization-code redirect out of band, as the
// SDK's AuthorizationCodeFetcher contract requires.
type OAuthHandler struct {
	serverName string
	logger     *slog.Logger
	promptFn   OAuthPromptFn

	// callbackTimeout bounds how long fetchCode waits for the browser
	// redirect after the user confirms completion. Default 2m.
	callbackTimeout time.Duration
}

// NewOAuthHandler builds a handler for one server. promptFn may be nil —
// fetchCode then returns a clear "no prompt callback installed" error.
func NewOAuthHandler(server string, logger *slog.Logger, promptFn OAuthPromptFn) *OAuthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &OAuthHandler{
		serverName:      server,
		logger:          logger,
		promptFn:        promptFn,
		callbackTimeout: 2 * time.Minute,
	}
}

// SDKHandler returns the SDK-shaped handler set on
// StreamableClientTransport.OAuthHandler. It stands up a local callback
// listener on 127.0.0.1:<random-port> so the authorization-code redirect
// can be captured; the redirect URI is registered via Dynamic Client
// Registration. The listener is closed when fetchCode returns (or after a
// backstop timeout if the flow never reaches the fetcher).
func (h *OAuthHandler) SDKHandler() (auth.OAuthHandler, error) {
	if h.promptFn == nil {
		return nil, errors.New("mcp oauth: no prompt callback installed; cannot surface auth URL to user")
	}
	cb, err := newCallbackListener()
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: start callback listener: %w", err)
	}
	redirectURL := cb.redirectURL()

	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: redirectURL,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs: []string{redirectURL},
				ClientName:   "evva",
			},
		},
		AuthorizationCodeFetcher: h.fetcherWith(cb),
	})
	if err != nil {
		cb.close()
		return nil, fmt.Errorf("mcp oauth: build handler: %w", err)
	}
	return handler, nil
}

// fetcherWith returns the AuthorizationCodeFetcher bound to a callback
// listener. The fetcher surfaces the auth URL via the prompt, then reads
// the code/state the listener captured from the browser redirect.
func (h *OAuthHandler) fetcherWith(cb *callbackListener) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		defer cb.close()

		res, err := h.runPrompt(ctx, args.URL)
		if err != nil {
			return nil, fmt.Errorf("mcp oauth: prompt: %w", err)
		}
		if res == OAuthCancelled {
			return nil, errors.New("mcp oauth: user cancelled auth")
		}

		// The browser redirect should have already hit the callback listener
		// by the time the user confirms completion. Wait briefly for it.
		select {
		case got := <-cb.result:
			if got.err != nil {
				return nil, got.err
			}
			return &auth.AuthorizationResult{Code: got.code, State: got.state}, nil
		case <-time.After(h.callbackTimeout):
			return nil, errors.New("mcp oauth: timed out waiting for the authorization redirect")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// runPrompt invokes the host prompt for one auth URL. Split out from the
// fetcher so the prompt-decision contract is unit-testable without
// standing up a real OAuth server.
func (h *OAuthHandler) runPrompt(ctx context.Context, authURL string) (OAuthPromptResult, error) {
	if h.promptFn == nil {
		return OAuthCancelled, errors.New("mcp oauth: no prompt callback installed")
	}
	return h.promptFn(ctx, OAuthPrompt{Server: h.serverName, AuthURL: authURL})
}

// callbackListener captures a single OAuth authorization-code redirect.
type callbackListener struct {
	ln     net.Listener
	srv    *http.Server
	result chan callbackResult
}

type callbackResult struct {
	code  string
	state string
	err   error
}

func newCallbackListener() (*callbackListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	cb := &callbackListener{
		ln:     ln,
		result: make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			cb.deliver(callbackResult{err: fmt.Errorf("authorization server returned error: %s", e)})
			http.Error(w, "authorization failed: "+e, http.StatusBadRequest)
			return
		}
		cb.deliver(callbackResult{code: q.Get("code"), state: q.Get("state")})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h3>Authorization complete.</h3>You can close this window and return to evva.</body></html>"))
	})
	cb.srv = &http.Server{Handler: mux}
	go func() { _ = cb.srv.Serve(ln) }()
	return cb, nil
}

func (c *callbackListener) deliver(r callbackResult) {
	select {
	case c.result <- r:
	default:
	}
}

func (c *callbackListener) redirectURL() string {
	return "http://" + c.ln.Addr().String() + "/callback"
}

func (c *callbackListener) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.srv.Shutdown(ctx)
}
