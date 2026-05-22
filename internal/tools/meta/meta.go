// Package meta hosts agent-meta tools: Agent (spawn sub-agent), ToolSearch
// (load deferred-tool schemas), and ScheduleWakeup (self-pace /loop
// iterations). Each needs an agent-side hook supplied via constructor
// injection from the toolset Builders.
//
// SKILL lives in its own package (pkg/skill) because it owns a registry of
// user-installed skill files; co-locating it there keeps the loader and the
// tool adjacent. The package is public so downstream SDK consumers can
// register programmatic skills via skill.NewRegistry + Add.
package meta

import "github.com/johnny1110/evva/pkg/tools"

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.AGENT, tools.TOOL_SEARCH, tools.SCHEDULE_WAKEUP}
}
