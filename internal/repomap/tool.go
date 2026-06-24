package repomap

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/lsp"
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.REPO_MAP} }

// repoMapTool is the repo_map zoom tool: an on-demand, higher-detail view of a
// subtree's symbols than the session-open overview, sourced from the LSP layer
// (with a glob fallback when no server is configured). Read-only and
// concurrency-safe — it issues the same read-only LSP queries lsp_request does.
type repoMapTool struct {
	mgr     *lsp.Manager
	workDir string
	budget  int
}

// NewTool builds the repo_map tool. budget is the overview token budget
// (RepoMapTokenBudget); "full" detail gets a larger budget at execute time.
func NewTool(mgr *lsp.Manager, workDir string, budget int) *repoMapTool {
	return &repoMapTool{mgr: mgr, workDir: workDir, budget: budget}
}

func (t *repoMapTool) Name() string { return string(tools.REPO_MAP) }

func (t *repoMapTool) Description() string {
	return "Zoom into a directory or package's symbol outline — ranked types, " +
		"functions, and (in full detail) their members with signatures — sourced " +
		"from the language server. Complements the session-open repo map: call this " +
		"when you need a higher-detail view of a specific subtree than the overview " +
		"gave you, or to re-read a part of the tree you've been changing. Falls back " +
		"to a coarse, grep-derived outline when no language server is configured."
}

func (t *repoMapTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Directory or package path to map, relative to the workspace root (or absolute). Defaults to the whole repository."
    },
    "detail": {
      "type": "string",
      "enum": ["overview", "full"],
      "description": "overview (default): ranked top-level declarations with signatures. full: also include members (methods/fields), with a larger budget."
    }
  }
}`)
}

type repoMapInput struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

func (t *repoMapTool) Execute(ctx context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	var in repoMapInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("repo_map: decode: %v", err)}, nil
		}
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		path = "."
	}
	detail := "overview"
	if in.Detail == "full" {
		detail = "full"
	}
	budget := t.budget
	if budget <= 0 {
		budget = 2000
	}
	if detail == "full" {
		budget *= 2 // full detail carries members — give it room (A7).
	}

	if t.mgr != nil && len(t.mgr.Servers()) > 0 {
		if out, err := BuildPath(ctx, t.mgr, t.workDir, path, detail, budget); err == nil {
			return tools.Result{Content: out}, nil
		} else {
			logger.Debug("repo_map: LSP zoom failed, using glob fallback", "err", err)
		}
	}

	// No language server (or the sweep failed) → coarse glob outline of the
	// requested subtree: BuildFallback walks from the resolved path as its root.
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(t.workDir, path)
	}
	out, err := BuildFallback(ctx, target, budget)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("repo_map: %v", err)}, nil
	}
	return tools.Result{Content: out}, nil
}
