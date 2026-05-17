package toolset

import "github.com/johnny1110/evva/internal/tools"

// toolTags maps each tool name to a short list of keywords useful for
// TOOL_SEARCH ranking. Pick the words the LLM would naturally type when
// looking for the tool — concrete, 1–2 words each, no marketing fluff.
//
// Empty list = no tag-based boost (the matcher falls back to name + desc
// hits). A nil entry is fine; a missing entry returns nil from TagsFor.
var toolTags = map[tools.ToolName][]string{
	// fs
	tools.READ_FILE:  {"file", "read", "open", "io", "filesystem"},
	tools.WRITE_FILE: {"file", "write", "create", "io", "filesystem"},
	tools.EDIT_FILE:  {"file", "edit", "modify", "replace", "io"},

	// shell
	tools.BASH: {"shell", "command", "exec", "process", "git", "cli", "run"},
	tools.GREP: {"search", "grep", "regex", "find", "text", "match"},
	tools.TREE: {"list", "directory", "tree", "filesystem", "structure"},

	// meta
	tools.AGENT:           {"subagent", "delegate", "spawn", "parallel", "task"},
	tools.TOOL_SEARCH:     {"tool", "search", "discover", "schema", "lookup"},
	tools.SKILL:           {"skill", "invoke", "instructions", "markdown", "user-defined"},
	tools.SCHEDULE_WAKEUP: {"loop", "schedule", "wake", "pace", "interval"},

	// task
	tools.TASK_CREATE: {"task", "todo", "create", "track", "plan"},
	tools.TASK_GET:    {"task", "todo", "get", "fetch", "detail"},
	tools.TASK_LIST:   {"task", "todo", "list", "all", "overview"},
	tools.TASK_UPDATE: {"task", "todo", "update", "status", "progress"},
	tools.TASK_OUTPUT: {"task", "output", "log", "stdout", "result"},
	tools.TASK_STOP:   {"task", "stop", "kill", "cancel", "abort"},

	// monitor / mode / notebook
	tools.MONITOR:         {"watch", "tail", "follow", "stream", "stdout", "process"},
	tools.ENTER_PLAN_MODE: {"plan", "mode", "design", "readonly", "preview"},
	tools.EXIT_PLAN_MODE:  {"plan", "mode", "exit", "approve", "implement"},
	tools.ENTER_WORKTREE:  {"worktree", "git", "isolate", "branch", "create"},
	tools.EXIT_WORKTREE:   {"worktree", "git", "exit", "discard", "leave"},
	tools.NOTEBOOK_EDIT:   {"notebook", "jupyter", "ipynb", "cell", "edit"},

	// cron / web / ux
	tools.CRON_CREATE:    {"schedule", "cron", "recurring", "timer", "future"},
	tools.CRON_LIST:      {"schedule", "cron", "list", "jobs"},
	tools.CRON_DELETE:    {"schedule", "cron", "delete", "cancel"},
	tools.REMOTE_TRIGGER: {"remote", "trigger", "webhook", "api", "external"},

	tools.WEB_FETCH:  {"http", "url", "web", "fetch", "scrape", "html"},
	tools.WEB_SEARCH: {"web", "search", "google", "internet", "lookup", "query"},

	tools.ASK_USER_QUESTION: {"ask", "user", "question", "prompt", "interact", "clarify"},
	tools.PUSH_NOTIFICATION: {"notify", "notification", "alert", "ping", "remind"},
}

// TagsFor returns the keywords associated with a tool name, or nil if none
// are declared. Safe to call for unknown names — returns nil.
func TagsFor(name tools.ToolName) []string {
	return toolTags[name]
}
