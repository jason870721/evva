package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
)

const (
	// DefaultEmbedModel is OpenAI's cheapest embedding model. Memory recall
	// ranks a few hundred short documents; the large model's extra accuracy
	// is not worth several times the cost here.
	DefaultEmbedModel = "text-embedding-3-small"

	embedPath = "/v1/embeddings"
)

// Embedder implements llm.Embedder against the OpenAI embeddings endpoint.
//
// This is the HOSTED backend, and it is deliberately not the default: using
// it means memory bodies leave the machine. The Ollama backend covers the
// local path, and the config docs state the tradeoff plainly rather than
// leaving it to be discovered.
//
// The wire shape is the OpenAI-compatible one, so this type also serves any
// provider that mirrors that surface — the factory takes the base URL from
// APIConfig rather than hardcoding api.openai.com.
type Embedder struct {
	apiURL string
	apiKey string
	model  string
	http   *http.Client
}

// NewEmbedder builds an OpenAI-compatible embedder.
func NewEmbedder(cfg llm.APIConfig, model string) *Embedder {
	if model == "" {
		model = DefaultEmbedModel
	}
	return &Embedder{
		apiURL: strings.TrimRight(cfg.ApiURL, "/"),
		apiKey: cfg.ApiSecret,
		model:  model,
		http:   &http.Client{},
	}
}

// EmbedderFactory adapts NewEmbedder into an llm.EmbedderFactory. An absent
// key is an error here rather than at first use: a hosted embedder with no
// credential can only ever fail, and failing at construction lets the caller
// fall back to keyword search before a session starts.
func EmbedderFactory(cfg llm.APIConfig, model string) (llm.Embedder, error) {
	if strings.TrimSpace(cfg.ApiSecret) == "" {
		return nil, fmt.Errorf("openai embed: no API key configured")
	}
	return NewEmbedder(cfg, model), nil
}

func (e *Embedder) EmbedModel() string { return e.model }

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed sends one batched request and returns vectors in INPUT order.
//
// The response is re-sorted by the `index` field rather than trusted to
// arrive ordered: the API documents an index per row precisely because the
// order is not guaranteed, and the caller pairs vectors with inputs
// positionally. Getting this wrong would attach every memory's vector to the
// wrong memory — a failure that produces plausible-looking rankings and no
// error at all.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(embedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("openai embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiURL+embedPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai embed: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: http: %w", llm.NormalizeErr(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embed: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embed: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("openai embed: decode: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("openai embed: %s", out.Error.Message)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("openai embed: got %d embeddings for %d inputs", len(out.Data), len(texts))
	}

	rows := out.Data
	sort.Slice(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	vecs := make([][]float32, len(rows))
	for i, r := range rows {
		if r.Index != i {
			return nil, fmt.Errorf("openai embed: non-contiguous index %d at position %d", r.Index, i)
		}
		vecs[i] = r.Embedding
	}
	return vecs, nil
}
