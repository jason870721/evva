package toolset

import (
	configtool "github.com/johnny1110/evva/internal/tools/config"
	"github.com/johnny1110/evva/internal/tools/dev"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/internal/tools/ux"
	"github.com/johnny1110/evva/pkg/mcp"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/alarm"
	"github.com/johnny1110/evva/pkg/tools/cron"
	"github.com/johnny1110/evva/pkg/tools/excel"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/tools/lsp"
	"github.com/johnny1110/evva/pkg/tools/monitor"
	"github.com/johnny1110/evva/pkg/tools/notebook"
	"github.com/johnny1110/evva/pkg/tools/repl"
	"github.com/johnny1110/evva/pkg/tools/shell"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/tools/util"
	"github.com/johnny1110/evva/pkg/tools/web"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// init wires evva's bundled tools into pkg/toolset.DefaultRegistry().
// Importing internal/toolset (which the agent does transitively) is
// enough to populate the registry — no explicit RegisterBuiltins call
// needed from cmd/evva.
//
// Downstream apps that import pkg/agent get this registration for free
// because internal/agent imports internal/toolset.
//
// Adding a new built-in tool: register it here. Adding a third-party
// tool: the host calls pkg/toolset.DefaultRegistry().Register(...) at
// startup before the first agent is constructed.
func init() {
	r := pubtoolset.DefaultRegistry()

	// --- fs (stateful — share ReadTracker + workdir via ToolState) ---
	r.MustRegister(tools.READ_FILE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return fs.NewRead(ts.ReadTracker(), ts.Workdir()), nil
	})
	r.MustRegister(tools.WRITE_FILE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return fs.NewWrite(ts.ReadTracker(), ts.Workdir()), nil
	})
	r.MustRegister(tools.EDIT_FILE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return fs.NewEdit(ts.ReadTracker(), ts.Workdir()), nil
	})
	r.MustRegister(tools.GLOB, func(s tools.State) (tools.Tool, error) {
		return fs.NewGlob(s.Workdir()), nil
	})

	// --- shell ---
	// Bash captures workdir at construction so each agent (including a
	// subagent spawned with isolation: "worktree") runs commands in its
	// own directory. Grep and Tree remain stateless singletons because
	// they accept an absolute path as a parameter.
	//
	// run_in_background needs the DaemonHost (DaemonState + RootCtx +
	// AgentID) — type-assert here so the production path is wired by
	// default. Tests that build with NewBash directly still get the
	// sync-only behaviour.
	r.MustRegister(tools.BASH, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return shell.NewBashWithHost(ts.Workdir(), ts), nil
	})
	r.MustRegister(tools.GREP, func(tools.State) (tools.Tool, error) { return shell.Grep, nil })
	r.MustRegister(tools.TREE, func(tools.State) (tools.Tool, error) { return shell.Tree, nil })

	// daemon_* read/control surface for every background unit (bash bg,
	// monitor, async subagent). All three resolve through ToolState's
	// DaemonState() — lazy-allocated on first registration. See
	// docs/design/daemon-design.md.
	r.MustRegister(tools.DAEMON_LIST, func(s tools.State) (tools.Tool, error) {
		return daemon.NewList(s.(*ToolState).DaemonState()), nil
	})
	r.MustRegister(tools.DAEMON_OUTPUT, func(s tools.State) (tools.Tool, error) {
		return daemon.NewOutput(s.(*ToolState).DaemonState()), nil
	})
	r.MustRegister(tools.DAEMON_STOP, func(s tools.State) (tools.Tool, error) {
		return daemon.NewStop(s.(*ToolState).DaemonState()), nil
	})

	// --- meta ---
	// Lookups are late-bound: the agent installs itself via SetSubagentSpawner /
	// SetDeferredLookup / SetSkillRegistry after construction, and the tools
	// read through those accessors at Execute time.
	r.MustRegister(tools.AGENT, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return meta.NewAgent(ts.SubagentSpawner), nil
	})
	r.MustRegister(tools.TOOL_SEARCH, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return meta.NewToolSearch(ts.DeferredLookup), nil
	})
	r.MustRegister(tools.SKILL, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return skill.NewSkill(ts.SkillRegistry), nil
	})
	r.MustRegister(tools.SCHEDULE_WAKEUP, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return meta.NewWakeup(ts.WakeupQueue()), nil
	})

	// --- todo (stateful — backed by one *TodoStore via ToolState) ---
	r.MustRegister(tools.TODO_WRITE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return todo.NewWrite(ts.TodoStore()), nil
	})

	// --- monitor / mode / notebook ---
	r.MustRegister(tools.MONITOR, func(s tools.State) (tools.Tool, error) {
		return monitor.NewMonitor(s.(*ToolState)), nil
	})
	r.MustRegister(tools.ENTER_PLAN_MODE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return mode.NewEnterPlanMode(ts.PlanController), nil
	})
	r.MustRegister(tools.EXIT_PLAN_MODE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return mode.NewExitPlanMode(ts.PlanController), nil
	})
	r.MustRegister(tools.ENTER_WORKTREE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return mode.NewEnterWorktree(ts.WorktreeController), nil
	})
	r.MustRegister(tools.EXIT_WORKTREE, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return mode.NewExitWorktree(ts.WorktreeController), nil
	})
	r.MustRegister(tools.NOTEBOOK_EDIT, func(tools.State) (tools.Tool, error) { return notebook.Edit, nil })

	// --- lsp (semantic code intelligence) ---
	r.MustRegister(tools.LSP_REQUEST, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return lsp.NewTool(ts.LSPManager(), ts.Workdir()), nil
	})

	// --- mcp resource meta tools (deferred) ---
	// Per-server tools and per-server authenticate tools are registered
	// dynamically by mcp.Manager.RegisterFactories at agent boot; these two
	// static meta tools work across whichever servers are connected.
	r.MustRegister(tools.LIST_MCP_RESOURCES, func(s tools.State) (tools.Tool, error) {
		return mcp.NewListResourcesTool(s.(*ToolState).McpManager()), nil
	})
	r.MustRegister(tools.READ_MCP_RESOURCE, func(s tools.State) (tools.Tool, error) {
		return mcp.NewReadResourceTool(s.(*ToolState).McpManager()), nil
	})

	// --- cron (stateless stubs) ---
	r.MustRegister(tools.CRON_CREATE, func(tools.State) (tools.Tool, error) { return cron.Create, nil })
	r.MustRegister(tools.CRON_LIST, func(tools.State) (tools.Tool, error) { return cron.List, nil })
	r.MustRegister(tools.CRON_DELETE, func(tools.State) (tools.Tool, error) { return cron.Delete, nil })
	r.MustRegister(tools.REMOTE_TRIGGER, func(tools.State) (tools.Tool, error) { return cron.Trigger, nil })

	// --- alarm (one-shot absolute-time wake; shares one *alarm.Scheduler per
	// agent via ToolState — fires through the WakeupQueue + SignalAlarm) ---
	r.MustRegister(tools.ALARM_CREATE, func(s tools.State) (tools.Tool, error) {
		return alarm.NewCreate(s.(*ToolState).AlarmScheduler()), nil
	})
	r.MustRegister(tools.ALARM_LIST, func(s tools.State) (tools.Tool, error) {
		return alarm.NewList(s.(*ToolState).AlarmScheduler()), nil
	})
	r.MustRegister(tools.ALARM_CANCEL, func(s tools.State) (tools.Tool, error) {
		return alarm.NewCancel(s.(*ToolState).AlarmScheduler()), nil
	})

	// --- web (cfg-bound — read TavilyAPIKey / FetchMaxBytes through State) ---
	r.MustRegister(tools.WEB_FETCH, func(s tools.State) (tools.Tool, error) {
		return web.NewFetch(s.Config()), nil
	})
	r.MustRegister(tools.WEB_SEARCH, func(s tools.State) (tools.Tool, error) {
		return web.NewSearch(s.Config()), nil
	})
	r.MustRegister(tools.HTTP_REQUEST, func(s tools.State) (tools.Tool, error) {
		return web.NewHTTPRequest(s.Config()), nil
	})

	// --- ux ---
	// AskUserQuestion is late-bound: the question broker is installed on
	// ToolState after construction via agent.WithQuestionBroker; the tool
	// reads it at Execute time.
	r.MustRegister(tools.ASK_USER_QUESTION, func(s tools.State) (tools.Tool, error) {
		ts := s.(*ToolState)
		return ux.NewAskQuestion(ts.QuestionBroker), nil
	})
	r.MustRegister(tools.PUSH_NOTIFICATION, func(tools.State) (tools.Tool, error) { return ux.Notify, nil })

	// --- util (stateless) ---
	r.MustRegister(tools.JSON_QUERY, func(tools.State) (tools.Tool, error) { return util.JSONQuery, nil })
	r.MustRegister(tools.CALC, func(tools.State) (tools.Tool, error) { return util.Calc, nil })

	// --- repl (runs a Python/JS snippet in a fresh subprocess) ---
	r.MustRegister(tools.REPL, func(s tools.State) (tools.Tool, error) {
		return repl.NewREPL(s.Workdir()), nil
	})

	// --- dev (evva developer tools, gated by config.IsDevelopment) ---
	r.MustRegister(tools.FEEDBACK, func(s tools.State) (tools.Tool, error) {
		return dev.NewFeedback(s.Config()), nil
	})

	// --- excel (spreadsheet manipulation via excelize) ---
	r.MustRegister(tools.EXCEL, func(s tools.State) (tools.Tool, error) {
		return excel.NewTool(s.Workdir()), nil
	})

	// --- config (let the model read/write evva settings) ---
	r.MustRegister(tools.CONFIG, func(s tools.State) (tools.Tool, error) {
		return configtool.New(s.Config()), nil
	})

	// Auto-memory has no dedicated write tool — the model writes typed memory
	// files itself via write/edit, auto-allowed inside the memory dir by the
	// permission carve-out (pkg/permission + state_machine.go).
}
