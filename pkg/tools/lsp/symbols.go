package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

// Symbol is a wire-type-free view of an LSP symbol — the neutral shape the
// repo-map builder consumes so it never imports pkg/tools/lsp/protocol. It
// flattens both the workspace/symbol (flat SymbolInformation) and the
// textDocument/documentSymbol (hierarchical DocumentSymbol) responses into one
// representation. Kept here, in pkg/tools/lsp, so *Manager satisfies the
// builder's Source interface without that interface depending on protocol.
type Symbol struct {
	Name      string // declared name, e.g. "Manager" or "EnsureServerStarted"
	Kind      string // human-readable kind ("Struct", "Function", "Method", …)
	Detail    string // signature/detail when the server reports it (document symbols)
	File      string // absolute file path
	Line      int    // 1-indexed declaration line
	Container string // enclosing symbol (parent in a document-symbol tree); "" at top level
}

// WorkspaceSymbols runs a workspace/symbol query against the first configured
// server and returns the results as neutral Symbols. An empty query asks the
// server for everything it knows (gopls and most servers treat "" as a broad
// sweep), which is what the repo map wants. Returns an error when no server is
// configured or the request fails — the caller degrades to the glob fallback.
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	names := m.Servers()
	if len(names) == 0 {
		return nil, fmt.Errorf("no LSP servers configured")
	}
	// Mirror executeWorkspaceSymbol's first-server resolution: a workspace
	// query is server-wide, so any healthy server's index will do. We poke it
	// with a dummy path carrying the server's extension to drive EnsureServerStarted.
	firstName := names[0]
	ext := m.FirstExtensionFor(firstName)
	if ext == "" {
		return nil, fmt.Errorf("no file extension for server %q", firstName)
	}
	srv, err := m.EnsureServerStarted(ctx, "/dummy"+ext)
	if err != nil {
		return nil, fmt.Errorf("server start: %w", err)
	}

	raw, err := srv.Request(ctx, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: query})
	if err != nil {
		return nil, err
	}
	if isEmpty(raw) {
		return nil, nil
	}
	var infos []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &infos); err != nil {
		return nil, fmt.Errorf("decode workspace symbols: %w", err)
	}
	out := make([]Symbol, 0, len(infos))
	for i := range infos {
		out = append(out, Symbol{
			Name: infos[i].Name,
			Kind: infos[i].Kind.String(),
			File: fileURIToPath(infos[i].Location.URI),
			Line: int(infos[i].Location.Range.Start.Line) + 1,
		})
	}
	return out, nil
}

// DocumentSymbols runs textDocument/documentSymbol for one file and returns the
// outline as neutral Symbols, flattening the hierarchical response (a tree of
// types → members) into a depth-first slice with Container set to the parent's
// name. Opens and closes the document around the request, exactly like the
// lsp_request document_symbols path (executeFileOp). Returns an error when the
// file is unreadable or no server handles its extension.
func (m *Manager) DocumentSymbols(ctx context.Context, path string) ([]Symbol, error) {
	srv, err := m.EnsureServerStarted(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("server start: %w", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	m.NotifyFileChanged(path)
	if err := m.OpenFile(ctx, path, string(content)); err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = m.CloseFile(ctx, path) }()

	raw, err := srv.Request(ctx, protocol.MethodDocumentSymbol, protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI(path)},
	})
	if err != nil {
		return nil, err
	}
	if isEmpty(raw) {
		return nil, nil
	}

	// Servers may return the hierarchical DocumentSymbol shape or the flat
	// SymbolInformation shape — same dual-decode as formatDocumentSymbols.
	var tree []protocol.DocumentSymbol
	if err := json.Unmarshal(raw, &tree); err == nil && len(tree) > 0 {
		var out []Symbol
		flattenDocSymbols(tree, "", path, &out)
		return out, nil
	}
	var infos []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &infos); err != nil {
		return nil, fmt.Errorf("decode document symbols: %w", err)
	}
	out := make([]Symbol, 0, len(infos))
	for i := range infos {
		file := fileURIToPath(infos[i].Location.URI)
		if file == "" {
			file = path
		}
		out = append(out, Symbol{
			Name: infos[i].Name,
			Kind: infos[i].Kind.String(),
			File: file,
			Line: int(infos[i].Location.Range.Start.Line) + 1,
		})
	}
	return out, nil
}

// flattenDocSymbols walks a DocumentSymbol tree depth-first, recording each
// node with its parent's name as Container.
func flattenDocSymbols(syms []protocol.DocumentSymbol, container, path string, out *[]Symbol) {
	for i := range syms {
		s := syms[i]
		*out = append(*out, Symbol{
			Name:      s.Name,
			Kind:      s.Kind.String(),
			Detail:    s.Detail,
			File:      path,
			Line:      int(s.Range.Start.Line) + 1,
			Container: container,
		})
		if len(s.Children) > 0 {
			children := make([]protocol.DocumentSymbol, len(s.Children))
			for j, c := range s.Children {
				children[j] = *c
			}
			flattenDocSymbols(children, s.Name, path, out)
		}
	}
}
