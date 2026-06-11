package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/config"
)

const (
	fetchHTTPTimeout = 20 * time.Second
	fetchUserAgent   = "evva-web-fetch/1.0"

	// rawBodyMultiplier sizes the LimitReader on the raw HTTP body. HTML is
	// denser than the readable text we extract from it, so we slurp more
	// bytes than FetchMaxBytes to leave headroom for tag stripping.
	rawBodyMultiplier = 4
)

// fetchHTTPClient is shared across calls. http.Client is safe for
// concurrent use; sharing also lets the transport reuse connections when
// the model fetches several URLs in a single turn.
var fetchHTTPClient = &http.Client{Timeout: fetchHTTPTimeout}

// FetchTool implements web_fetch — GET a URL, render readable text.
// The cfg pointer is read at Execute time so runtime mutations of
// FetchMaxBytes via the /config form take effect on the next call.
type FetchTool struct {
	cfg *config.Config
}

func (t *FetchTool) Name() string { return string(tools.WEB_FETCH) }

func (t *FetchTool) Description() string {
	return "Fetches and extracts the readable text content from a specific URL.\n\n" +
		"Use to read full details of a webpage — API docs, GitHub issues, blog posts, StackOverflow threads. " +
		"Typically called AFTER web_search has returned a promising URL, or when the user has provided a URL directly.\n\n" +
		"HTML is parsed: scripts, styles, nav, footer, and aside are stripped; anchors render as \"text (href)\" so links remain followable. " +
		"Non-HTML responses (plain text, JSON) are returned as-is. " +
		"http:// URLs are auto-upgraded to https://.\n\n" +
		"Output is capped (FETCH_MAX_BYTES, default 100000 chars); over-long content is truncated with a marker. " +
		"WILL FAIL on authenticated pages (Google Docs, private GitHub, Confluence) — there is no auth or session support."
}

func (t *FetchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["url"],
		"properties":{
			"url":{"type":"string","format":"uri","description":"The exact URL of the webpage you want to read."}
		}
	}`)
}

type fetchInput struct {
	URL string `json:"url"`
}

func (t *FetchTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in fetchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: decode input: %v", err)}, nil
	}
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return tools.Result{IsError: true, Content: "web_fetch: url is required"}, nil
	}
	logger.Debug("fetch.dispatch", "url", raw)

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: invalid url: %q", raw)}, nil
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "https"
	case "https":
	default:
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: unsupported scheme %q (only http/https)", parsed.Scheme)}, nil
	}
	target := parsed.String()

	maxBytes := 0
	if t.cfg != nil {
		maxBytes = t.cfg.FetchMaxBytes
	}
	if maxBytes <= 0 {
		maxBytes = 100_000
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: build request: %v", err)}, nil
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9,*/*;q=0.5")

	resp, err := fetchHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return tools.Result{IsError: true, Content: "web_fetch: cancelled"}, ctx.Err()
		}
		logger.Warn("fetch.fail", "url", target, "stage", "do", "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Warn("fetch.fail", "url", finalURL, "status", resp.StatusCode)
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("web_fetch: %s on %s", resp.Status, finalURL),
		}, nil
	}

	bodyLimit := int64(maxBytes) * rawBodyMultiplier
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: read body: %v", err)}, nil
	}

	contentType := resp.Header.Get("Content-Type")
	var text string
	if isHTMLContentType(contentType) {
		text, err = extractReadableText(bytes.NewReader(bodyBytes))
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("web_fetch: parse html: %v", err)}, nil
		}
	} else {
		text = string(bodyBytes)
	}

	truncated := false
	if len(text) > maxBytes {
		text = text[:maxBytes]
		truncated = true
	}

	// The header and truncation marker are evva's own framing and stay OUTSIDE
	// the envelope; only the page text — the part the outside world authored —
	// is wrapped as untrusted (RP-21). An empty page gets no empty envelope.
	header := fmt.Sprintf("[Fetched: %s (%s, %d chars)]\n\n", finalURL, summarizeContentType(contentType), len(text))
	body := wrapUntrusted(finalURL, text)
	if body == "" {
		return tools.Result{Content: strings.TrimRight(header, "\n")}, nil
	}
	var out bytes.Buffer
	out.Grow(len(header) + len(body) + 64)
	out.WriteString(header)
	out.WriteString(body)
	if truncated {
		fmt.Fprintf(&out, "\n\n[truncated — output limited to %d chars]", maxBytes)
	}
	return tools.Result{Content: out.String()}, nil
}

func isHTMLContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	return strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml")
}

// summarizeContentType returns just the media type without parameters,
// e.g. "text/html; charset=utf-8" → "text/html". Falls back to "unknown"
// when the server didn't send one.
func summarizeContentType(ct string) string {
	ct = strings.TrimSpace(ct)
	if ct == "" {
		return "unknown"
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		return strings.TrimSpace(ct[:i])
	}
	return ct
}
