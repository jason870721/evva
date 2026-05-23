// Package mode hosts agent-mode and isolation tools:
// EnterPlanMode / ExitPlanMode (read-only planning) and
// EnterWorktree / ExitWorktree (filesystem-isolated worktrees).
//
// Each pair is wired through a narrow controller supplied by the agent:
// PlanModeController mutates the owning agent's permission mode and reads
// the plan-file path off its workdir; WorktreeController switches the
// agent's workdir into / out of a git worktree and rebuilds workdir-bound
// active tools. See worktree.go / worktree_controller.go.
package mode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/internal/permission"
	"github.com/johnny1110/evva/pkg/tools"
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{
		tools.ENTER_PLAN_MODE, tools.EXIT_PLAN_MODE,
		tools.ENTER_WORKTREE, tools.EXIT_WORKTREE,
	}
}

// PlanFilePath returns the absolute path of the plan file for the given
// planName. When planName is empty it defaults to "current" (backward
// compatible — the legacy single-plan-per-session filename).
// Exposed so tests and the user-guide doc can reference the canonical
// location.
func PlanFilePath(workdir, planName string) string {
	if planName == "" {
		planName = "current"
	}
	return filepath.Join(workdir, filepath.FromSlash(permission.PlanDirSegment), planName+".md")
}

// --- EnterPlanMode -----------------------------------------------------

const enterPlanModeDescription = `Use this tool proactively when you're about to start a non-trivial implementation task. Getting user sign-off on your approach before writing code prevents wasted effort and ensures alignment. This tool transitions you into plan mode where you can explore the codebase and design an implementation approach for user approval.

## When to Use This Tool

**Prefer using EnterPlanMode** for implementation tasks unless they're simple. Use it when ANY of these conditions apply:

1. **New Feature Implementation**: Adding meaningful new functionality
   - Example: "Add a logout button" - where should it go? What should happen on click?
   - Example: "Add form validation" - what rules? What error messages?

2. **Multiple Valid Approaches**: The task can be solved in several different ways
   - Example: "Add caching to the API" - could use Redis, in-memory, file-based, etc.
   - Example: "Improve performance" - many optimization strategies possible

3. **Code Modifications**: Changes that affect existing behavior or structure
   - Example: "Update the login flow" - what exactly should change?
   - Example: "Refactor this component" - what's the target architecture?

4. **Architectural Decisions**: The task requires choosing between patterns or technologies
   - Example: "Add real-time updates" - WebSockets vs SSE vs polling
   - Example: "Implement state management" - Redux vs Context vs custom solution

5. **Multi-File Changes**: The task will likely touch more than 2-3 files
   - Example: "Refactor the authentication system"
   - Example: "Add a new API endpoint with tests"

6. **Unclear Requirements**: You need to explore before understanding the full scope
   - Example: "Make the app faster" - need to profile and identify bottlenecks
   - Example: "Fix the bug in checkout" - need to investigate root cause

7. **User Preferences Matter**: The implementation could reasonably go multiple ways
   - If you would use ask_user_question to clarify the approach, use EnterPlanMode instead
   - Plan mode lets you explore first, then present options with context

## When NOT to Use This Tool

Only skip EnterPlanMode for simple tasks:
- Single-line or few-line fixes (typos, obvious bugs, small tweaks)
- Adding a single function with clear requirements
- Tasks where the user has given very specific, detailed instructions
- Pure research/exploration tasks (use the Agent tool with explore agent instead)

## What Happens in Plan Mode

In plan mode, you'll:
1. Thoroughly explore the codebase using Glob, Grep, and Read tools
2. Understand existing patterns and architecture
3. Design an implementation approach
4. Write the plan to the plan file specified in the tool result
5. Use ask_user_question if you need to clarify approaches
6. Exit plan mode with ExitPlanMode when ready to implement

## Examples

### GOOD - Use EnterPlanMode:
User: "Add user authentication to the app"
- Requires architectural decisions (session vs JWT, where to store tokens, middleware structure)

User: "Optimize the database queries"
- Multiple approaches possible, need to profile first, significant impact

User: "Implement dark mode"
- Architectural decision on theme system, affects many components

User: "Add a delete button to the user profile"
- Seems simple but involves: where to place it, confirmation dialog, API call, error handling, state updates

User: "Update the error handling in the API"
- Affects multiple files, user should approve the approach

### BAD - Don't use EnterPlanMode:
User: "Fix the typo in the README"
- Straightforward, no planning needed

User: "Add a console.log to debug this function"
- Simple, obvious implementation

User: "What files handle routing?"
- Research task, not implementation planning

## Important Notes

- If unsure whether to use it, err on the side of planning - it's better to get alignment upfront than to redo work
- Users appreciate being consulted before significant changes are made to their codebase`

