package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

// Client wraps one SDK ClientSession with the lifecycle policy this phase
// needs: lazy re-connect on session-expired, lock-protected swap of the
// underlying session, and a small set of convenience methods the dynamic
// tool factories call.
type Client struct {
	Name   string
	Config ServerConfig

	mu      sync.RWMutex
	session *mcpsdk.ClientSession // may be replaced after reconnect
	status  ServerStatus
	lastErr error
	tools   []*mcpsdk.Tool // result of last tools/list
	caps    *mcpsdk.ServerCapabilities

	logger   *slog.Logger
	oauth    *OAuthHandler // nil until the per-server authenticate tool runs
	promptFn OAuthPromptFn // host-installed prompt, used to build oauth on authenticate
	evvaHome string        // threaded to ConvertResult for blob persistence; "" disables blob writes
}

// connect runs the initial Connect + initialize handshake. Caller holds
// c.mu (or is the only goroutine touching c, as during Open's fan-out).
func (c *Client) connect(ctx context.Context) error {
	var (
		transport mcpsdk.Transport
		err       error
	)
	switch c.Config.Type {
	case TransportStdio:
		transport, err = buildStdioTransport(c.Config)
	case TransportStreamableHTTP:
		if c.oauth != nil {
			h, herr := c.oauth.SDKHandler()
			if herr != nil {
				return fmt.Errorf("mcp: build oauth handler: %w", herr)
			}
			transport, err = buildStreamableHTTPTransport(c.Config, h)
		} else {
			transport, err = buildStreamableHTTPTransport(c.Config, nil)
		}
	default:
		return fmt.Errorf("mcp: unknown transport %q", c.Config.Type)
	}
	if err != nil {
		c.status = StatusFailed
		c.lastErr = err
		return err
	}

	impl := &mcpsdk.Implementation{Name: "evva", Version: "1.6.0"}
	sdkClient := mcpsdk.NewClient(impl, &mcpsdk.ClientOptions{
		Logger:                      c.logger,
		ProgressNotificationHandler: c.logProgress,
		LoggingMessageHandler:       c.logServerLog,
		// Sampling, Elicitation, Roots — out of scope §6.
	})

	timeout := c.Config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	session, err := sdkClient.Connect(cctx, transport, nil)
	if err != nil {
		if isAuthError(err) {
			c.status = StatusNeedsAuth
			c.lastErr = err
			return nil // not a hard failure — the authenticate tool recovers
		}
		c.status = StatusFailed
		c.lastErr = err
		return err
	}
	c.session = session
	c.status = StatusConnected
	c.lastErr = nil
	if ir := session.InitializeResult(); ir != nil {
		c.caps = ir.Capabilities
	}
	return nil
}

// ListTools fetches the server's tool catalog. Returns nil for
// disconnected / failed / auth-needed states (no error — these are
// expected states the manager already tracks).
func (c *Client) ListTools(ctx context.Context) ([]*mcpsdk.Tool, error) {
	c.mu.RLock()
	if c.status != StatusConnected || c.session == nil {
		c.mu.RUnlock()
		return nil, nil
	}
	s := c.session
	c.mu.RUnlock()

	res, err := s.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.tools = res.Tools
	c.mu.Unlock()
	return res.Tools, nil
}

// CallTool invokes a tool, retrying once on session-expired.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.RLock()
		s := c.session
		c.mu.RUnlock()
		if s == nil {
			return nil, errors.New("mcp: no active session")
		}

		result, err := s.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      name,
			Arguments: args,
		})
		if err == nil {
			return result, nil
		}
		if attempt == 0 && isSessionExpired(err) {
			c.logger.Info("mcp: session expired, reconnecting", "server", c.Name)
			if reErr := c.reconnect(ctx); reErr != nil {
				return nil, fmt.Errorf("mcp: reconnect after session-expired: %w", reErr)
			}
			continue
		}
		if isAuthError(err) {
			c.mu.Lock()
			c.status = StatusNeedsAuth
			c.mu.Unlock()
		}
		return nil, err
	}
	return nil, errors.New("mcp: unreachable retry loop")
}

// reconnect tears down the current session and runs connect again.
// Caller does NOT hold c.mu.
func (c *Client) reconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
	return c.connect(ctx)
}

// Status returns the live connection status under the read lock.
func (c *Client) Status() ServerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// isSessionExpired detects MCP session-not-found errors. The SDK wraps
// these in the exported sentinel ErrSessionMissing (HTTP 404 from a
// terminated streamable session), so a structured errors.Is check is
// robust to message-format drift. The 404/-32001 substring fallback
// covers any path that surfaces the raw JSON-RPC error without the
// sentinel. TestErrorMatchers_PinSDKShape pins both behaviors.
func isSessionExpired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcpsdk.ErrSessionMissing) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "404") && strings.Contains(s, "-32001")
}

// isAuthError detects 401/403 from HTTP transports. The SDK consumes
// 401/403 internally when an OAuthHandler is attached; at boot (no
// handler) the connect error carries the HTTP status text, so a
// substring match is the available signal. Pinned by
// TestErrorMatchers_PinSDKShape.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "403") ||
		strings.Contains(s, "Unauthorized") || strings.Contains(s, "Forbidden")
}

func (c *Client) logProgress(_ context.Context, req *mcpsdk.ProgressNotificationClientRequest) {
	if req == nil || req.Params == nil {
		return
	}
	c.logger.Debug("mcp.progress", "server", c.Name, "progress", req.Params.Progress, "total", req.Params.Total, "msg", req.Params.Message)
}

func (c *Client) logServerLog(_ context.Context, req *mcpsdk.LoggingMessageRequest) {
	if req == nil || req.Params == nil {
		return
	}
	c.logger.Info("mcp.server_log", "server", c.Name, "level", req.Params.Level, "data", req.Params.Data)
}

// newMcpTool builds a tools.Tool whose identity and schema come from the
// SDK Tool and whose Execute dispatches through the owning Client.
func newMcpTool(c *Client, sdkTool *mcpsdk.Tool) tools.Tool {
	schemaBytes, err := json.Marshal(sdkTool.InputSchema)
	if err != nil || len(schemaBytes) == 0 || string(schemaBytes) == "null" {
		// A tool with no usable schema still needs a valid object schema so
		// the provider accepts the tool definition.
		schemaBytes = json.RawMessage(`{"type":"object"}`)
	}
	return &mcpToolImpl{
		client:  c,
		sdkName: sdkTool.Name,
		name:    BuildToolName(c.Name, sdkTool.Name),
		desc:    sdkTool.Description,
		schema:  schemaBytes,
	}
}

type mcpToolImpl struct {
	client  *Client
	sdkName string
	name    string
	desc    string
	schema  json.RawMessage
}

func (t *mcpToolImpl) Name() string            { return t.name }
func (t *mcpToolImpl) Description() string     { return t.desc }
func (t *mcpToolImpl) Schema() json.RawMessage { return t.schema }

func (t *mcpToolImpl) Execute(ctx context.Context, _ *slog.Logger, in json.RawMessage) (tools.Result, error) {
	var args map[string]any
	if len(in) > 0 {
		if err := json.Unmarshal(in, &args); err != nil {
			return tools.Result{IsError: true, Content: "mcp: decode args: " + err.Error()}, nil
		}
	}
	res, err := t.client.CallTool(ctx, t.sdkName, args)
	if err != nil {
		return tools.Result{IsError: true, Content: "mcp: call failed: " + err.Error()}, nil
	}
	return ConvertResult(res, t.client.Name, t.sdkName, t.client.evvaHome)
}
