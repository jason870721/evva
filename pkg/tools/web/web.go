// Package web hosts web tools: web_search (Tavily-backed) and web_fetch
// (HTTP GET + readable-text extraction).
//
// Tools hold a *config.Config pointer captured at construction so their
// Execute methods can read the live FETCH_MAX_BYTES / TAVILY_API_KEY
// (mutated by the /config form) without falling back to a global
// singleton.
package web

import (
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/config"
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.WEB_FETCH, tools.WEB_SEARCH, tools.HTTP_REQUEST}
}

// NewFetch builds a fetch tool bound to cfg. cfg may be nil — Execute
// falls back to the default 100k byte cap.
func NewFetch(cfg *config.Config) *FetchTool {
	return &FetchTool{cfg: cfg}
}

// NewSearch builds a search tool bound to cfg. cfg may be nil — Execute
// surfaces a "not configured" error when no API key is reachable.
func NewSearch(cfg *config.Config) *SearchTool {
	return &SearchTool{cfg: cfg}
}

// NewHTTPRequest builds an http_request tool bound to cfg. cfg may be nil —
// Execute falls back to the default 100k body cap.
func NewHTTPRequest(cfg *config.Config) *HTTPRequestTool {
	return &HTTPRequestTool{cfg: cfg}
}
