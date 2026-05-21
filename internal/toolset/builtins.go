package toolset

import (
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/cron"
	"github.com/johnny1110/evva/internal/tools/dev"
	"github.com/johnny1110/evva/internal/tools/fs"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/internal/tools/monitor"
	"github.com/johnny1110/evva/internal/tools/notebook"
	"github.com/johnny1110/evva/internal/tools/shell"
	"github.com/johnny1110/evva/internal/tools/skill"
	"github.com/johnny1110/evva/internal/tools/todo"
	"github.com/johnny1110/evva/internal/tools/util"
	"github.com/johnny1110/evva/internal/tools/ux"
	"github.com/johnny1110/evva/internal/tools/web"
)

// registerBuiltins populates a Registry with every tool evva ships out of
// the box. Called exactly once by DefaultRegistry on first access.
//
// Adding a new built-in tool: register it here. Adding a third-party tool:
// the host (cmd/evva derivative) calls DefaultRegistry().Register(...) at
// startup before agent.Main constructs the root agent.
func registerBuiltins(r *Registry) {
	// --- fs (stateful — share ReadTracker + workdir via ToolState) ---
	r.MustRegister(tools.READ_FILE, func(s *ToolState) (tools.Tool, error) { return fs.NewRead(s.ReadTracker(), s.workDir()), nil })
	r.MustRegister(tools.WRITE_FILE, func(s *ToolState) (tools.Tool, error) { return fs.NewWrite(s.ReadTracker(), s.workDir()), nil })
	r.MustRegister(tools.EDIT_FILE, func(s *ToolState) (tools.Tool, error) { return fs.NewEdit(s.ReadTracker(), s.workDir()), nil })
	r.MustRegister(tools.GLOB, func(s *ToolState) (tools.Tool, error) { return fs.NewGlob(s.workDir()), nil })

	// --- shell (stateless) ---
	r.MustRegister(tools.BASH, func(s *ToolState) (tools.Tool, error) { return shell.Bash, nil })
	r.MustRegister(tools.GREP, func(s *ToolState) (tools.Tool, error) { return shell.Grep, nil })
	r.MustRegister(tools.TREE, func(s *ToolState) (tools.Tool, error) { return shell.Tree, nil })

	// --- meta ---
	// Lookups are late-bound: the agent installs itself via SetSubagentSpawner /
	// SetDeferredLookup / SetSkillRegistry after construction, and the tools
	// read through those accessors at Execute time.
	r.MustRegister(tools.AGENT, func(s *ToolState) (tools.Tool, error) { return meta.NewAgent(s.SubagentSpawner, s.AgentGroup()), nil })
	r.MustRegister(tools.TOOL_SEARCH, func(s *ToolState) (tools.Tool, error) { return meta.NewToolSearch(s.DeferredLookup), nil })
	r.MustRegister(tools.SKILL, func(s *ToolState) (tools.Tool, error) { return skill.NewSkill(s.SkillRegistry), nil })
	r.MustRegister(tools.SCHEDULE_WAKEUP, func(s *ToolState) (tools.Tool, error) { return meta.NewWakeup(s.WakeupQueue()), nil })

	// --- todo (stateful — backed by one *TodoStore via ToolState) ---
	r.MustRegister(tools.TODO_WRITE, func(s *ToolState) (tools.Tool, error) { return todo.NewWrite(s.TodoStore()), nil })

	// --- monitor / mode / notebook ---
	r.MustRegister(tools.MONITOR, func(s *ToolState) (tools.Tool, error) { return monitor.Monitor, nil })
	r.MustRegister(tools.ENTER_PLAN_MODE, func(s *ToolState) (tools.Tool, error) {
		return mode.NewEnterPlanMode(s.PlanController), nil
	})
	r.MustRegister(tools.EXIT_PLAN_MODE, func(s *ToolState) (tools.Tool, error) {
		return mode.NewExitPlanMode(s.PlanController), nil
	})
	r.MustRegister(tools.ENTER_WORKTREE, func(s *ToolState) (tools.Tool, error) { return mode.EnterWorktree, nil })
	r.MustRegister(tools.EXIT_WORKTREE, func(s *ToolState) (tools.Tool, error) { return mode.ExitWorktree, nil })
	r.MustRegister(tools.NOTEBOOK_EDIT, func(s *ToolState) (tools.Tool, error) { return notebook.Edit, nil })

	// --- cron (stateless stubs) ---
	r.MustRegister(tools.CRON_CREATE, func(s *ToolState) (tools.Tool, error) { return cron.Create, nil })
	r.MustRegister(tools.CRON_LIST, func(s *ToolState) (tools.Tool, error) { return cron.List, nil })
	r.MustRegister(tools.CRON_DELETE, func(s *ToolState) (tools.Tool, error) { return cron.Delete, nil })
	r.MustRegister(tools.REMOTE_TRIGGER, func(s *ToolState) (tools.Tool, error) { return cron.Trigger, nil })

	// --- web (cfg-bound — read TavilyAPIKey / FetchMaxBytes through ToolState) ---
	r.MustRegister(tools.WEB_FETCH, func(s *ToolState) (tools.Tool, error) { return web.NewFetch(s.Config()), nil })
	r.MustRegister(tools.WEB_SEARCH, func(s *ToolState) (tools.Tool, error) { return web.NewSearch(s.Config()), nil })

	// --- ux ---
	// AskUserQuestion is late-bound: the question broker is installed on ToolState
	// after construction via agent.WithQuestionBroker; the tool reads it at Execute time.
	r.MustRegister(tools.ASK_USER_QUESTION, func(s *ToolState) (tools.Tool, error) { return ux.NewAskQuestion(s.QuestionBroker), nil })
	r.MustRegister(tools.PUSH_NOTIFICATION, func(s *ToolState) (tools.Tool, error) { return ux.Notify, nil })

	// --- util (stateless) ---
	r.MustRegister(tools.JSON_QUERY, func(s *ToolState) (tools.Tool, error) { return util.JSONQuery, nil })
	r.MustRegister(tools.CALC, func(s *ToolState) (tools.Tool, error) { return util.Calc, nil })

	// --- dev (evva developer tools, gated by config.IsDevelopment) ---
	r.MustRegister(tools.FEEDBACK, func(s *ToolState) (tools.Tool, error) { return dev.NewFeedback(s.Config()), nil })
}