const enterPlanModeSchema = `{
	"type":"object",
	"additionalProperties":false,
	"properties":{
		"plan_name":{
			"type":"string",
			"description":"Optional name for the plan file. Use lowercase letters, digits, and hyphens only (e.g. \"auth-refactor\", \"add-dark-mode\"). If omitted, defaults to \"current\"."
		}
	}
}`

// EnterPlanModeTool flips the session into plan mode, stashes the prior
// mode for restore, and prepares an empty plan file the model writes to.
type EnterPlanModeTool struct {
	lookup ControllerLookup
}

// NewEnterPlanMode constructs the tool with a late-bound controller
// lookup. The lookup is invoked once per Execute call — passing a
// closure (rather than the controller directly) lets toolset.Build resolve
// the agent after agent.New returns.
func NewEnterPlanMode(lookup ControllerLookup) *EnterPlanModeTool {
	return &EnterPlanModeTool{lookup: lookup}
}

func (t *EnterPlanModeTool) Name() string            { return string(tools.ENTER_PLAN_MODE) }
func (t *EnterPlanModeTool) Description() string     { return enterPlanModeDescription }
func (t *EnterPlanModeTool) Schema() json.RawMessage { return json.RawMessage(enterPlanModeSchema) }

func (t *EnterPlanModeTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	ctrl := resolveController(t.lookup)
	if ctrl == nil {
		return tools.Result{
			IsError: true,
			Content: "enter_plan_mode: no plan-mode controller installed",
		}, nil
	}

	// Parse optional plan_name from input; default to "current".
	var parsed struct {
		PlanName string `json:"plan_name"`
	}
	if len(input) > 0 {
		_ = json.Unmarshal(input, &parsed)
	}
	planName := parsed.PlanName
	if planName == "" {
		planName = "current"
	}
	ctrl.SetPlanName(planName)

	planPath := PlanFilePath(ctrl.Workdir(), planName)
	if ctrl.PermissionMode() == permission.ModePlan {
		return tools.Result{
			Content: "Already in plan mode. The plan file is at " + planPath +
				". Continue exploring and writing your plan; call exit_plan_mode when ready.",
		}, nil
	}

	// Side effects (stash prePlanMode, reset attachment counters, emit
	// KindModeChanged) live on the transition hub now — calling
	// SetPermissionMode is enough.
	prev := ctrl.PermissionMode()
	ctrl.SetPermissionMode(permission.ModePlan)

	// Ensure the plan directory exists. Don't truncate an existing plan
	// file — re-entering plan mode after a rejection should pick up where
	// the model left off, not nuke its in-progress draft. Only create an
	// empty file when none exists.
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		logger.Warn("enter_plan_mode: mkdir failed", "err", err, "path", planPath)
		return tools.Result{
			IsError: true,
			Content: "enter_plan_mode: cannot create plan directory: " + err.Error(),
		}, nil
	}
	if _, err := os.Stat(planPath); errors.Is(err, os.ErrNotExist) {
		if werr := os.WriteFile(planPath, []byte{}, 0o644); werr != nil {
			logger.Warn("enter_plan_mode: create failed", "err", werr, "path", planPath)
			return tools.Result{
				IsError: true,
				Content: "enter_plan_mode: cannot prepare plan file: " + werr.Error(),
			}, nil
		}
	}

	logger.Info("enter_plan_mode", "prev_mode", string(prev), "plan_file", planPath, "plan_name", planName)
	return tools.Result{
		Content: "You are now in plan mode. Plan file: " + planPath +
			". The plan-mode workflow will be injected on the next user prompt.",
	}, nil
}

// --- ExitPlanMode -----------------------------------------------------

const exitPlanModeDescription = `Use this tool when you are in plan mode and have finished writing your plan to the plan file and are ready for user approval.

## How This Tool Works
- You should have already written your plan to the plan file specified in the plan mode system message
- This tool does NOT take the plan content as a parameter - it will read the plan from the file you wrote
- This tool simply signals that you're done planning and ready for the user to review and approve
- The user will see the contents of your plan file when they review it

## When to Use This Tool
IMPORTANT: Only use this tool when the task requires planning the implementation steps of a task that requires writing code. For research tasks where you're gathering information, searching files, reading files or in general trying to understand the codebase - do NOT use this tool.

## Before Using This Tool
Ensure your plan is complete and unambiguous:
- If you have unresolved questions about requirements or approach, use ask_user_question first (in earlier phases)
- Once your plan is finalized, use THIS tool to request approval

**Important:** Do NOT use ask_user_question to ask "Is this plan okay?" or "Should I proceed?" - that's exactly what THIS tool does. ExitPlanMode inherently requests user approval of your plan.

## Examples

1. Initial task: "Search for and understand the implementation of vim mode in the codebase" - Do not use the exit plan mode tool because you are not planning the implementation steps of a task.
2. Initial task: "Help me implement yank mode for vim" - Use the exit plan mode tool after you have finished planning the implementation steps of the task.
3. Initial task: "Add a new feature to handle user authentication" - If unsure about auth method (OAuth, JWT, etc.), use ask_user_question first, then use exit plan mode tool after clarifying the approach.`

