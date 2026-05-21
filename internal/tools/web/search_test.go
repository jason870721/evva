package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/tools"
	"time"

	config "github.com/johnny1110/evva/pkg/config"
)

// Phase 1 analysis — SearchTool.Execute code paths:
//   - Decode JSON input
//   - Reject empty/whitespace query
//   - Reject missing TAVILY_API_KEY (read from config singleton)
//   - Marshal request body
//   - Build request with ctx; on ctx already cancelled, surface ctx err
//   - HTTP POST; non-2xx → IsError with body snippet
//   - Decode response JSON; on decode failure → IsError
//   - Empty results → "no results" line
//   - Non-empty results → numbered markdown block

// setTavilyKey overrides the config singleton's TAVILY API key for the
// duration of a test, restoring the previous value on cleanup. The
// config package's Get() returns a process-wide singleton so this is
// the cleanest hook short of refactoring the tool's signature.
func setTavilyKey(t *testing.T, key string) {
	t.Helper()
	cfg := config.Get()
	prev := cfg.TavilyAPIKey
	cfg.TavilyAPIKey = key
	t.Cleanup(func() { cfg.TavilyAPIKey = prev })
}

// withTavilyServer points the tool at a test HTTP server and restores
// the production endpoint + client on cleanup. The injected client has
// no timeout (tests should be fast; an over-eager timeout slows the suite).
func withTavilyServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	prevEndpoint := tavilyEndpoint
	prevClient := searchHTTPClient
	tavilyEndpoint = srv.URL
	searchHTTPClient = &http.Client{Timeout: 2 * time.Second}
	t.Cleanup(func() {
		tavilyEndpoint = prevEndpoint
		searchHTTPClient = prevClient
		srv.Close()
	})
	return srv
}

func TestSearch_RejectsEmptyQuery(t *testing.T) {
	// Arrange
	tool := NewSearch(config.Get())
	setTavilyKey(t, "tvly-test")

	// Act
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"query":"   "}`))

	// Assert
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError true for whitespace query")
	}
	if !strings.Contains(res.Content, "required") {
		t.Errorf("expected error to mention 'required', got %q", res.Content)
	}
}

func TestSearch_RejectsMissingAPIKey(t *testing.T) {
	// Arrange
	tool := NewSearch(config.Get())
	setTavilyKey(t, "")

	// Act
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"query":"hello"}`))

	// Assert
	if !res.IsError {
		t.Fatal("expected IsError when TAVILY_API_KEY unset")
	}
	if !strings.Contains(res.Content, "TAVILY_API_KEY") {
		t.Errorf("expected error to mention env var name, got %q", res.Content)
	}
}

func TestSearch_RejectsDecodeError(t *testing.T) {
	tool := NewSearch(config.Get())
	setTavilyKey(t, "tvly-test")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{not-json`))

	if !res.IsError {
		t.Fatal("expected IsError on malformed JSON input")
	}
	if !strings.Contains(res.Content, "decode") {
		t.Errorf("expected 'decode' in error, got %q", res.Content)
	}
}

func TestSearch_HappyPath_RendersResults(t *testing.T) {
	// Arrange — capture the inbound request so we can assert the body.
	var capturedBody []byte
	var capturedMethod, capturedContentType string
	withTavilyServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedContentType = r.Header.Get("Content-Type")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"title":"Go release notes","url":"https://go.dev/doc/devel/release","content":"Latest Go releases."},
				{"title":"","url":"https://example.com/blank","content":""}
			]
		}`))
	})
	tool := NewSearch(config.Get())
	setTavilyKey(t, "tvly-secret")

	// Act
	res, err := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"query":"latest go release"}`))

	// Assert request shape
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", capturedMethod)
	}
	if !strings.HasPrefix(capturedContentType, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json*", capturedContentType)
	}
	var req tavilyRequest
	if jerr := json.Unmarshal(capturedBody, &req); jerr != nil {
		t.Fatalf("server received non-JSON body: %v\nraw=%s", jerr, capturedBody)
	}
	if req.APIKey != "tvly-secret" {
		t.Errorf("api_key forwarded as %q, want %q", req.APIKey, "tvly-secret")
	}
	if req.Query != "latest go release" {
		t.Errorf("query forwarded as %q", req.Query)
	}
	if req.MaxResults != tavilyMaxResults {
		t.Errorf("max_results: got %d, want %d", req.MaxResults, tavilyMaxResults)
	}

	// Assert response rendering
	if res.IsError {
		t.Fatalf("unexpected IsError; content=%s", res.Content)
	}
	want := []string{
		`Search results for "latest go release"`,
		"1. **Go release notes** — https://go.dev/doc/devel/release",
		"Latest Go releases.",
		"2. **(untitled)** — https://example.com/blank",
	}
	for _, fragment := range want {
		if !strings.Contains(res.Content, fragment) {
			t.Errorf("output missing %q\nfull:\n%s", fragment, res.Content)
		}
	}
}

func TestSearch_EmptyResults_ReturnsNoResultsLine(t *testing.T) {
	withTavilyServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	tool := NewSearch(config.Get())
	setTavilyKey(t, "tvly-test")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"query":"nothing matches"}`))

	if res.IsError {
		t.Fatalf("empty results should not be an error; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "no results") {
		t.Errorf("expected 'no results' marker; got %q", res.Content)
	}
}

func TestSearch_Non2xxSurfacesStatusAndBody(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"detail":"bad api key"}`},
		{"server-error", http.StatusInternalServerError, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTavilyServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			tool := NewSearch(config.Get())
			setTavilyKey(t, "tvly-test")

			res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"query":"q"}`))

			if !res.IsError {
				t.Fatal("expected IsError on non-2xx")
			}
			if !strings.Contains(res.Content, tc.body) {
				t.Errorf("expected body snippet %q in output, got %q", tc.body, res.Content)
			}
		})
	}
}

func TestSearch_MalformedResponseJSON(t *testing.T) {
	withTavilyServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json at all`))
	})
	tool := NewSearch(config.Get())
	setTavilyKey(t, "tvly-test")

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"query":"q"}`))

	if !res.IsError {
		t.Fatal("expected IsError on undecodable response")
	}
	if !strings.Contains(res.Content, "decode") {
		t.Errorf("expected 'decode' in error, got %q", res.Content)
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	// Server hangs so the only way out is ctx cancellation.
	withTavilyServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	tool := NewSearch(config.Get())
	setTavilyKey(t, "tvly-test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	res, err := tool.Execute(ctx, tools.NopLogger(), json.RawMessage(`{"query":"q"}`))

	if err == nil {
		t.Fatal("expected go-level error on cancelled ctx")
	}
	if !res.IsError {
		t.Fatal("expected IsError on cancelled ctx")
	}
}
