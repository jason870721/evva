package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/tools"
	"time"

	config "github.com/johnny1110/evva/pkg/config"
)

// Phase 1 analysis — FetchTool.Execute code paths:
//   - Decode JSON input
//   - Empty / invalid / wrong-scheme URL rejected
//   - http:// auto-upgrades to https:// (inspected by capturing the request)
//   - GET with User-Agent + Accept; non-2xx → IsError
//   - text/html* → extractReadableText; other content types → as-is
//   - Truncation when extracted text exceeds FetchMaxBytes
//   - Header line prepended

// newTestHTTPSServer spins up a TLS test server and points the
// package-level fetchHTTPClient at the server's TLS-aware client.
// FetchTool unconditionally upgrades http:// to https:// before
// dialing, so plain httptest.NewServer doesn't work — we need a TLS
// origin and a client that trusts the test server's cert.
func newTestHTTPSServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	prev := fetchHTTPClient
	c := srv.Client()
	c.Timeout = 2 * time.Second
	fetchHTTPClient = c
	t.Cleanup(func() {
		fetchHTTPClient = prev
		srv.Close()
	})
	return srv
}

func setFetchMaxBytes(t *testing.T, n int) {
	t.Helper()
	cfg := config.Get()
	prev := cfg.FetchMaxBytes
	cfg.FetchMaxBytes = n
	t.Cleanup(func() { cfg.FetchMaxBytes = prev })
}

func TestFetch_RejectsEmptyURL(t *testing.T) {
	tool := NewFetch(config.Get())
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"url":"  "}`))
	if !res.IsError || !strings.Contains(res.Content, "required") {
		t.Fatalf("expected 'required' error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestFetch_RejectsInvalidURL(t *testing.T) {
	tool := NewFetch(config.Get())
	// "::not a url" has no host after url.Parse
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"url":"::not a url"}`))
	if !res.IsError {
		t.Fatal("expected IsError for malformed URL")
	}
	if !strings.Contains(res.Content, "invalid url") {
		t.Errorf("expected 'invalid url' in error; got %q", res.Content)
	}
}

func TestFetch_RejectsUnsupportedScheme(t *testing.T) {
	tool := NewFetch(config.Get())
	// Use a URL that parses cleanly (has host) so we reach the scheme
	// check rather than tripping the empty-host branch.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"url":"ftp://example.com/x"}`))
	if !res.IsError {
		t.Fatal("expected IsError for non-http(s) scheme")
	}
	if !strings.Contains(res.Content, "unsupported scheme") {
		t.Errorf("expected 'unsupported scheme'; got %q", res.Content)
	}
}

func TestFetch_HappyPath_HTMLExtraction(t *testing.T) {
	srv := newTestHTTPSServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != fetchUserAgent {
			t.Errorf("User-Agent: got %q, want %q", got, fetchUserAgent)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><script>alert(1)</script><p>Hello <a href="https://example.com">link</a></p></body></html>`))
	})
	setFetchMaxBytes(t, 100_000)

	tool := NewFetch(config.Get())
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(fmt.Sprintf(`{"url":%q}`, srv.URL)))
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError; content=%s", res.Content)
	}

	wantSubstrings := []string{
		"[Fetched: " + srv.URL + " (text/html, ",
		"Hello",
		"link (https://example.com)",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(res.Content, s) {
			t.Errorf("missing %q\nfull:\n%s", s, res.Content)
		}
	}
	if strings.Contains(res.Content, "alert(1)") {
		t.Error("script content leaked into extraction")
	}
}

func TestFetch_PlainTextReturnedAsIs(t *testing.T) {
	body := "line1\nline2\nline3"
	srv := newTestHTTPSServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	})
	setFetchMaxBytes(t, 100_000)

	tool := NewFetch(config.Get())
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(fmt.Sprintf(`{"url":%q}`, srv.URL)))

	if res.IsError {
		t.Fatalf("unexpected IsError; content=%s", res.Content)
	}
	if !strings.Contains(res.Content, body) {
		t.Errorf("plain-text body missing; full:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "(text/plain") {
		t.Errorf("header should mention text/plain; full:\n%s", res.Content)
	}
}

func TestFetch_Non2xxIsError(t *testing.T) {
	srv := newTestHTTPSServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	setFetchMaxBytes(t, 100_000)

	tool := NewFetch(config.Get())
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(fmt.Sprintf(`{"url":%q}`, srv.URL)))

	if !res.IsError {
		t.Fatal("expected IsError on 404")
	}
	if !strings.Contains(res.Content, "404") {
		t.Errorf("expected '404' in error; got %q", res.Content)
	}
}

func TestFetch_TruncationMarkerAppended(t *testing.T) {
	// Server returns plain text larger than FetchMaxBytes — the tool
	// should clip it and append the marker. FetchMaxBytes is small so
	// the truncation triggers without ballooning the test fixture.
	const maxBytes = 50
	body := strings.Repeat("x", maxBytes*4) // well past the cap
	srv := newTestHTTPSServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	})
	setFetchMaxBytes(t, maxBytes)

	tool := NewFetch(config.Get())
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(fmt.Sprintf(`{"url":%q}`, srv.URL)))

	if res.IsError {
		t.Fatalf("unexpected IsError; content=%s", res.Content)
	}
	if !strings.Contains(res.Content, "[truncated") {
		t.Errorf("expected truncation marker; got\n%s", res.Content)
	}
}

func TestFetch_ContextCancelled(t *testing.T) {
	srv := newTestHTTPSServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	setFetchMaxBytes(t, 100_000)

	tool := NewFetch(config.Get())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := tool.Execute(ctx, tools.NopLogger(), json.RawMessage(fmt.Sprintf(`{"url":%q}`, srv.URL)))

	if err == nil {
		t.Fatal("expected go-level err on cancelled ctx")
	}
	if !res.IsError {
		t.Fatal("expected IsError on cancelled ctx")
	}
}

func TestFetch_isHTMLContentType(t *testing.T) {
	cases := map[string]bool{
		"text/html":                  true,
		"text/html; charset=utf-8":   true,
		"application/xhtml+xml":      true,
		"  TEXT/HTML  ":              true,
		"text/plain":                 false,
		"application/json":           false,
		"":                           false,
	}
	for ct, want := range cases {
		t.Run(ct, func(t *testing.T) {
			if got := isHTMLContentType(ct); got != want {
				t.Errorf("isHTMLContentType(%q) = %v, want %v", ct, got, want)
			}
		})
	}
}

func TestFetch_summarizeContentType(t *testing.T) {
	cases := map[string]string{
		"text/html; charset=utf-8": "text/html",
		"application/json":          "application/json",
		"":                          "unknown",
		"  text/plain ":             "text/plain",
	}
	for ct, want := range cases {
		t.Run(ct, func(t *testing.T) {
			if got := summarizeContentType(ct); got != want {
				t.Errorf("summarizeContentType(%q) = %q, want %q", ct, got, want)
			}
		})
	}
}
