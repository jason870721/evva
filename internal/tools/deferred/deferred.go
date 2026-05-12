// Package deferred aggregates every "load with ToolSearch first" tool name
// in one bundle.
//
// Deferred tools surface to the LLM by name in system reminders, but their
// schemas are only loaded on demand via the ToolSearch tool. Importing this
// package triggers init() in every deferred sub-package, which registers
// each tool (or stateful tool group) with the central tools registry.
// Use Names() to fetch the full name list for a profile.
package deferred

import (
	"slices"

	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/deferred/cron"
	"github.com/johnny1110/evva/internal/tools/deferred/mode"
	"github.com/johnny1110/evva/internal/tools/deferred/monitor"
	"github.com/johnny1110/evva/internal/tools/deferred/notebook"
	"github.com/johnny1110/evva/internal/tools/deferred/task"
	"github.com/johnny1110/evva/internal/tools/deferred/ux"
	"github.com/johnny1110/evva/internal/tools/deferred/web"
)

// Names lists every deferred tool name, in canonical order:
// task, monitor, mode, notebook, ux, cron, web.
func Names() []tools.ToolName {
	return slices.Concat(
		task.Names(),
		monitor.Names(),
		mode.Names(),
		notebook.Names(),
		ux.Names(),
		cron.Names(),
		web.Names(),
	)
}
