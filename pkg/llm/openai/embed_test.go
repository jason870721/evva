package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

// embedRow mirrors one element of the response's data array.
type embedRow struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

func writeRows(w http.ResponseWriter, rows []embedRow) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
}

func TestEmbedSendsAuthAndBatches(t *testing.T) {
	var auth string
	var gotReq embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != embedPath {
			t.Errorf("path: got %q, want %q", r.URL.Path, embedPath)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		writeRows(w, []embedRow{{Index: 0, Embedding: []float32{1, 0}}, {Index: 1, Embedding: []float32{0, 1}}})
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL, ApiSecret: "sk-test"}, "m")
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization: got %q", auth)
	}
	if len(gotReq.Input) != 2 {
		t.Errorf("expected one batched request, got %d inputs", len(gotReq.Input))
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Errorf("vectors: %v", vecs)
	}
}

// The API documents a per-row index precisely because response order is not
// guaranteed. Trusting arrival order would attach every memory's vector to
// the wrong memory — producing plausible rankings and no error at all.
func TestEmbedReordersByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRows(w, []embedRow{
			{Index: 2, Embedding: []float32{3}},
			{Index: 0, Embedding: []float32{1}},
			{Index: 1, Embedding: []float32{2}},
		})
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL, ApiSecret: "k"}, "m")
	vecs, err := e.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for i, want := range []float32{1, 2, 3} {
		if vecs[i][0] != want {
			t.Errorf("position %d: got %v, want %v — response was not re-sorted by index", i, vecs[i][0], want)
		}
	}
}

func TestEmbedRejectsNonContiguousIndices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRows(w, []embedRow{{Index: 0, Embedding: []float32{1}}, {Index: 7, Embedding: []float32{2}}})
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL, ApiSecret: "k"}, "m")
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("a gap in the index sequence must be an error, not a silent misalignment")
	}
}

func TestEmbedRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRows(w, []embedRow{{Index: 0, Embedding: []float32{1}}})
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL, ApiSecret: "k"}, "m")
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected an error when the server returns fewer vectors than inputs")
	}
}

func TestEmbedSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL, ApiSecret: "bad"}, "m")
	_, err := e.Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("expected the server's message to survive, got: %v", err)
	}
}

// A hosted embedder with no credential can only ever fail. Failing at
// construction lets the caller fall back to keyword search before a session
// starts, rather than discovering it on the first search.
func TestEmbedderFactoryRequiresKey(t *testing.T) {
	if _, err := EmbedderFactory(llm.APIConfig{ApiURL: "https://x"}, "m"); err == nil {
		t.Error("factory should refuse to build without an API key")
	}
	if _, err := EmbedderFactory(llm.APIConfig{ApiURL: "https://x", ApiSecret: "  "}, "m"); err == nil {
		t.Error("a whitespace-only key should count as absent")
	}
	if _, err := EmbedderFactory(llm.APIConfig{ApiURL: "https://x", ApiSecret: "k"}, "m"); err != nil {
		t.Errorf("factory should succeed with a key: %v", err)
	}
}

func TestEmbedderDefaultsModel(t *testing.T) {
	if e := NewEmbedder(llm.APIConfig{}, ""); e.EmbedModel() != DefaultEmbedModel {
		t.Errorf("empty model should default to %q, got %q", DefaultEmbedModel, e.EmbedModel())
	}
}
