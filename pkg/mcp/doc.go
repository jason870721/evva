// Package mcp implements evva's Model Context Protocol client. It loads
// MCP server configurations from settings.json, connects to each
// configured server via the official modelcontextprotocol/go-sdk, and
// surfaces discovered tools and resources so the agent can register them
// dynamically into the deferred-tool channel.
//
// The package is Experimental — public types may change in a minor
// version (see docs/sdk-stability.md). Stabilization candidate for v1.7
// or later once downstream consumers have exercised the surface.
//
// Architectural seam:
//
//   - The host calls mcp.Load(workdir, evvaHome) once at boot to read the
//     mcpServers block, then mcp.Open(ctx, cfg, opts) to build a *Manager.
//     The Manager opens connections concurrently and is safe to call
//     before any agent exists.
//   - The host passes the *Manager into agent.New via WithMcpManager;
//     internal/agent installs it on the per-agent ToolState and registers
//     a dynamic factory per discovered tool on pubtoolset.DefaultRegistry.
//   - When the model invokes mcp__server__tool, agent.ResolveTool builds
//     the tool through the dynamic factory, which captures the Manager's
//     session for that server.
//
// Subagents inherit the parent's *Manager — no re-connection, no
// session duplication. The Manager is the single source of truth for
// every MCP interaction in the agent tree.
//
// Transports supported: stdio (subprocess) and Streamable HTTP
// (2025-03-26 spec). SSE-only, WebSocket, SDK, claudeai-proxy, SSE-IDE,
// and WS-IDE transports are deliberately out of scope — see
// docs/roadmap/v1/v1-3-mcp.md §6.
package mcp
