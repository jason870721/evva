package sysprompt

// Prompt-side tool name constants. The sysprompt package interpolates these
// into agent prompts so a tool rename in internal/tools/name.go can be made
// in one place and caught here at compile/test time.
//
// Values MUST match the canonical strings in internal/tools/name.go. The
// drift guard lives in toolnames_link_test.go (package sysprompt_test) so
// the production sysprompt build does not import internal/tools — keeps the
// dependency arrow one-way (sysprompt depends on nothing else internal).
//
// Why duplicate the strings instead of importing the tools enum? Because
// the LLM-facing string is what matters; if anyone ever renames a Go
// identifier without changing the wire value the prompt will stay correct.
// If the wire value itself changes, the link test fails at CI.
const (
	nameRead       = "read"
	nameWrite      = "write"
	nameEdit       = "edit"
	nameBash       = "bash"
	nameGrep       = "grep"
	nameTree       = "tree"
	nameGlob       = "glob"
	nameAgent      = "agent"
	nameToolSearch = "tool_search"
	nameSkill      = "skill"
	nameWebSearch  = "web_search"
	nameWebFetch   = "web_fetch"
	nameJSONQuery  = "json_query"
	nameCalc       = "calc"
	nameFeedback   = "feedback"
	nameTodoWrite  = "todo_write"

	// Solo dynamic-workflow board — mounted in place of todo_write when
	// enable_dynamic_workflow is on (see pkg/tools/workflow).
	nameWfTaskCreate  = "wf_task_create"
	nameWfTaskUpdate  = "wf_task_update"
	nameWfTaskVerify  = "wf_task_verify"
	nameWfTaskList    = "wf_task_list"
	nameWfTaskGet     = "wf_task_get"
	nameAskUserQ      = "ask_user_question"
	nameEnterPlanMode = "enter_plan_mode"
	nameExitPlanMode  = "exit_plan_mode"
	nameEnterWorktree = "enter_worktree"
	nameExitWorktree  = "exit_worktree"
	nameWorktreeList  = "worktree_list"

	// Daemon-control surface — see pkg/tools/daemon.
	nameDaemonList   = "daemon_list"
	nameDaemonOutput = "daemon_output"
	nameDaemonStop   = "daemon_stop"
	nameMonitor      = "monitor"

	// LSP — semantic code intelligence.
	nameLspRequest = "lsp_request"

	// REPL — scratch Python/JS code execution.
	nameRepl = "repl"

	// Alarm — one-shot, absolute-time self-wake.
	nameAlarmCreate = "alarm_create"
	nameAlarmList   = "alarm_list"
	nameAlarmCancel = "alarm_cancel"

	// schedule_wakeup — blocking relative sleep (≤1h); contrasted with alarms.
	nameScheduleWakeup = "schedule_wakeup"
)

// Subagent identifiers — the strings the main agent quotes in its
// tools-guide section ("Prefer subagent_type: \"explore\"") and that
// Phase 2's Agent tool will accept as its subagent_type enum. Single-
// sourced here so the main agent's prose and the AgentDefinition.Name
// fields don't drift.
const (
	subagentExplore = "explore"
	subagentGeneral = "general-purpose"
	subagentPlan    = "plan"
)
