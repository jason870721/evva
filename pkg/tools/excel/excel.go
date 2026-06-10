// Package excel provides Excel (.xlsx) file manipulation via the excelize library.
//
// The excel tool (deferred) allows the agent to read, write, create, and
// manipulate Excel spreadsheets: cell values, formulas, sheet management,
// charts, pivot tables, data validation, and more.
package excel

import "github.com/johnny1110/evva/pkg/tools"

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.EXCEL} }
