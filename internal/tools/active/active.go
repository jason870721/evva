// Package active aggregates every "ready to call" tool name in one bundle.
//
// Active tools require no schema lookup before invocation — the LLM sees them
// directly in each Complete call. See docs/claude-tool for the active vs
// deferred split. Importing this package triggers init() in every active
// sub-package, which registers each tool with the central tools registry.
// Use Names() to fetch the full name list for a profile.
package active

import (
	"slices"

	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/active/fs"
	"github.com/johnny1110/evva/internal/tools/active/meta"
	"github.com/johnny1110/evva/internal/tools/active/shell"
)

// Names lists every active tool name, in canonical order:
// fs (Read/Write/Edit), shell (Bash), meta (Agent/ToolSearch/Skill/Wakeup).
func Names() []tools.ToolName {
	return slices.Concat(fs.Names(), shell.Names(), meta.Names())
}
