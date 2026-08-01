package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

func TestEmbedBatchesAndPreservesOrder(t *testing.T) {
	var gotReq embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != embedPath {
			t.Errorf("path: got %q, want %q", r.URL.Path, embedPath)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(embedResponse{
			Embeddings: [][]float32{{1, 0}, {0, 1}, {1, 1}},
		})
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL}, "test-model")
	vecs, err := e.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// One request for three inputs — per-text round trips would make a cold
	// index rebuild unusably slow.
	if len(gotReq.Input) != 3 {
		t.Errorf("expected one batched request of 3 inputs, got %d", len(gotReq.Input))
	}
	if gotReq.Model != "test-model" {
		t.Errorf("model: got %q", gotReq.Model)
	}
	if len(vecs) != 3 || vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Errorf("vectors misaligned: %v", vecs)
	}
}

// A short array would silently pair vectors with the wrong inputs, since the
// caller matches positionally. That must be an error, not a partial result.
func TestEmbedRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{1, 0}}})
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL}, "m")
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected an error when the server returns fewer vectors than inputs")
	} else if !strings.Contains(err.Error(), "1 embeddings for 2 inputs") {
		t.Errorf("error should name the mismatch, got: %v", err)
	}
}

func TestEmbedSurfacesServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`model "nope" not found`))
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL}, "nope")
	_, err := e.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected an error on HTTP 404")
	}
	// The message must name the missing model: "pull the model" is the fix,
	// and the operator can only act on it if the error says so.
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should carry the server's body, got: %v", err)
	}
}

func TestEmbedEmptyInputIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("no request should be made for an empty input")
	}))
	defer srv.Close()

	e := NewEmbedder(llm.APIConfig{ApiURL: srv.URL}, "m")
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil || len(vecs) != 0 {
		t.Errorf("empty input: got %v, %v", vecs, err)
	}
}

func TestEmbedderDefaultsModel(t *testing.T) {
	e := NewEmbedder(llm.APIConfig{}, "")
	if e.EmbedModel() != DefaultEmbedModel {
		t.Errorf("empty model should default to %q, got %q", DefaultEmbedModel, e.EmbedModel())
	}
}

// Ollama is unauthenticated; the factory must not demand a key the way the
// hosted backend does.
func TestEmbedderFactoryNeedsNoKey(t *testing.T) {
	e, err := EmbedderFactory(llm.APIConfig{ApiURL: "http://localhost:11434"}, "")
	if err != nil || e == nil {
		t.Fatalf("factory should succeed with no API key: %v", err)
	}
}
