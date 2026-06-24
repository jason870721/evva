package agent

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/pkg/mcp"
	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
	"github.com/johnny1110/evva/pkg/ui"
)

// autoLoadMcp loads the MCP server config from settings.json, opens
// connections, registers the discovered tool factories on the default
// registry, and installs the manager on the ToolState. Skipped when a
// host already injected a manager via WithMcpManager (mcpManagerSet) or
// one is otherwise present. Mirrors the skill / LSP auto-load blocks:
// one disk read at startup, zero cost when nothing is configured.
//
// The OAuth prompt is installed as a late-bound closure: the question
// broker doesn't exist yet at this point in New (wireBrokers runs further
// down). Reading toolState.QuestionBroker() at OAuth-time is safe because
// OAuth only fires on a tool-call event, long after boot completes.
func (a *Agent) autoLoadMcp(lgr *slog.Logger) {
	if a.mcpManagerSet || a.toolState.McpManager() != nil {
		return
	}
	cfg, warns := mcp.Load(a.workdir, a.cfg.AppHome)
	for _, w := range warns {
		lgr.Warn("mcp: config", "msg", w.Error())
	}
	mgr, openWarns := mcp.Open(a.rootCtx, cfg, mcp.OpenOptions{
		Logger:      lgr,
		EvvaHome:    a.cfg.AppHome,
		OAuthPrompt: mcpPromptViaQuestion(func() question.Broker { return a.toolState.QuestionBroker() }),
	})
	for _, w := range openWarns {
		lgr.Warn("mcp: connect", "server", w.Path, "err", w.Err)
	}
	mgr.RegisterFactories(pubtoolset.DefaultRegistry())
	// After a needs-auth server authenticates mid-session, refresh the
	// deferred allowlist so tool_search can surface its now-real tools.
	mgr.SetOnToolsChanged(a.foldMcpIntoAllowlist)
	a.toolState.SetMcpManager(mgr)
}

// MCPServers returns a UI-facing snapshot of every configured MCP server and
// its live connection status — what the read-only /mcp panel renders. It
// includes disabled servers (Status() reports them) and maps mcp.ServerState
// into the public ui.MCPServerInfo so the ui package never sees pkg/mcp.
// Nil-safe: returns nil when no manager is installed (nothing configured).
func (a *Agent) MCPServers() []ui.MCPServerInfo {
	mgr := a.toolState.McpManager()
	if mgr == nil {
		return nil
	}
	states := mgr.Status()
	if len(states) == 0 {
		return nil
	}
	out := make([]ui.MCPServerInfo, 0, len(states))
	for _, s := range states {
		detail := s.Config.URL
		if s.Config.Type == mcp.TransportStdio {
			detail = strings.TrimSpace(s.Config.Command + " " + strings.Join(s.Config.Args, " "))
		}
		out = append(out, ui.MCPServerInfo{
			Name:          s.Name,
			Transport:     string(s.Config.Type),
			Status:        string(s.Status),
			Scope:         string(s.Config.Scope),
			Detail:        detail,
			ToolCount:     s.ToolCount,
			ResourceCount: s.ResourceCount,
			Error:         s.Error,
		})
	}
	return out
}

// mcpDiscoveredNames returns every mcp__<server>__<tool> (and per-server
// authenticate) name the manager knows about, as tools.ToolName. Empty
// when no manager is installed or nothing connected.
func (a *Agent) mcpDiscoveredNames() []tools.ToolName {
	mgr := a.toolState.McpManager()
	if mgr == nil {
		return nil
	}
	names := mgr.DiscoveredToolNames()
	out := make([]tools.ToolName, 0, len(names))
	for _, n := range names {
		out = append(out, tools.ToolName(n))
	}
	return out
}

// foldMcpIntoAllowlist adds the manager's discovered names to the deferred
// allowlist so tool_search / ResolveTool admit MCP tool calls. Idempotent.
// Runs on the agent-loop goroutine (boot, or the authenticate tool's
// Execute), so no extra locking is needed beyond the existing discipline.
func (a *Agent) foldMcpIntoAllowlist() {
	for _, n := range a.mcpDiscoveredNames() {
		a.deferredAllowlist[n] = struct{}{}
	}
}

// foldMcpIntoProfile, for MAIN-tier profiles, folds the discovered MCP
// names into the deferred allowlist AND re-renders the system prompt so
// the <available-deferred-tools> block advertises them. Called in New
// after autoLoadMcp; no-op when nothing connected.
//
// Non-MAIN profiles (subagents, custom) are intentionally left untouched:
// a subagent reaches MCP tools only when its OWN profile opts in by listing
// mcp__ names in DeferredTools (handled by New's normal profile→allowlist
// path). This matches acceptance A12 — the manager (live sessions) is
// shared with subagents, but the catalog is not auto-advertised or
// auto-admitted into a subagent's allowlist.
func (a *Agent) foldMcpIntoProfile() {
	names := a.mcpDiscoveredNames()
	if len(names) == 0 {
		return
	}
	if a.profile.Type != MAIN {
		return
	}
	a.foldMcpIntoAllowlist()
	persona := a.activePersona
	if persona == "" {
		persona = "evva"
	}
	aug, err := resolveMainProfileWithExtra(
		a.cfg, a.agentRegistry, persona, a.skillRefs, a.memSnap,
		baseLLMOptions(a.profile.LLMOptions),
		a.profile.LLMProvider, a.profile.LLMModel, names, a.repoMap,
	)
	if err != nil {
		a.logger.Warn("mcp: re-render main prompt", "err", err)
		return
	}
	a.profile.SystemPrompt = aug.SystemPrompt
	a.profile.DeferredTools = aug.DeferredTools
	a.profile.LLMOptions = aug.LLMOptions
}

// mcpPromptViaQuestion adapts evva's internal question.Broker into the
// host-agnostic mcp.OAuthPromptFn shape pkg/mcp exposes. The closure
// captures brokerFn (not the Broker itself) so the indirection stays
// late-bound — the broker may not exist at agent construction time.
//
// Keeping this in internal/agent is the load-bearing constraint that lets
// pkg/mcp stay free of any internal/* dependency.
func mcpPromptViaQuestion(brokerFn func() question.Broker) mcp.OAuthPromptFn {
	return func(ctx context.Context, p mcp.OAuthPrompt) (mcp.OAuthPromptResult, error) {
		b := brokerFn()
		if b == nil {
			return mcp.OAuthCancelled, fmt.Errorf("question broker not installed")
		}
		qText := fmt.Sprintf(
			"Open this URL in your browser to authorize the %q MCP server:\n\n%s\n\nChoose \"I'm done\" once you've completed the flow in your browser.",
			p.Server, p.AuthURL,
		)
		req := question.Request{
			Questions: []question.Question{{
				Header:   "MCP auth",
				Question: qText,
				Options: []question.Option{
					{Label: "I'm done", Description: "I completed the authorization in my browser"},
					{Label: "Cancel", Description: "Don't connect this server right now"},
				},
			}},
		}
		resp, err := b.Request(ctx, req)
		if err != nil {
			return mcp.OAuthCancelled, err
		}
		if slices.Contains(resp.Answers[qText], "Cancel") {
			return mcp.OAuthCancelled, nil
		}
		return mcp.OAuthCompleted, nil
	}
}
