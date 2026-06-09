package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

func runHTTP(t *testing.T, input string) tools.Result {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	res, err := NewHTTPRequest(nil).Execute(context.Background(), logger, json.RawMessage(input))
	if err != nil {
		t.Fatalf("execute returned err: %v", err)
	}
	return res
}

func echoServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method": r.Method,
			"q":      r.URL.Query().Get("k"),
			"ctype":  r.Header.Get("Content-Type"),
			"xhdr":   r.Header.Get("X-Test"),
			"body":   string(body),
		})
	}))
}

func TestHTTPRequest_GETWithQuery(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	res := runHTTP(t, `{"url":"`+srv.URL+`/echo","query":{"k":"v"}}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{"→ 200", `"method": "GET"`, `"q": "v"`} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in:\n%s", want, res.Content)
		}
	}
}

func TestHTTPRequest_PostObjectBodySetsJSON(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	res := runHTTP(t, `{"method":"POST","url":"`+srv.URL+`/echo","body":{"a":1}}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"method": "POST"`) || !strings.Contains(res.Content, `"ctype": "application/json"`) {
		t.Errorf("object body should POST as application/json:\n%s", res.Content)
	}
}

func TestHTTPRequest_PostStringBodyVerbatim(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	res := runHTTP(t, `{"method":"POST","url":"`+srv.URL+`/echo","body":"raw text"}`)
	if !strings.Contains(res.Content, "raw text") {
		t.Errorf("string body should be sent as-is:\n%s", res.Content)
	}
}

func TestHTTPRequest_HeadersPassThrough(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	res := runHTTP(t, `{"url":"`+srv.URL+`/echo","headers":{"X-Test":"hi"}}`)
	if !strings.Contains(res.Content, `"xhdr": "hi"`) {
		t.Errorf("custom header not sent:\n%s", res.Content)
	}
}

func TestHTTPRequest_Non2xxReturnedNotErrored(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	res := runHTTP(t, `{"url":"`+srv.URL+`/missing"}`)
	if res.IsError {
		t.Fatalf("a 404 must be returned, not raised as a tool error:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "→ 404") || !strings.Contains(res.Content, "not found") {
		t.Errorf("404 body should be readable:\n%s", res.Content)
	}
}

func TestHTTPRequest_InputErrors(t *testing.T) {
	cases := map[string]string{
		"missing url":      `{}`,
		"bad method":       `{"url":"http://x","method":"FETCH"}`,
		"unsupported sch":  `{"url":"ftp://example.com"}`,
		"unparseable json": `not json`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			res := runHTTP(t, input)
			if !res.IsError {
				t.Errorf("expected IsError for %s, got: %s", name, res.Content)
			}
		})
	}
}
