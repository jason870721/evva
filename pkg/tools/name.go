package tools

// ToolName is the canonical identifier the LLM sees for each tool.
// Values are the strings sent to the model in tool definitions; the SCREAMING_SNAKE
// constant names are how Go code refers to them.
type ToolName string

// Active tools — always available, no schema lookup required. ===============
const (
	// READ_FILE — read a file by absolute path. Handles text, PDFs (with pages),
	// Jupyter notebooks, images. First-line defense for "what's in this file."
	READ_FILE ToolName = "read"

	// WRITE_FILE — create a new file or fully overwrite one. Use only when Edit
	// doesn't fit.
	WRITE_FILE ToolName = "write"

	// EDIT_FILE — exact string replacement in a file. Requires prior Read.
	// Preferred over Write for modifying existing files.
	EDIT_FILE ToolName = "edit"

	// BASH — run shell commands. Catch-all for git, build/test runs, find/grep/rg,
	// any CLI. Supports background execution.
	BASH ToolName = "bash"

	// AGENT — spawn a subagent (Explore, Plan, general-purpose, code-review, etc.)
	// for parallel/independent work or to protect main context from big result dumps.
	AGENT ToolName = "agent"

	// TOOL_SEARCH — load schemas for deferred tools by name (select:Foo,Bar) or
	// keyword search. Required before calling anything deferred.
	TOOL_SEARCH ToolName = "tool_search"

	// SKILL — invoke a user-installed skill by name (e.g. commit, code-review,
	// make-prd, pgagent). Same as the user typing /skill-name.
	SKILL ToolName = "skill"

	// SCHEDULE_WAKEUP — self-pace iterations in /loop dynamic mode.
	// Not relevant outside loops.
	SCHEDULE_WAKEUP ToolName = "schedule_wakeup"

	// TODO_WRITE — write the session's todo list (full-list replacement).
	// Used proactively at the start of any non-trivial task; subsequent calls
	// flip statuses and add/remove entries. One in_progress entry at a time.
	TODO_WRITE ToolName = "todo_write"

	// UPDATE_USER_PROFILE — merge sections into <APP_HOME>/USER_PROFILE.md.
	// Persistent across sessions; auto-memory feature. Section keys are fixed:
	// "Preferences", "Working style", "Recurring topics".
	UPDATE_USER_PROFILE ToolName = "update_user_profile"

	// UPDATE_PROJECT_MEMORY — merge sections into
	// <APP_HOME>/projects/<slug>/MEMORY.md keyed by slugified workdir path.
	// Section keys are fixed: "Project facts", "Decisions", "Open issues",
	// "References".
	UPDATE_PROJECT_MEMORY ToolName = "update_project_memory"
)

// Deferred tools — name-only until loaded with TOOL_SEARCH. ================
// Grouped by purpose to match docs/claude-tool/claude-code-tool-summary.md.

// Process management. Background-task tools land in Phase 16:
//   - Bash run_in_background spawns a detached process that delivers its
//     result back to the agent loop asynchronously.
//   - MONITOR streams stdout lines from a long-running command as events.
//   - TASK_LIST / TASK_OUTPUT / TASK_STOP let the model introspect/control
//     background tasks between fire-and-notification.
const (
	MONITOR ToolName = "monitor"

	// TASK_LIST — enumerate every background task with status + metadata.
	TASK_LIST ToolName = "task_list"
	// TASK_OUTPUT — fetch the captured stdout/stderr of a running or
	// completed background task. Optional tail limits to last N lines.
	TASK_OUTPUT ToolName = "task_output"
	// TASK_STOP — kill a running background task. Idempotent for tasks
	// that have already finished.
	TASK_STOP ToolName = "task_stop"

	ENTER_PLAN_MODE ToolName = "enter_plan_mode"
	EXIT_PLAN_MODE  ToolName = "exit_plan_mode"
	ENTER_WORKTREE  ToolName = "enter_worktree"
	EXIT_WORKTREE   ToolName = "exit_worktree"

	NOTEBOOK_EDIT ToolName = "notebook_edit"
)

// User interaction.
const (
	ASK_USER_QUESTION ToolName = "ask_user_question"
	PUSH_NOTIFICATION ToolName = "push_notification"
)

// Scheduling.
const (
	CRON_CREATE    ToolName = "cron_create"
	CRON_LIST      ToolName = "cron_list"
	CRON_DELETE    ToolName = "cron_delete"
	REMOTE_TRIGGER ToolName = "remote_trigger"
)

// Web.
const (
	WEB_FETCH  ToolName = "web_fetch"
	WEB_SEARCH ToolName = "web_search"
)

// Others.
const (
	// this is for explore agent (read only)
	GREP ToolName = "grep"
	TREE ToolName = "tree"
	GLOB ToolName = "glob"

	// Others — deferred.
	JSON_QUERY ToolName = "json_query"
	CALC       ToolName = "calc"

	// this is for dev phase
	FEEDBACK ToolName = "feedback"
)
