package toolset

import "github.com/johnny1110/evva/pkg/tools"

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

	// todo
	tools.TODO_WRITE: {"todo", "task", "plan", "track", "progress", "list"},

	// monitor / mode / notebook
	tools.MONITOR:         {"watch", "tail", "follow", "stream", "stdout", "process"},
	tools.ENTER_PLAN_MODE: {"plan", "mode", "design", "readonly", "preview"},
	tools.EXIT_PLAN_MODE:  {"plan", "mode", "exit", "approve", "implement"},
	tools.ENTER_WORKTREE:  {"worktree", "git", "isolate", "branch", "create"},
	tools.EXIT_WORKTREE:   {"worktree", "git", "exit", "discard", "leave"},
	tools.NOTEBOOK_EDIT:   {"notebook", "jupyter", "ipynb", "cell", "edit"},

	// lsp
	tools.LSP_REQUEST: {"lsp", "language", "definition", "references", "hover", "symbols", "semantic", "code", "intelligence"},

	// cron / web / ux
	tools.CRON_CREATE:    {"schedule", "cron", "recurring", "timer", "future"},
	tools.CRON_LIST:      {"schedule", "cron", "list", "jobs"},
	tools.CRON_DELETE:    {"schedule", "cron", "delete", "cancel"},
	tools.REMOTE_TRIGGER: {"remote", "trigger", "webhook", "api", "external"},

	tools.WEB_FETCH:  {"http", "url", "web", "fetch", "scrape", "html"},
	tools.WEB_SEARCH: {"web", "search", "google", "internet", "lookup", "query"},

	tools.ASK_USER_QUESTION: {"ask", "user", "question", "prompt", "interact", "clarify"},
	tools.PUSH_NOTIFICATION: {"notify", "notification", "alert", "ping", "remind"},

	// utils
	tools.JSON_QUERY: {"json", "query", "filter", "extract", "parse"},
	tools.CALC:       {"math", "calculate", "sum", "product", "average"},
}

// TagsFor returns the keywords associated with a tool name, or nil if none
// are declared. Safe to call for unknown names — returns nil.
func TagsFor(name tools.ToolName) []string {
	return toolTags[name]
}

// toolHints maps each tool name to a one-sentence capability phrase used by
// TOOL_SEARCH's ranking. Hints score higher (+4) than description hits (+2)
// because they're curated, so they should describe the tool's discriminating
// capability — what makes it different from neighbors, not what it does in
// general. One sentence per tool, no marketing fluff.
//
// Missing entry = empty hint = ranking falls back to description scoring.
var toolHints = map[tools.ToolName]string{
	// fs
	tools.READ_FILE:  "Read a file from disk; supports PDF pages, Jupyter notebooks, and images.",
	tools.WRITE_FILE: "Overwrite a file's contents or create a new one.",
	tools.EDIT_FILE:  "Apply a precise old_string -> new_string replacement in an existing file.",
	tools.GLOB:       "Match file paths against glob patterns sorted by modification time.",

	// shell
	tools.BASH: "Execute a shell command and return combined stdout/stderr.",
	tools.GREP: "Regex-search file contents recursively across a directory.",
	tools.TREE: "Print a directory tree to a given depth.",

	// meta
	tools.AGENT:           "Spawn a subagent to handle a focused subtask in isolation.",
	tools.TOOL_SEARCH:     "Discover deferred tools by name or keyword.",
	tools.SKILL:           "Invoke a user-installed Markdown skill for task-specific instructions.",
	tools.SCHEDULE_WAKEUP: "Sleep, then re-enter the conversation with a queued prompt.",

	// todo
	tools.TODO_WRITE: "Write the session's todo list (full-list replacement; flip statuses by writing a new list).",

	// monitor / mode / notebook
	tools.MONITOR:         "Stream events from a background task or process.",
	tools.ENTER_PLAN_MODE: "Switch the session into read-only plan mode for design work.",
	tools.EXIT_PLAN_MODE:  "Exit plan mode and return to the previous permission mode.",
	tools.ENTER_WORKTREE:  "Create a git worktree for isolated implementation work.",
	tools.EXIT_WORKTREE:   "Tear down the current worktree and return to the host branch.",
	tools.NOTEBOOK_EDIT:   "Edit cells in a Jupyter notebook by index.",

	// lsp
	tools.LSP_REQUEST: "Query a Language Server for semantic information — go-to-definition, find references, hover, and document symbols.",

	// cron / remote
	tools.CRON_CREATE:    "Schedule a recurring remote agent run on a cron expression.",
	tools.CRON_LIST:      "List the user's scheduled remote agent routines.",
	tools.CRON_DELETE:    "Remove a scheduled remote agent routine.",
	tools.REMOTE_TRIGGER: "Trigger a remote agent run via webhook.",

	// web
	tools.WEB_FETCH:  "Fetch and extract readable text from a URL.",
	tools.WEB_SEARCH: "Search the public web via Tavily for up-to-date information.",

	// ux
	tools.ASK_USER_QUESTION: "Ask the user a multiple-choice or free-text question during execution.",
	tools.PUSH_NOTIFICATION: "Send a system notification when the user is away from the terminal.",

	// util
	tools.JSON_QUERY: "Extract a value from JSON using a dot/bracket path expression.",
	tools.CALC:       "Evaluate a mathematical expression with full operator support.",

	// dev
	tools.FEEDBACK: "Report a bug, suggest an improvement, or request a new tool from evva developers.",
}

// HintFor returns the curated search hint for a tool name, or "" if none is
// declared. Safe to call for unknown names — returns "".
func HintFor(name tools.ToolName) string {
	return toolHints[name]
}
