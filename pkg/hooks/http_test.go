package hooks

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunHTTP_2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runHTTP(context.Background(), slog.Default(), Command{URL: srv.URL, Method: "POST", Timeout: 5}, []byte(`{}`), 10*time.Second)
	if err != nil {
		t.Errorf("expected nil error for 2xx, got %v", err)
	}
}

func TestRunHTTP_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := runHTTP(context.Background(), slog.Default(), Command{URL: srv.URL, Timeout: 5}, []byte(`{}`), 10*time.Second)
	if err == nil {
		t.Error("expected error for non-2xx")
	}
}

func TestRunHTTP_CustomHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runHTTP(context.Background(), slog.Default(), Command{
		URL:     srv.URL,
		Method:  "PUT",
		Headers: map[string]string{"X-Custom": "value"},
		Timeout: 5,
	}, []byte(`{"test":true}`), 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "value" {
		t.Errorf("expected X-Custom header, got %q", gotHeader)
	}
}
