// Package meta hosts agent-meta tools: Agent (spawn sub-agent), ToolSearch
// (load deferred-tool schemas), Skill (invoke a user-installed skill), and
// ScheduleWakeup (self-pace /loop iterations).
//
// All singletons for stubbing. When implemented these will require deeper
// agent-side hooks (sub-agent runner, schema registry, skill loader,
// scheduler), supplied via constructor injection.
package meta

import "github.com/johnny1110/evva/internal/tools"

func init() {
	tools.Register(tools.AGENT, Agent)
	tools.Register(tools.TOOL_SEARCH, ToolSearch)
	tools.Register(tools.SKILL, Skill)
	tools.Register(tools.SCHEDULE_WAKEUP, Wakeup)
}

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.AGENT, tools.TOOL_SEARCH, tools.SKILL, tools.SCHEDULE_WAKEUP}
}

var (
	Agent tools.Tool = tools.NewStub(
		tools.AGENT,
		"Launch a new agent to handle complex, multi-step tasks. "+
			"Each agent type has specific capabilities and tools available to it. "+
			"When using the Agent tool, specify a subagent_type parameter to select which agent type to use. "+
			"If omitted, the general-purpose agent is used.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["description","prompt"],
			"properties":{
				"description":{"type":"string","description":"A short (3-5 word) description of the task"},
				"prompt":{"type":"string","description":"The task for the agent to perform"},
				"subagent_type":{"type":"string","description":"The type of specialized agent to use for this task"},
				"model":{"type":"string","enum":["sonnet","opus","haiku"],"description":"Optional model override. Takes precedence over the agent definition's model frontmatter."},
				"isolation":{"type":"string","enum":["worktree"],"description":"Isolation mode. \"worktree\" creates a temporary git worktree so the agent works on an isolated copy of the repo."},
				"run_in_background":{"type":"boolean","description":"Set to true to run this agent in the background. You will be notified when it completes."}
			}
		}`,
	)

	ToolSearch tools.Tool = tools.NewStub(
		tools.TOOL_SEARCH,
		"Fetches full schema definitions for deferred tools so they can be called.\n\n"+
			"Deferred tools appear by name in <system-reminder> messages. Until fetched, only the name is known — "+
			"there is no parameter schema, so the tool cannot be invoked. "+
			"This tool takes a query, matches it against the deferred tool list, "+
			"and returns the matched tools' complete JSONSchema definitions inside a <functions> block.\n\n"+
			"Query forms:\n"+
			"- \"select:Read,Edit,Grep\" — fetch these exact tools by name\n"+
			"- \"notebook jupyter\" — keyword search, up to max_results best matches\n"+
			"- \"+slack send\" — require \"slack\" in the name, rank by remaining terms",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["query","max_results"],
			"properties":{
				"query":{"type":"string","description":"Query to find deferred tools. Use \"select:<tool_name>\" for direct selection, or keywords to search."},
				"max_results":{"type":"number","default":5,"description":"Maximum number of results to return (default: 5)"}
			}
		}`,
	)

	Skill tools.Tool = tools.NewStub(
		tools.SKILL,
		"Execute a skill within the main conversation.\n\n"+
			"When users reference a \"slash command\" or \"/<something>\", they are referring to a skill. "+
			"Use this tool to invoke it.\n\n"+
			"- Set `skill` to the exact name from the available-skills list (no leading slash). "+
			"Plugin-namespaced skills use `plugin:skill`.\n"+
			"- Set `args` to pass optional arguments.\n"+
			"- Only invoke a skill that appears in the available-skills list or that the user explicitly typed as /<name>.\n"+
			"- Do not invoke a skill that is already running.\n"+
			"- Do not use this tool for built-in CLI commands like /help or /clear.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["skill"],
			"properties":{
				"skill":{"type":"string","description":"The name of a skill from the available-skills list. Do not guess names."},
				"args":{"type":"string","description":"Optional arguments for the skill"}
			}
		}`,
	)

	Wakeup tools.Tool = tools.NewStub(
		tools.SCHEDULE_WAKEUP,
		"Schedule when to resume work in /loop dynamic mode — the user invoked /loop without an interval, "+
			"asking you to self-pace iterations of a specific task.\n\n"+
			"Pass the same /loop prompt back via `prompt` each turn so the next firing repeats the task. "+
			"For an autonomous /loop (no user prompt), pass the literal sentinel `<<autonomous-loop-dynamic>>` as `prompt` instead. "+
			"Omit the call to end the loop.\n\n"+
			"Picking delaySeconds: Anthropic prompt cache has a 5-minute TTL — sleeping past 300s pays a cache miss. "+
			"Stay <270s for active work (cache warm), commit to 1200s+ when waiting longer is fine. "+
			"Don't pick exactly 300s.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["delaySeconds","reason","prompt"],
			"properties":{
				"delaySeconds":{"type":"number","description":"Seconds from now to wake up. Clamped to [60, 3600] by the runtime."},
				"prompt":{"type":"string","description":"The /loop input to fire on wake-up. Pass the same input verbatim each turn, or use the <<autonomous-loop-dynamic>> sentinel for autonomous loops."},
				"reason":{"type":"string","description":"One short sentence explaining the chosen delay. Shown back to the user."}
			}
		}`,
	)
)
