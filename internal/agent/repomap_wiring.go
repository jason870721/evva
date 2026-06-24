package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/johnny1110/evva/internal/repomap"
)

// repoMapBuildTimeout bounds repo-map construction so a cold language-server
// index (gopls/rust-analyzer can take seconds-to-minutes on a large repo)
// cannot stall session start. On timeout the builder returns whatever it
// assembled with an "(indexing — partial)" note rather than blocking (A9).
const repoMapBuildTimeout = 4 * time.Second

// buildRepoMap composes the session-open repo map and folds it into the MAIN
// prompt — the repo-map analog of foldMcpIntoProfile. Main agent only, gated on
// EnableRepoMap (off by default). Built once at construction from the wired LSP
// manager, or the glob fallback when no server is configured (A5), and cached
// on a.repoMap so every later prompt rebuild (SwitchProfile, MCP fold) reuses
// the same snapshot without re-querying the server. Best-effort: any failure
// leaves a.repoMap empty and the prompt byte-identical to a no-map session (A1).
func (a *Agent) buildRepoMap(lgr *slog.Logger) {
	if a.IsSubagent() || a.profile.Type != MAIN || !a.cfg.GetEnableRepoMap() {
		return
	}

	ctx, cancel := context.WithTimeout(a.rootCtx, repoMapBuildTimeout)
	defer cancel()

	budget := a.cfg.GetRepoMapTokenBudget()
	body := ""
	if mgr := a.toolState.LSPManager(); mgr != nil && len(mgr.Servers()) > 0 {
		if m, err := repomap.Build(ctx, mgr, a.workdir, budget); err == nil {
			body = m
		} else {
			lgr.Debug("repomap: LSP build failed, using fallback", "err", err)
		}
	}
	if body == "" {
		// No server (or the sweep failed) → coarse glob outline (A5).
		if m, err := repomap.BuildFallback(ctx, a.workdir, budget); err == nil {
			body = m
		} else {
			lgr.Debug("repomap: fallback build failed", "err", err)
			return
		}
	}
	a.repoMap = body

	// Re-render the MAIN prompt with the map folded in, threading the live MCP
	// catalog so a prior MCP fold isn't lost (mirrors foldMcpIntoProfile).
	// baseLLMOptions strips the stale WithSystem option so the re-render doesn't
	// stack duplicate system prompts.
	persona := a.activePersona
	if persona == "" {
		persona = "evva"
	}
	aug, err := resolveMainProfileWithExtra(
		a.cfg, a.agentRegistry, persona, a.skillRefs, a.memSnap,
		baseLLMOptions(a.profile.LLMOptions),
		a.profile.LLMProvider, a.profile.LLMModel, a.mcpDiscoveredNames(), a.repoMap,
	)
	if err != nil {
		lgr.Warn("repomap: re-render main prompt", "err", err)
		return
	}
	a.profile.SystemPrompt = aug.SystemPrompt
	a.profile.LLMOptions = aug.LLMOptions
	lgr.Info("repomap: injected into main prompt", "bytes", len(a.repoMap))
}
