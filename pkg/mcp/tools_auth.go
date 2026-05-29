package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
)

// mcpAuthTool is the per-server authenticate pseudo-tool surfaced for
// servers in needs-auth status. Invoking it drives the OAuth flow: the
// client reconnects with a live OAuth handler, which surfaces the auth
// URL through the installed prompt; on success the server's real tools
// are discovered, their factories registered, and the host's deferred
// allowlist refreshed.
//
// Analog of ref/src/tools/McpAuthTool/McpAuthTool.ts, simplified for
// v1.6 (no XAA, no in-browser auto-open).
type mcpAuthTool struct {
	mgr    *Manager
	client *Client
}

func newMcpAuthTool(m *Manager, c *Client) tools.Tool {
	return &mcpAuthTool{mgr: m, client: c}
}

func (t *mcpAuthTool) Name() string {
	return BuildToolName(t.client.Name, "authenticate")
}

func (t *mcpAuthTool) Description() string {
	return fmt.Sprintf("Authenticate the %q MCP server. The server requires OAuth; invoking this surfaces an authorization URL to the user, waits for them to complete the in-browser flow, then reconnects and makes the server's real tools available.", t.client.Name)
}

func (t *mcpAuthTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
}

func (t *mcpAuthTool) Execute(ctx context.Context, lgr *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	c := t.client
	if c.promptFn == nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("mcp authenticate: server %q has no OAuth prompt callback installed", c.Name)}, nil
	}

	c.mu.Lock()
	c.oauth = NewOAuthHandler(c.Name, c.logger, c.promptFn)
	c.mu.Unlock()

	if err := c.reconnect(ctx); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("mcp authenticate: %q reconnect failed: %v", c.Name, err)}, nil
	}
	if c.Status() != StatusConnected {
		return tools.Result{IsError: true, Content: fmt.Sprintf("mcp authenticate: %q is still not connected (status=%s); the authorization may have been cancelled or declined", c.Name, c.Status())}, nil
	}

	sdkTools, err := c.ListTools(ctx)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("mcp authenticate: %q connected but tools/list failed: %v", c.Name, err)}, nil
	}

	// Register the now-discovered tool factories and let the host refresh
	// its deferred allowlist so tool_search can surface them.
	t.mgr.registerClientTools(c)
	if t.mgr.onToolsChanged != nil {
		t.mgr.onToolsChanged()
	}

	names := make([]string, 0, len(sdkTools))
	for _, st := range sdkTools {
		names = append(names, BuildToolName(c.Name, st.Name))
	}
	lgr.Info("mcp: authenticated", "server", c.Name, "tools", len(names))

	body := fmt.Sprintf("Server %q authenticated and connected. %d tool(s) now available:\n%s\n\nUse tool_search to load a tool's schema before invoking it.",
		c.Name, len(names), strings.Join(names, "\n"))
	return tools.Result{Content: body}, nil
}
