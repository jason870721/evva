package agent

import (
	"context"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/memdir/recall"
	"github.com/johnny1110/evva/pkg/llm"
)

// Wiring for semantic memory search: build the embedder (if configured),
// bind a Searcher to the memory dir, install it for the memory_search tool,
// and refresh the vector index in the background.

// wireMemorySearch installs the memory searcher. Root agent only —
// subagents get a nil provider and the tool reports memory as unavailable,
// matching how every other root-only capability behaves.
//
// Always installs a searcher when auto-memory is on, even with no embedder:
// keyword search is the zero-setup path and is the whole reason the tool
// does not require configuration to be useful.
func (a *Agent) wireMemorySearch() {
	if a.IsSubagent() || a.cfg == nil || !a.cfg.GetEnableAutoMemory() {
		return
	}
	dir := a.memSnap.MemoryDir
	if dir == "" {
		return
	}
	searcher := recall.NewSearcher(dir, a.buildEmbedder())
	a.memorySearcher = searcher
	a.toolState.SetMemorySearcher(searcher, a.memoryOrigin())
	a.refreshMemoryIndex()
}

// buildEmbedder resolves the configured embedding backend, or nil.
//
// Every failure path returns nil rather than an error: an unconfigured
// provider, an unregistered one, a missing API key. Semantic search is an
// enhancement over keyword search, so losing it must never be able to stop
// a session from starting.
func (a *Agent) buildEmbedder() llm.Embedder {
	name := a.cfg.GetEmbeddingProvider()
	if name == "" {
		return nil
	}
	api, ok := a.cfg.LLMProviderConfig[name]
	if !ok {
		// Ollama needs no credential, so an absent entry is normal there and
		// the zero APIConfig carries the default URL the factory falls back on.
		api = a.cfg.LLMProviderConfig[name]
	}
	e, err := llm.DefaultEmbedderRegistry().Build(name, a.cfg.GetEmbeddingModel(), api)
	if err != nil {
		a.logger.Warn("memory.embedder.unavailable",
			"provider", name,
			"err", err,
			"effect", "memory_search falls back to keyword matching")
		return nil
	}
	a.logger.Info("memory.embedder", "provider", name, "model", e.EmbedModel())
	return e
}

// refreshMemoryIndex brings the vector sidecar in line with the store.
//
// Runs in the BACKGROUND because a cold rebuild embeds every memory, which
// on a hosted backend is a network round trip the operator did not ask to
// wait for at startup. Searches that land before it finishes still work —
// they read whatever rows exist, and fall back to keyword mode when the
// index is empty. That is the "session starts with keyword fallback and
// upgrades when ready" behavior the PRD's risk table calls for.
func (a *Agent) refreshMemoryIndex() {
	s := a.memorySearcher
	if s == nil || !s.HasEmbedder() {
		return
	}
	go func() {
		res, err := s.Sync(context.Background())
		if err != nil {
			a.logger.Warn("memory.index.sync_failed", "err", err)
			return
		}
		if res.Embedded > 0 || res.Dropped > 0 {
			a.logger.Info("memory.index.sync",
				"embedded", res.Embedded, "dropped", res.Dropped, "total", res.Total)
		}
	}()
}

// memoryOrigin is the provenance label for memories written in this
// session, and the value scope:"project" filters on. Derived from the
// workdir the same way session storage keys are, so the two agree.
func (a *Agent) memoryOrigin() string {
	return memdir.ProjectKey(a.Workdir())
}

// MemorySearcher exposes the searcher so the dream path can re-index after
// consolidation. Nil when memory search is not wired.
func (a *Agent) MemorySearcher() *recall.Searcher { return a.memorySearcher }
