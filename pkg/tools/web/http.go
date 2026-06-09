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

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
)

const (
	httpReqTimeout   = 30 * time.Second
	httpReqUserAgent = "evva-http-request/1.0"
)

// httpReqClient is shared (http.Client is concurrency-safe) so connections are
// reused when the model makes several calls in one turn.
var httpReqClient = &http.Client{Timeout: httpReqTimeout}

// httpReqMethods is the allowed method set. GET/HEAD are read-only and
// auto-allow at the permission gate; the rest mutate and require approval
// (see pkg/permission/decision.go — the http_request method carve-out).
var httpReqMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
}

// HTTPRequestTool implements http_request: a generic HTTP client for driving
// JSON/HTTP APIs with structured input/output, instead of hand-built curl
// strings piped through jq/python. It is the general ergonomics primitive a
// swarm uses to operate an external HTTP system (Sunday's motivating case): the
// model gets the status code + parsed body directly, with no shell quoting.
//
// Unlike web_fetch (which renders a page and treats non-2xx as an error), this
// returns the response for ANY status — a 4xx/409 body is exactly what an API
// client needs to read and react to (e.g. an optimistic-concurrency 409).
type HTTPRequestTool struct {
	cfg *config.Config
}

func (t *HTTPRequestTool) Name() string { return string(tools.HTTP_REQUEST) }

func (t *HTTPRequestTool) Description() string {
	return "Make an HTTP request to a JSON/HTTP API and return the status code, key response headers, and body.\n\n" +
		"Use this to drive HTTP APIs (REST/JSON services, local tools, webhooks) — it is the structured alternative to " +
		"building `curl` command strings: pass `method`, `url`, optional `headers`, `query`, and `body` as fields, and read " +
		"the parsed response back. JSON bodies are pretty-printed.\n\n" +
		"`method` defaults to GET. A `body` that is a JSON string is sent verbatim; an object/array is JSON-encoded and " +
		"`Content-Type: application/json` is set automatically. Only http/https are allowed.\n\n" +
		"Non-2xx responses are RETURNED (not raised as errors) so you can inspect a 4xx/5xx body and act on it (a 409 with a " +
		"`current_status`, a 400 validation message, etc.). Only network/build failures are tool errors.\n\n" +
		"Permission: GET and HEAD are read-only and run without approval; POST/PUT/PATCH/DELETE mutate and prompt for approval."
}

func (t *HTTPRequestTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["url"],
		"properties":{
			"method":{"type":"string","enum":["GET","HEAD","POST","PUT","PATCH","DELETE"],"description":"HTTP method (default GET). GET/HEAD are read-only; the rest mutate and need approval."},
			"url":{"type":"string","format":"uri","description":"Full URL including scheme (http/https)."},
			"headers":{"type":"object","additionalProperties":{"type":"string"},"description":"Optional request headers."},
			"query":{"type":"object","additionalProperties":{"type":"string"},"description":"Optional query parameters appended to the URL."},
			"body":{"description":"Optional request body. A JSON string is sent as-is; an object/array is JSON-encoded with Content-Type application/json."}
		}
	}`)
}

type httpRequestInput struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
	Body    json.RawMessage   `json:"body"`
}

func (t *HTTPRequestTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in httpRequestInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult("http_request: decode input: %v", err), nil
	}

	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !httpReqMethods[method] {
		return errResult("http_request: unsupported method %q", method), nil
	}

	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return errResult("http_request: url is required"), nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errResult("http_request: invalid url: %q", raw), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errResult("http_request: unsupported scheme %q (only http/https)", parsed.Scheme), nil
	}
	if len(in.Query) > 0 {
		q := parsed.Query()
		for k, v := range in.Query {
			q.Set(k, v)
		}
		parsed.RawQuery = q.Encode()
	}

	bodyReader, bodyContentType := buildBody(in.Body)

	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), bodyReader)
	if err != nil {
		return errResult("http_request: build request: %v", err), nil
	}
	req.Header.Set("User-Agent", httpReqUserAgent)
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	if bodyContentType != "" {
		req.Header.Set("Content-Type", bodyContentType)
	}
	for k, v := range in.Headers { // explicit headers win over defaults
		req.Header.Set(k, v)
	}

	logger.Debug("http_request.dispatch", "method", method, "url", parsed.String())
	resp, err := httpReqClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errResult("http_request: cancelled"), ctx.Err()
		}
		logger.Warn("http_request.fail", "url", parsed.String(), "err", err)
		return errResult("http_request: request failed: %v", err), nil
	}
	defer resp.Body.Close()

	maxBytes := 100_000
	if t.cfg != nil && t.cfg.FetchMaxBytes > 0 {
		maxBytes = t.cfg.FetchMaxBytes
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return errResult("http_request: read body: %v", err), nil
	}
	truncated := false
	if len(bodyBytes) > maxBytes {
		bodyBytes = bodyBytes[:maxBytes]
		truncated = true
	}

	return tools.Result{Content: formatResponse(method, resp, bodyBytes, truncated, maxBytes)}, nil
}

// buildBody turns the JSON `body` field into a request reader + content type. A
// JSON string is sent verbatim (the caller controls the bytes); any other JSON
// value (object/array/number) is sent as JSON with the json content type. nil
// when there's no body.
func buildBody(raw json.RawMessage) (io.Reader, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.NewReader(s), ""
	}
	return bytes.NewReader(raw), "application/json"
}

// formatResponse renders the status line + content type + (pretty, if JSON) body.
func formatResponse(method string, resp *http.Response, body []byte, truncated bool, maxBytes int) string {
	ct := resp.Header.Get("Content-Type")
	rendered := string(body)
	if looksLikeJSON(ct, rendered) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") == nil {
			rendered = pretty.String()
		}
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "[http_request: %s %s → %d %s", method, resp.Request.URL.String(), resp.StatusCode, http.StatusText(resp.StatusCode))
	if ct != "" {
		fmt.Fprintf(&out, ", %s", summarizeContentType(ct))
	}
	out.WriteString("]\n")
	out.WriteString(rendered)
	if truncated {
		fmt.Fprintf(&out, "\n\n[truncated — body limited to %d bytes]", maxBytes)
	}
	return out.String()
}

func looksLikeJSON(contentType, body string) bool {
	if strings.Contains(strings.ToLower(contentType), "json") {
		return true
	}
	b := strings.TrimSpace(body)
	return strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[")
}

func errResult(format string, args ...any) tools.Result {
	return tools.Result{IsError: true, Content: fmt.Sprintf(format, args...)}
}
