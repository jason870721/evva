package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

// lspTool is the AI-facing tool that dispatches LSP operations.
type lspTool struct {
	manager *Manager
	workDir string
}

// NewTool creates an lspTool bound to the given manager.
func NewTool(mgr *Manager, workDir string) *lspTool {
	return &lspTool{manager: mgr, workDir: workDir}
}

func (t *lspTool) Name() string { return string(tools.LSP_REQUEST) }

func (t *lspTool) Description() string {
	return "Query language servers for semantic code intelligence. " +
		"Supports go-to-definition, find references, hover, document symbols, " +
		"workspace-wide symbol search, go-to-implementation, and call hierarchy " +
		"(incoming/outgoing calls). The server starts automatically on first use. " +
		"Use for: navigating to definitions, finding all usages of a function/type, " +
		"inspecting type info, listing file symbols, searching the workspace for a symbol, " +
		"jumping to interface/type implementations, or tracing the call graph."
}

func (t *lspTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "required": ["operation"],
  "oneOf": [
    {
      "properties": {
        "operation": {"const": "go_to_definition"},
        "filePath": {"type": "string", "description": "Absolute path to the source file."},
        "line": {"type": "integer", "description": "1-indexed line number."},
        "character": {"type": "integer", "description": "1-indexed character (UTF-16 code units)."}
      }
    },
    {
      "properties": {
        "operation": {"const": "find_references"},
        "filePath": {"type": "string", "description": "Absolute path to the source file."},
        "line": {"type": "integer", "description": "1-indexed line number."},
        "character": {"type": "integer", "description": "1-indexed character (UTF-16 code units)."}
      }
    },
    {
      "properties": {
        "operation": {"const": "hover"},
        "filePath": {"type": "string", "description": "Absolute path to the source file."},
        "line": {"type": "integer", "description": "1-indexed line number."},
        "character": {"type": "integer", "description": "1-indexed character (UTF-16 code units)."}
      }
    },
    {
      "properties": {
        "operation": {"const": "document_symbols"},
        "filePath": {"type": "string", "description": "Absolute path to the source file."}
      }
    },
    {
      "properties": {
        "operation": {"const": "workspace_symbol"},
        "query": {"type": "string", "description": "Search query for workspace-wide symbol search."},
        "filePath": {"type": "string", "description": "Optional: any project file to determine which LSP server to query."}
      }
    },
    {
      "properties": {
        "operation": {"const": "go_to_implementation"},
        "filePath": {"type": "string", "description": "Absolute path to the source file."},
        "line": {"type": "integer", "description": "1-indexed line number."},
        "character": {"type": "integer", "description": "1-indexed character (UTF-16 code units)."}
      }
    },
    {
      "properties": {
        "operation": {"const": "prepare_call_hierarchy"},
        "filePath": {"type": "string", "description": "Absolute path to the source file."},
        "line": {"type": "integer", "description": "1-indexed line number."},
        "character": {"type": "integer", "description": "1-indexed character (UTF-16 code units)."}
      }
    },
    {
      "properties": {
        "operation": {"const": "incoming_calls"},
        "item": {"type": "object", "description": "A CallHierarchyItem JSON object from a previous prepare_call_hierarchy result."}
      }
    },
    {
      "properties": {
        "operation": {"const": "outgoing_calls"},
        "item": {"type": "object", "description": "A CallHierarchyItem JSON object from a previous prepare_call_hierarchy result."}
      }
    }
  ]
}`)
}

type lspInput struct {
	Operation string          `json:"operation"`
	FilePath  string          `json:"filePath"`
	Line      int             `json:"line"`
	Character int             `json:"character"`
	Query     string          `json:"query"`
	Item      json.RawMessage `json:"item"`
}

func (t *lspTool) Execute(ctx context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	var in lspInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: decode: %v", err)}, nil
	}

	if t.manager == nil {
		return tools.Result{IsError: true, Content: "lsp_request: LSP manager is not configured"}, nil
	}

	switch in.Operation {
	case "go_to_definition", "find_references", "hover", "document_symbols",
		"go_to_implementation", "prepare_call_hierarchy":
		return t.executeFileOp(ctx, logger, in)
	case "workspace_symbol":
		return t.executeWorkspaceSymbol(ctx, logger, in)
	case "incoming_calls", "outgoing_calls":
		return t.executeCallHierarchyStep(ctx, logger, in)
	default:
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: unknown operation %q", in.Operation)}, nil
	}
}

// executeFileOp handles operations that need a file path.
func (t *lspTool) executeFileOp(ctx context.Context, logger *slog.Logger, in lspInput) (tools.Result, error) {
	filePath := t.resolvePath(in.FilePath)
	if !fileExists(filePath) {
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: file not found: %s", filePath)}, nil
	}

	srv, err := t.manager.EnsureServerStarted(ctx, filePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: server start: %v", err)}, nil
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: read file: %v", err)}, nil
	}

	t.manager.NotifyFileChanged(filePath)

	if err := t.manager.OpenFile(ctx, filePath, string(content)); err != nil {
		logger.Debug("lsp_request.openFile", "err", err)
	}
	defer func() {
		if err := t.manager.CloseFile(ctx, filePath); err != nil {
			logger.Debug("lsp_request.closeFile", "err", err)
		}
	}()

	switch in.Operation {
	case "go_to_definition":
		return t.goToDefinition(ctx, srv, filePath, in.Line, in.Character)
	case "find_references":
		return t.findReferences(ctx, srv, filePath, in.Line, in.Character)
	case "hover":
		return t.hover(ctx, srv, filePath, in.Line, in.Character)
	case "document_symbols":
		return t.documentSymbols(ctx, srv, filePath)
	case "go_to_implementation":
		return t.goToImplementation(ctx, srv, filePath, in.Line, in.Character)
	case "prepare_call_hierarchy":
		return t.prepareCallHierarchy(ctx, srv, filePath, in.Line, in.Character)
	default:
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: unknown operation %q", in.Operation)}, nil
	}
}

// executeWorkspaceSymbol handles workspace/symbol — no file needed.
func (t *lspTool) executeWorkspaceSymbol(ctx context.Context, _ *slog.Logger, in lspInput) (tools.Result, error) {
	if in.Query == "" {
		return tools.Result{IsError: true, Content: "lsp_request: query is required for workspace_symbol"}, nil
	}

	// Resolve a server — use filePath if provided, otherwise first registered.
	var srv *Server
	if in.FilePath != "" {
		filePath := t.resolvePath(in.FilePath)
		var ok bool
		srv, ok = t.manager.ServerForFile(filePath)
		if !ok {
			return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: no LSP server for %s", filePath)}, nil
		}
		srv, err := t.manager.EnsureServerStarted(ctx, filePath)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: server start: %v", err)}, nil
		}
		_ = srv
		_ = err
		srv, err = t.manager.EnsureServerStarted(ctx, filePath)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: server start: %v", err)}, nil
		}
	} else {
		names := t.manager.Servers()
		if len(names) == 0 {
			return tools.Result{IsError: true, Content: "lsp_request: no LSP servers configured"}, nil
		}
		// Use first server — find a file with a matching extension to start it.
		firstName := names[0]
		srv, _ = t.manager.ServerForFile("/dummy." + firstName)
		if srv == nil {
			return tools.Result{IsError: true, Content: "lsp_request: no LSP server available"}, nil
		}
		var err error
		// Try to ensure any server is started by using a dummy path with the right ext.
		// Manager.Servers() returns names; we need to map name to extension.
		srv, err = t.manager.EnsureServerStarted(ctx, "/dummy."+firstName)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: server start: %v", err)}, nil
		}
	}

	params := protocol.WorkspaceSymbolParams{Query: in.Query}
	raw, err := srv.Request(ctx, protocol.MethodWorkspaceSymbol, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("workspace_symbol: %v", err)}, nil
	}
	return tools.Result{Content: formatWorkspaceSymbols(raw, in.Query)}, nil
}

// executeCallHierarchyStep handles incoming_calls / outgoing_calls.
func (t *lspTool) executeCallHierarchyStep(ctx context.Context, _ *slog.Logger, in lspInput) (tools.Result, error) {
	if len(in.Item) == 0 {
		return tools.Result{IsError: true, Content: "lsp_request: item is required for call hierarchy operations"}, nil
	}

	// Parse the item to extract its URI for server resolution.
	var item protocol.CallHierarchyItem
	if err := json.Unmarshal(in.Item, &item); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: item parse: %v", err)}, nil
	}

	filePath := fileURIToPath(item.URI)
	if filePath == "" {
		return tools.Result{IsError: true, Content: "lsp_request: item has no URI"}, nil
	}

	srv, err := t.manager.EnsureServerStarted(ctx, filePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("lsp_request: server start: %v", err)}, nil
	}

	var raw json.RawMessage
	if in.Operation == "incoming_calls" {
		params := protocol.CallHierarchyIncomingCallsParams{Item: item}
		raw, err = srv.Request(ctx, protocol.MethodIncomingCalls, params)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("incoming_calls: %v", err)}, nil
		}
		return tools.Result{Content: formatIncomingCalls(raw)}, nil
	}

	params := protocol.CallHierarchyOutgoingCallsParams{Item: item}
	raw, err = srv.Request(ctx, protocol.MethodOutgoingCalls, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("outgoing_calls: %v", err)}, nil
	}
	return tools.Result{Content: formatOutgoingCalls(raw)}, nil
}

// resolvePath resolves a file path relative to the workdir.
func (t *lspTool) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(t.workDir, p))
}

// ── operation handlers ─────────────────────────────────────────────────

func (t *lspTool) goToDefinition(ctx context.Context, srv *Server, filePath string, line, character int) (tools.Result, error) {
	params := protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(filePath)},
		Position:     toPosition(line, character),
	}
	raw, err := srv.Request(ctx, protocol.MethodDefinition, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("definition: %v", err)}, nil
	}
	return tools.Result{Content: formatDefinition(raw)}, nil
}

func (t *lspTool) findReferences(ctx context.Context, srv *Server, filePath string, line, character int) (tools.Result, error) {
	params := protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(filePath)},
			Position:     toPosition(line, character),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	}
	raw, err := srv.Request(ctx, protocol.MethodReferences, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("references: %v", err)}, nil
	}
	return tools.Result{Content: formatReferences(raw)}, nil
}

func (t *lspTool) hover(ctx context.Context, srv *Server, filePath string, line, character int) (tools.Result, error) {
	params := protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(filePath)},
		Position:     toPosition(line, character),
	}
	raw, err := srv.Request(ctx, protocol.MethodHover, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("hover: %v", err)}, nil
	}
	return tools.Result{Content: formatHover(raw)}, nil
}

func (t *lspTool) documentSymbols(ctx context.Context, srv *Server, filePath string) (tools.Result, error) {
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(filePath)},
	}
	raw, err := srv.Request(ctx, protocol.MethodDocumentSymbol, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("documentSymbols: %v", err)}, nil
	}
	return tools.Result{Content: formatDocumentSymbols(raw)}, nil
}

func (t *lspTool) goToImplementation(ctx context.Context, srv *Server, filePath string, line, character int) (tools.Result, error) {
	params := protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(filePath)},
		Position:     toPosition(line, character),
	}
	raw, err := srv.Request(ctx, protocol.MethodImplementation, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("implementation: %v", err)}, nil
	}
	return tools.Result{Content: formatImplementation(raw)}, nil
}

func (t *lspTool) prepareCallHierarchy(ctx context.Context, srv *Server, filePath string, line, character int) (tools.Result, error) {
	params := protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(filePath)},
		Position:     toPosition(line, character),
	}
	raw, err := srv.Request(ctx, protocol.MethodPrepareCallHierarchy, params)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("callHierarchy: %v", err)}, nil
	}
	return tools.Result{Content: formatCallHierarchy(raw)}, nil
}

// toPosition converts 1-indexed line/character to a 0-indexed protocol.Position.
// The model is told to use 1-indexed values; LSP servers expect 0-indexed.
func toPosition(line, character int) protocol.Position {
	line = max(line-1, 0)
	character = max(character-1, 0)
	return protocol.Position{
		Line:      uint32(line),
		Character: uint32(character),
	}
}
