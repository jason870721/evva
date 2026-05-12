// Package mode hosts agent-mode and isolation tools:
// EnterPlanMode / ExitPlanMode (read-only planning) and
// EnterWorktree / ExitWorktree (filesystem-isolated worktrees).
//
// Singletons for stubbing. When implemented these will likely transition to
// constructor-injected so they can mutate the owning agent's mode state.
package mode

import "github.com/johnny1110/evva/internal/tools"

func init() {
	tools.Register(tools.ENTER_PLAN_MODE, EnterPlan)
	tools.Register(tools.EXIT_PLAN_MODE, ExitPlan)
	tools.Register(tools.ENTER_WORKTREE, EnterWorktree)
	tools.Register(tools.EXIT_WORKTREE, ExitWorktree)
}

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{
		tools.ENTER_PLAN_MODE, tools.EXIT_PLAN_MODE,
		tools.ENTER_WORKTREE, tools.EXIT_WORKTREE,
	}
}

var (
	EnterPlan tools.Tool = tools.NewStub(
		tools.ENTER_PLAN_MODE,
		"Transition into plan mode to explore the codebase and design an implementation approach before writing code. "+
			"Use proactively for non-trivial implementation tasks (new features, multiple valid approaches, "+
			"multi-file changes, unclear requirements). Skip for typo fixes, single-function additions, or pure research.",
		`{
			"type":"object",
			"additionalProperties":false,
			"properties":{}
		}`,
	)

	ExitPlan tools.Tool = tools.NewStub(
		tools.EXIT_PLAN_MODE,
		"Signal that the plan is complete and ready for user approval. "+
			"The plan content is read from the plan file (not passed as a parameter). "+
			"Use only for implementation planning, not for research tasks. "+
			"Do NOT use AskUserQuestion to ask \"is this plan okay?\" — that's exactly what this tool does.",
		`{
			"type":"object",
			"additionalProperties":{},
			"properties":{
				"allowedPrompts":{
					"type":"array",
					"description":"Prompt-based permissions needed to implement the plan.",
					"items":{
						"type":"object",
						"additionalProperties":false,
						"required":["tool","prompt"],
						"properties":{
							"tool":{"type":"string","enum":["Bash"],"description":"The tool this prompt applies to"},
							"prompt":{"type":"string","description":"Semantic description of the action, e.g. \"run tests\", \"install dependencies\""}
						}
					}
				}
			}
		}`,
	)

	EnterWorktree tools.Tool = tools.NewStub(
		tools.ENTER_WORKTREE,
		"Create an isolated git worktree and switch the session into it. "+
			"Use ONLY when the user explicitly says \"worktree\" or CLAUDE.md/memory instructs it. "+
			"Do not use for ordinary branch work. "+
			"Pass `path` to enter an existing worktree instead of creating one.",
		`{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"name":{"type":"string","description":"Optional name for a new worktree. Each \"/\"-separated segment may contain only letters, digits, dots, underscores, and dashes; max 64 chars total. Mutually exclusive with path."},
				"path":{"type":"string","description":"Path to an existing worktree of the current repository to switch into. Must appear in git worktree list. Mutually exclusive with name."}
			}
		}`,
	)

	ExitWorktree tools.Tool = tools.NewStub(
		tools.EXIT_WORKTREE,
		"Exit a worktree session created by EnterWorktree and return to the original working directory. "+
			"No-op if no worktree session is active. "+
			"Only operates on worktrees created by EnterWorktree in this session — never touches manually-created worktrees.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["action"],
			"properties":{
				"action":{"type":"string","enum":["keep","remove"],"description":"\"keep\" leaves the worktree and branch on disk; \"remove\" deletes both."},
				"discard_changes":{"type":"boolean","description":"Required true when action is \"remove\" and the worktree has uncommitted files or unmerged commits."}
			}
		}`,
	)
)
