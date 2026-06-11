package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/config"
)

const (
	tavilyMaxResults  = 5
	tavilyHTTPTimeout = 20 * time.Second
)

// tavilyEndpoint is the search-API URL. Promoted to a var (not a const)
// so tests can point Execute at an httptest.Server and assert request
// shape. Production callers never mutate it.
var tavilyEndpoint = "https://api.tavily.com/search"

// searchHTTPClient is shared by every Execute call — http.Client is safe
// for concurrent use and pooling connections matters when the model fires
// several searches in a single turn.
var searchHTTPClient = &http.Client{Timeout: tavilyHTTPTimeout}

// SearchTool implements web_search via the Tavily API. The cfg pointer
// is read at Execute time so the /config form's TavilyAPIKey rotation
// takes effect on the next call.
type SearchTool struct {
	cfg *config.Config
}

func (t *SearchTool) Name() string { return string(tools.WEB_SEARCH) }

func (t *SearchTool) Description() string {
	return "Searches the internet via the Tavily API for up-to-date information, technical documentation, news, or solutions to errors.\n\n" +
		"Use when you lack the latest context, when dealing with newly-released libraries/APIs, or when you need to find a specific page. " +
		"Formulate concise, Google-style queries.\n\n" +
		"Returns a numbered list of results — each entry has a title, the source URL, and a brief snippet. " +
		"To read the full content of a promising result, follow up with web_fetch on its URL.\n\n" +
		"Requires TAVILY_API_KEY to be set in the environment; without it the tool returns a configuration error."
}

func (t *SearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["query"],
		"properties":{
			"query":{"type":"string","minLength":2,"description":"The search query to look up on the internet."}
		}
	}`)
}

type searchInput struct {
	Query string `json:"query"`
}

type tavilyRequest struct {
	APIKey        string `json:"api_key"`
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
	SearchDepth   string `json:"search_depth"`
	IncludeAnswer bool   `json:"include_answer"`
}

type tavilyResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

func (t *SearchTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in searchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_search: decode input: %v", err)}, nil
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return tools.Result{IsError: true, Content: "web_search: query is required"}, nil
	}
	logger.Debug("search.dispatch", "query", query)

	apiKey := ""
	if t.cfg != nil {
		apiKey = t.cfg.TavilyAPIKey
	}
	if apiKey == "" {
		return tools.Result{
			IsError: true,
			Content: "web_search: TAVILY_API_KEY is not configured. Set it in ~/.evva/.env to enable web search.",
		}, nil
	}

	reqBody, err := json.Marshal(tavilyRequest{
		APIKey:        apiKey,
		Query:         query,
		MaxResults:    tavilyMaxResults,
		SearchDepth:   "basic",
		IncludeAnswer: false,
	})
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_search: encode request: %v", err)}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_search: build request: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := searchHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return tools.Result{IsError: true, Content: "web_search: cancelled"}, ctx.Err()
		}
		logger.Warn("search.fail", "query", query, "stage", "do", "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_search: request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		logger.Warn("search.fail", "query", query, "status", resp.StatusCode)
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("web_search: %s: %s", resp.Status, strings.TrimSpace(string(snippet))),
		}, nil
	}

	var out tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_search: decode response: %v", err)}, nil
	}

	if len(out.Results) == 0 {
		return tools.Result{Content: fmt.Sprintf("web_search: no results for %q", query)}, nil
	}

	// Titles, URLs, and snippets are authored by the outside world — the whole
	// result list goes inside one untrusted envelope; only the header line is
	// evva's own framing (RP-21).
	var b strings.Builder
	for i, r := range out.Results {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "%d. **%s** — %s\n", i+1, title, r.URL)
		if snippet := strings.TrimSpace(r.Content); snippet != "" {
			fmt.Fprintf(&b, "   %s\n", snippet)
		}
		b.WriteByte('\n')
	}
	header := fmt.Sprintf("Search results for %q:\n\n", query)
	return tools.Result{Content: header + wrapUntrusted("web_search", strings.TrimRight(b.String(), "\n"))}, nil
}
