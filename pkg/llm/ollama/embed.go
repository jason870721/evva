package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
)

const (
	// DefaultEmbedModel is Ollama's small, fast embedding model. Chosen as
	// the default because it is the one most likely to already be pulled on
	// a machine that runs Ollama at all, and because 768 dims over a few
	// hundred memory files costs nothing to scan linearly.
	DefaultEmbedModel = "nomic-embed-text"

	embedPath = "/api/embed"
)

// Embedder implements llm.Embedder against a local Ollama server. This is
// the preferred backend: memory bodies never leave the machine, and it needs
// no API key.
type Embedder struct {
	apiURL string
	model  string
	http   *http.Client
}

// NewEmbedder builds an Ollama embedder. ApiSecret is ignored — Ollama is
// unauthenticated by default, same as the chat client.
func NewEmbedder(cfg llm.APIConfig, model string) *Embedder {
	if model == "" {
		model = DefaultEmbedModel
	}
	return &Embedder{
		apiURL: strings.TrimRight(cfg.ApiURL, "/"),
		model:  model,
		http:   &http.Client{},
	}
}

// EmbedderFactory adapts NewEmbedder into an llm.EmbedderFactory.
func EmbedderFactory(cfg llm.APIConfig, model string) (llm.Embedder, error) {
	return NewEmbedder(cfg, model), nil
}

func (e *Embedder) EmbedModel() string { return e.model }

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

// Embed sends one batched request. Ollama's /api/embed accepts an array and
// returns embeddings in input order, so no per-text round trip is needed.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(embedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("ollama embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiURL+embedPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama embed: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama embed: %s", out.Error)
	}
	// A short array would silently misalign vectors with their inputs — the
	// caller pairs them positionally — so this is a hard error, not a
	// best-effort partial result.
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: got %d embeddings for %d inputs", len(out.Embeddings), len(texts))
	}
	return out.Embeddings, nil
}
