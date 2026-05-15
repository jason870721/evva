// Package task exposes the six task tools (Create, Get, List, Update, Output,
// Stop). They are stateful and share one *TaskGroup per agent — the agent's
// toolset.Builders provides the TaskGroup and threads it through each constructor.
package task

import "github.com/johnny1110/evva/internal/tools"

// Names lists every tool name this package contributes, in canonical order.
func Names() []tools.ToolName {
	return []tools.ToolName{
		tools.TASK_CREATE,
		tools.TASK_GET,
		tools.TASK_LIST,
		tools.TASK_UPDATE,
		tools.TASK_OUTPUT,
		tools.TASK_STOP,
	}
}

func IsTaskToolName(n tools.ToolName) bool {
	switch n {
	case tools.TASK_CREATE, tools.TASK_GET, tools.TASK_LIST,
		tools.TASK_UPDATE, tools.TASK_OUTPUT, tools.TASK_STOP:
		return true
	}
	return false
}
