package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHealthz boots the service on an ephemeral port and asserts the M0
// contract: GET /healthz -> 200 "ok", and GET / serves the embedded SPA
// placeholder (proving the web.Dist embed is wired end to end).
func TestHealthz(t *testing.T) {
	svc := New("127.0.0.1:0")
	if err := svc.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- svc.Serve(ctx) }()

	base := "http://" + svc.Addr()
	client := &http.Client{Timeout: 2 * time.Second}

	// /healthz -> 200 "ok"
	resp := mustGet(t, client, base+"/healthz")
	if resp.code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.code)
	}
	if resp.body != "ok" {
		t.Fatalf("/healthz body = %q, want %q", resp.body, "ok")
	}

	// / -> embedded SPA placeholder
	root := mustGet(t, client, base+"/")
	if root.code != http.StatusOK {
		t.Fatalf("/ status = %d, want 200", root.code)
	}
	if !strings.Contains(root.body, "evva") {
		t.Fatalf("/ body did not contain the embedded SPA placeholder (got %d bytes)", len(root.body))
	}

	// Cancelling the context drains the server cleanly.
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}

type httpResult struct {
	code int
	body string
}

func mustGet(t *testing.T, c *http.Client, url string) httpResult {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", url, err)
	}
	return httpResult{code: resp.StatusCode, body: string(b)}
}
