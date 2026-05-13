// Package fs exposes filesystem tools (Read, Write, Edit) as stateless
// singletons. Construction policy (eager vs lazy) is decided by the agent;
// this package only knows how to produce tool instances.
package fs

import "github.com/johnny1110/evva/internal/tools"

// Names lists every tool name this package contributes, in canonical order.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.READ_FILE, tools.WRITE_FILE, tools.EDIT_FILE}
}
