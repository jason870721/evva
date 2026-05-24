// Package lsp provides Language Server Protocol integration for evva.
//
// The lsp_request tool (deferred) allows the agent to query LSP servers for
// semantic code intelligence: go-to-definition, find references, hover, and
// document symbols. Servers are started lazily on first use and managed via
// the daemon system.
//
// See docs/roadmap/lsp.md for architecture and implementation plan.
package lsp

import "github.com/johnny1110/evva/pkg/tools"

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.LSP_REQUEST} }
