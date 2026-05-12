// Package fs exposes filesystem tools (Read, Write, Edit) as stateless
// singletons. The names live in tools.READ_FILE / WRITE_FILE / EDIT_FILE;
// init() registers them with the central tools registry so callers can
// resolve by name via tools.Build.
package fs

import "github.com/johnny1110/evva/internal/tools"

func init() {
	tools.Register(tools.READ_FILE, Read)
	tools.Register(tools.WRITE_FILE, Write)
	tools.Register(tools.EDIT_FILE, Edit)
}

// Names lists every tool name this package contributes, in canonical order.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.READ_FILE, tools.WRITE_FILE, tools.EDIT_FILE}
}
