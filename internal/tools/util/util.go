// Package util hosts miscellaneous stateless utility tools.
package util

import "github.com/johnny1110/evva/internal/tools"

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.JSON_QUERY, tools.CALC}
}

var (
	// JSONQuery is the package-level singleton for json_query.
	JSONQuery tools.Tool = &jsonQueryTool{}
	// Calc is the package-level singleton for calc.
	Calc tools.Tool = &calcTool{}
)
