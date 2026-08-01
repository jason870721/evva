// Package memory holds the tools that read evva's typed-memory store.
//
// Writes deliberately have no tool — the model creates memory files with the
// ordinary write/edit tools under a permission carve-out — so this package is
// the read half only.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/memdir/recall"
	"github.com/johnny1110/evva/pkg/tools"
)

// Provider is the searching half of the memory store, resolved lazily.
//
// Late-bound rather than injected at construction because the memory dir and
// the embedder are settled during agent boot — after the tool registry is
// built — and because /profile swaps rebuild the agent underneath a tool set
// that outlives it. Same pattern as the worktree controller lookup.
type Provider interface {
	Search(ctx context.Context, query string, k int, minScore float64) ([]recall.Hit, string)
	HasEmbedder() bool
}

const (
	// defaultLimit is what a call with no limit returns. Small on purpose:
	// each hit is a whole memory body in context, and the point of search is
	// to be cheaper than reading the store, not to reproduce it.
	defaultLimit = 5
	maxLimit     = 20

	// defaultMinScore is the cosine floor for semantic hits. Below this,
	// results are typically topically adjacent but not useful, and a wrong
	// memory presented confidently is worse than no memory — the model has
	// no way to tell that a recalled note does not apply.
	defaultMinScore = 0.35
)

// SearchTool implements tools.Tool for memory_search.
type SearchTool struct {
	lookup func() Provider
	// origin identifies the current project, so scope:"project" can filter.
	origin func() string
}

// NewSearch builds the tool. Both closures may be nil or return nil — the
// tool then reports that memory search is unavailable rather than failing,
// which is the correct behavior for a subagent or a session with auto-memory
// switched off.
func NewSearch(lookup func() Provider, origin func() string) *SearchTool {
	return &SearchTool{lookup: lookup, origin: origin}
}

func (t *SearchTool) Name() string { return string(tools.MEMORY_SEARCH) }

func (t *SearchTool) Description() string {
	return `Search your memory directory for notes relevant to a query.

Your memory holds typed notes you wrote in earlier sessions: user (who you are working with), feedback (how they want you to work), project (ongoing goals and constraints), and reference (pointers to external resources).

Relevant memories are already surfaced automatically at the start of each user turn. Use this tool when that was not enough — you have realized mid-task that you need something you were not given:

- You are about to make a decision the user has weighed in on before ("how do they want PRs opened?").
- You hit a constraint that feels like it was established earlier ("is there a reason this build needs Node 24?").
- The user refers to prior work you cannot see ("do it the way we did for the web2 dashboard").

Query in natural language, describing what you need to know. Matching is semantic where an embedding backend is configured, so a query need not use the same words as the note.

Results are memories, not facts: they are point-in-time notes that may have gone stale. Verify anything load-bearing before you rely on it, and ignore hits that clearly do not apply to the current request. If a hit looks relevant, read the file for its full body.`
}

func (t *SearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "What you need to know, in natural language. Describe the topic rather than guessing a filename."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum memories to return (default 5, max 20)."
    },
    "scope": {
      "type": "string",
      "enum": ["all", "project"],
      "description": "\"all\" (default) searches every memory, including lessons recorded while working on other projects. \"project\" restricts to memories recorded in this one — use it when a general answer would be misleading, e.g. build or deploy conventions."
    }
  },
  "required": ["query"]
}`)
}

type searchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
	Scope string `json:"scope"`
}

func (t *SearchTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in searchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{Content: "memory_search: invalid input: " + err.Error(), IsError: true}, nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return tools.Result{Content: "memory_search: query is required.", IsError: true}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	provider := t.provider()
	if provider == nil {
		return tools.Result{
			Content: "memory_search: memory is not available in this session (auto-memory is off, or this is a subagent).",
		}, nil
	}

	hits, mode := provider.Search(ctx, in.Query, limit, defaultMinScore)
	if in.Scope == "project" {
		hits = filterOrigin(hits, t.currentOrigin())
	}
	if logger != nil {
		logger.Debug("memory.search", "query", in.Query, "mode", mode, "hits", len(hits), "scope", in.Scope)
	}
	if len(hits) == 0 {
		return tools.Result{Content: noHitsMessage(mode, provider.HasEmbedder())}, nil
	}
	return tools.Result{Content: renderHits(hits, mode, provider.HasEmbedder())}, nil
}

func (t *SearchTool) provider() Provider {
	if t == nil || t.lookup == nil {
		return nil
	}
	return t.lookup()
}

func (t *SearchTool) currentOrigin() string {
	if t == nil || t.origin == nil {
		return ""
	}
	return t.origin()
}

// filterOrigin keeps only hits from the given project. A hit with no
// recorded origin is DROPPED rather than kept: scope:"project" is asked for
// when a general answer would mislead, so an unattributed memory is exactly
// the thing the caller wanted excluded.
func filterOrigin(hits []recall.Hit, origin string) []recall.Hit {
	if origin == "" {
		return nil
	}
	out := hits[:0]
	for _, h := range hits {
		if h.Origin == origin {
			out = append(out, h)
		}
	}
	return out
}

// noHitsMessage explains an empty result differently depending on how the
// search ran, so the model can tell "nothing matched" from "matching is weak
// here" and decide whether to rephrase.
func noHitsMessage(mode string, semantic bool) string {
	if mode == recall.ModeKeyword && !semantic {
		return "No memories matched. Note: no embedding backend is configured, so this was a keyword search — " +
			"a memory phrased differently from your query would not be found. Rephrase with likely literal terms, or proceed without it."
	}
	return "No memories matched that query."
}

// renderHits formats the result set. Names, scores and descriptions only —
// the model reads full bodies through the normal read path when a hit looks
// worth it, which keeps search cheap and the files the single source of truth.
func renderHits(hits []recall.Hit, mode string, semantic bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d memor%s matched (%s ranking).\n", len(hits), plural(len(hits)), mode)
	if mode == recall.ModeKeyword && !semantic {
		b.WriteString("No embedding backend configured — this was a literal keyword match, so related notes phrased differently were not found.\n")
	}
	b.WriteString("\nThese are point-in-time notes. Verify anything load-bearing, and read the file for the full body.\n")
	for _, h := range hits {
		b.WriteString("\n")
		fmt.Fprintf(&b, "- %s (score %.2f)", h.Header.Filename, h.Score)
		if h.Header.Type != "" {
			fmt.Fprintf(&b, " [%s]", h.Header.Type)
		}
		if h.Origin != "" {
			fmt.Fprintf(&b, " [from %s]", h.Origin)
		}
		b.WriteString("\n")
		if h.Header.Description != "" {
			fmt.Fprintf(&b, "  %s\n", h.Header.Description)
		}
		if note := memdir.FreshnessText(h.Header.ModTime); note != "" {
			fmt.Fprintf(&b, "  %s\n", note)
		}
		fmt.Fprintf(&b, "  path: %s\n", h.Header.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// Names reports the tool names this family owns, matching the convention
// every other internal/tools/<family> package follows.
func Names() []tools.ToolName { return []tools.ToolName{tools.MEMORY_SEARCH} }