const exitPlanModeSchema = `{
	"type":"object",
	"additionalProperties":false,
	"properties":{
		"allowedPrompts":{
			"type":"array",
			"description":"Prompt-based permissions needed to implement the plan. These describe categories of actions rather than specific commands.",
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
}`

type exitPlanModeInput struct {
	AllowedPrompts []struct {
		Tool   string `json:"tool"`
		Prompt string `json:"prompt"`
	} `json:"allowedPrompts"`
}

// ExitPlanModeTool reads the plan file, asks the user to approve, and
// restores the pre-plan permission mode on approval. Rejection leaves
// the session in ModePlan with the user's reason surfaced to the model.
type ExitPlanModeTool struct {
	lookup ControllerLookup
}

func NewExitPlanMode(lookup ControllerLookup) *ExitPlanModeTool {
	return &ExitPlanModeTool{lookup: lookup}
}

func (t *ExitPlanModeTool) Name() string            { return string(tools.EXIT_PLAN_MODE) }
func (t *ExitPlanModeTool) Description() string     { return exitPlanModeDescription }
func (t *ExitPlanModeTool) Schema() json.RawMessage { return json.RawMessage(exitPlanModeSchema) }

func (t *ExitPlanModeTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	ctrl := resolveController(t.lookup)
	if ctrl == nil {
		return tools.Result{
			IsError: true,
			Content: "exit_plan_mode: no plan-mode controller installed",
		}, nil
	}
	if ctrl.PermissionMode() != permission.ModePlan {
		return tools.Result{
			IsError: true,
			Content: "exit_plan_mode: not in plan mode (call enter_plan_mode first, or Shift+Tab cycled out)",
		}, nil
	}

	workdir := ctrl.Workdir()
	planPath := PlanFilePath(workdir, ctrl.PlanName())
	body, err := os.ReadFile(planPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return tools.Result{
			IsError: true,
			Content: "exit_plan_mode: no plan file found at " + planPath +
				". Create it with the `write` tool (plan mode allows writes to this path only) before calling exit_plan_mode again.",
		}, nil
	case err != nil:
		return tools.Result{
			IsError: true,
			Content: "exit_plan_mode: cannot read plan file at " + planPath + ": " + err.Error(),
		}, nil
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return tools.Result{
			IsError: true,
			Content: "exit_plan_mode: plan file at " + planPath + " exists but is empty. Write your plan body to it (use the `write` tool) before calling exit_plan_mode again.",
		}, nil
	}

	// allowedPrompts is best-effort metadata for the UI/log only. v1
	// doesn't auto-promote them to permission rules.
	var parsed exitPlanModeInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &parsed)
	}

	// Compute the restore target once, before the broker round-trip.
	// The transition hub clears prePlanMode on exit, so the value
	// captured here is the source of truth for either branch (approved
	// or no-broker fallback).
	restore := ctrl.PrePlanMode()
	if restore == "" || restore == permission.ModePlan {
		restore = permission.ModeDefault
	}

	broker := ctrl.Broker()
	if broker == nil {
		// No broker installed (headless tests or runs without TUI). Skip
		// the prompt and restore the prior mode optimistically. Bootstrap
		// always installs a broker in production.
		ctrl.SetPermissionMode(restore)
		logger.Info("exit_plan_mode: no broker — auto-restored", "mode", string(restore))
		return tools.Result{
			Content: "Plan auto-accepted (no approval broker). Restored permission mode to " + string(restore) + ".",
		}, nil
	}

	req := permission.ApprovalRequest{
		AgentID:     ctrl.AgentID(),
		ToolName:    string(tools.EXIT_PLAN_MODE),
		ToolInput:   input,
		Mode:        permission.ModePlan,
		Reason:      "Plan approval — review and approve to exit plan mode",
		PlanContent: trimmed,
	}
	dec, err := broker.Request(ctx, req)
	if err != nil {
		logger.Warn("exit_plan_mode: broker cancelled", "err", err)
	}

	if dec.Behavior == permission.BehaviorAllow {
		ctrl.SetPermissionMode(restore)
		logger.Info("exit_plan_mode: approved", "restored_mode", string(restore))
		return tools.Result{
			Content: "Plan approved. Restored permission mode to " + string(restore) + ". Proceed with implementation.",
		}, nil
	}

	reason := strings.TrimSpace(dec.Reason)
	if reason == "" {
		reason = "user denied"
	}
	logger.Info("exit_plan_mode: rejected", "reason", reason)
	return tools.Result{
		// Not IsError — let the loop continue so the model can iterate.
		Content: fmt.Sprintf(
			"User requested changes: %s.\n\nIterate on the plan (edit %s) and call exit_plan_mode again when ready.",
			reason, planPath,
		),
	}, nil
}

func resolveController(lookup ControllerLookup) PlanModeController {
	if lookup == nil {
		return nil
	}
	return lookup()
}

