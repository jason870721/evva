package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestErrorMatchers_PinSDKShape is the canary against SDK error-format
// drift. isSessionExpired / isAuthError classify untyped SDK errors; if a
// minor SDK bump changes the wrapping or message wording, this test goes
// red and the matchers need updating. Without it, a silent reclassification
// would break retry-on-session-expired and the needs-auth flow.
func TestErrorMatchers_PinSDKShape(t *testing.T) {
	t.Run("session expired via ErrSessionMissing sentinel", func(t *testing.T) {
		// The streamable transport wraps a terminated session (HTTP 404) in
		// the exported ErrSessionMissing sentinel.
		wrapped := fmt.Errorf("call greet: failed to connect: %w", mcpsdk.ErrSessionMissing)
		if !isSessionExpired(wrapped) {
			t.Fatalf("isSessionExpired must match an ErrSessionMissing wrap")
		}
	})

	t.Run("session expired via 404/-32001 substring fallback", func(t *testing.T) {
		if !isSessionExpired(errors.New(`unexpected status 404: {"error":{"code":-32001}}`)) {
			t.Fatalf("isSessionExpired must match the 404/-32001 fallback shape")
		}
	})

	t.Run("non-session errors are not session-expired", func(t *testing.T) {
		if isSessionExpired(errors.New("connection refused")) {
			t.Fatalf("isSessionExpired false positive")
		}
		if isSessionExpired(nil) {
			t.Fatalf("isSessionExpired(nil) must be false")
		}
	})

	t.Run("auth error from a real 401 connect", func(t *testing.T) {
		// Drive a real StreamableClientTransport against a server that
		// answers 401 with no OAuth handler attached — the boot-connect
		// shape. Capture the SDK's error verbatim and assert isAuthError
		// fires on it.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "evva", Version: "test"}, nil)
		_, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
		if err == nil {
			t.Fatalf("connect against a 401 server should error")
		}
		if !isAuthError(err) {
			t.Fatalf("isAuthError must classify the SDK's 401 connect error; got %q", err.Error())
		}
	})
}
