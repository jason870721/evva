// Package permission implements evva's tool-permission system.
//
// The model is small: every tool call is gated by Decide() against the active
// Mode and a Store of allow/deny/ask Rules drawn from three Sources (project,
// user, session). Decide() returns either an immediate Allow/Deny or escalates
// to a Broker that prompts the user through the TUI and writes the answer back
// to the blocked goroutine via a reply channel.
//
// The package is pure: no logging, no filesystem (loader.go is the one
// exception, gated behind a separate function), no event sink. Callers wire
// it into the agent loop at state_machine.go and the TUI in the bubbletea v2
// app.
package permission

import (
	"path/filepath"
	"strings"
)

// PlanDirSegment is the workdir-relative directory that EnterPlanMode writes
// plan files into. Decide() carves out write-allow for paths under this
// segment even in ModePlan so the model can compose its plan while everything
// else stays read-only.
const PlanDirSegment = ".evva/plans"

// WorktreeDirSegment is the workdir-relative directory the EnterWorktree tool
// (and AgentTool isolation) materializes git worktrees under. Kept here next
// to PlanDirSegment so the .evva/* family of evva-owned directories stays
// single-sourced.
const WorktreeDirSegment = ".evva/worktrees"

// IsPlanFilePath reports whether absPath sits inside <workdir>/.evva/plans/.
// Both args are resolved with filepath.Abs before comparison so callers can
// pass user-supplied paths without pre-normalising. An empty workdir or a path
// that can't be proven contained returns false (the carve-out should never
// apply when the caller can't prove containment).
func IsPlanFilePath(workdir, absPath string) bool {
	if workdir == "" {
		return false
	}
	wd, err := filepath.Abs(workdir)
	if err != nil {
		return false
	}
	return pathWithin(filepath.Join(wd, filepath.FromSlash(PlanDirSegment)), absPath)
}

// IsAutoMemPath reports whether absPath sits inside the auto-memory directory
// memDir. memDir is the already-resolved <appHome>/memory path — the caller
// derives it from memdir.MemoryDir so pkg/permission stays free of an
// internal/memdir import. Same containment shape as IsPlanFilePath (rejects the
// dir root itself, "..", siblings, and absolute-elsewhere). Port of
// ref/src/memdir/paths.ts:isAutoMemPath, narrowed to one fixed dir — evva drops
// ref's user-configurable autoMemoryDirectory and its malicious-repo attack
// surface (PRD §5.8). Empty memDir → false, so the carve-out is inert when
// auto-memory is off.
func IsAutoMemPath(memDir, absPath string) bool {
	return pathWithin(memDir, absPath)
}

// pathWithin reports whether abs sits strictly inside root — not root itself,
// not a sibling, not reachable only via "..". Both are resolved with
// filepath.Abs first so callers can pass user-supplied paths without
// pre-normalising. This is the single containment check behind both
// IsPlanFilePath and IsAutoMemPath.
func pathWithin(root, abs string) bool {
	if root == "" || abs == "" {
		return false
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(abs)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	// filepath.Rel returns "." for the root itself; "..pieces" for outside.
	if rel == "." || rel == "" {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// Mode is one of the four permission stances. The model is intentionally
// small: each mode pins a clear safelist, and the gate's pipeline (deny
// rules → ask rules → mode safelist → allow rules → ask) is the same
// across every mode.
//
//   - ModeDefault: read-only tools and agent self-coordination (subagent,
//     task_*, todo_*, tool_search, skill, ask_user_question) auto-allow.
//     Bash auto-allows when the classifier reports a read-only command.
//     Everything else asks. Safe for beginners and sensitive work.
//   - ModeAcceptEdits: ModeDefault + edit/write/notebook_edit auto-allow,
//     plus common filesystem bash commands (mkdir, touch, mv, cp, ln,
//     chmod, chown, rmdir). Best for iterating on code under review.
//   - ModePlan: same read-only safelist as ModeDefault, plus read-only bash
//     commands via the classifier. Any other non-safelist tool is denied
//     outright (no prompt). Best for exploring a codebase before deciding
//     what to change.
//   - ModeBypass: every tool call runs with no prompting. Dangerous-command
//     classification still happens and is logged, but never blocks. Use
//     only inside isolated containers or VMs.
type Mode string

const (
	ModeDefault     Mode = "default"
	ModeAcceptEdits Mode = "accept_edits"
	ModePlan        Mode = "plan"
	ModeBypass      Mode = "bypass"
)

// modeCycle is the Shift+Tab cycle order.
var modeCycle = []Mode{
	ModeDefault,
	ModeAcceptEdits,
	ModePlan,
	ModeBypass,
}

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeDefault, ModeAcceptEdits, ModePlan, ModeBypass:
		return true
	}
	return false
}

// Next returns the next mode in the Shift+Tab cycle. Falls back to
// ModeDefault for an unknown mode (defensive — shouldn't happen).
func (m Mode) Next() Mode {
	for i, mm := range modeCycle {
		if mm == m {
			return modeCycle[(i+1)%len(modeCycle)]
		}
	}
	return ModeDefault
}

// ParseMode returns the Mode for s, or (ModeDefault, false) if s is unknown.
func ParseMode(s string) (Mode, bool) {
	m := Mode(s)
	if m.Valid() {
		return m, true
	}
	return ModeDefault, false
}

// Behavior is the outcome of a Rule match or a Decide() call.
type Behavior string

const (
	BehaviorAllow Behavior = "allow"
	BehaviorDeny  Behavior = "deny"
	BehaviorAsk   Behavior = "ask"
)

// Source tells you where a Rule came from. Used to scope a "Allow for this
// session" decision: a project rule goes to <workdir>/.evva/permissions.json,
// a session rule lives in memory only.
type Source string

const (
	SourceProject Source = "project"
	SourceUser    Source = "user"
	SourceSession Source = "session"
)

// Rule is one allow/deny/ask entry. ToolName must match a tool's wire name
// (see internal/tools/name.go). Content is tool-specific — for Bash it's a
// shell pattern; for Read/Write/Edit it's a path glob; empty means
// "every call to this tool."
type Rule struct {
	ToolName string
	Content  string
	Behavior Behavior
	Source   Source
}

// ToolCall is the minimum shape Decide() needs to evaluate a rule. The
// agent loop fills this in from llm.ToolCall before calling Decide.
type ToolCall struct {
	Name  string
	Input []byte // raw JSON; tool-specific matchers parse what they need
}

// Hint carries pre-computed classification info from a tool-specific
// classifier. Today only Bash sets this; other tools see a zero-value Hint.
//
// IsCommonFS marks commands like `mkdir`, `mv`, `cp` — they mutate but
// at a level the user has typically already approved of when picking
// `accept_edits`. `default` mode still asks for these.
type Hint struct {
	IsReadOnly  bool
	IsCommonFS  bool
	IsDangerous bool
	Matched     string // entry that triggered the classification, for UI
	Reason      string
}

// Decision is the resolved outcome. Behavior is always Allow or Deny after
// Decide() returns (Ask is resolved by the Broker before reaching the agent
// loop). Source is set when the user chose "Allow for this session" so the
// caller knows where to write the new rule.
type Decision struct {
	Behavior Behavior
	Reason   string
	AddRule  *Rule // non-nil when user chose "Allow for this session"
}

// ReadOnlyOrSelfTools is the baseline auto-allow set: tools that either
// don't touch the filesystem (read, grep, web_*, calc) or are part of
// the agent's own coordination surface (subagent spawn, task list,
// tool_search, skill). Auto-allowed in every mode except where deny/ask
// rules intervene.
//
// "Read-only" here means filesystem-write-free, not "doesn't change
// session state" — task_create / agent spawn do mutate session state,
// but they don't write files or run arbitrary shell. Those are the
// model's planning tools and should never block on a prompt.
//
// In ModePlan, anything NOT in this set is denied outright.
var ReadOnlyOrSelfTools = map[string]bool{
	"read":              true,
	"tree":              true,
	"grep":              true,
	"glob":              true,
	"web_fetch":         true,
	"web_search":        true,
	"json_query":        true,
	"calc":              true,
	"todo_write":        true,
	"ask_user_question": true,
	"agent":             true,
	"tool_search":       true,
	"skill":             true,
	// Daemon introspection — enumerate registered daemons and read their
	// captured output. Pure reads over agent-owned state; daemon_stop is
	// excluded since it mutates (terminates the daemon).
	"daemon_list":   true,
	"daemon_output": true,
	// Plan-mode coordination — the model must be able to enter and exit
	// plan mode while ModePlan denies everything else. Both are otherwise
	// session-state-only (they don't touch the filesystem outside of the
	// plan file, which has its own carve-out in Decide()).
	"enter_plan_mode": true,
	"exit_plan_mode":  true,
	// lsp access
	"lsp_request": true,
}

// AcceptEditsAutoAllow is the set of tools auto-allowed in addition to
// ReadOnlyOrSelfTools when the mode is ModeAcceptEdits. Bash isn't in
// the map — the gate checks the classifier's IsReadOnly / IsCommonFS
// hint separately to decide whether to auto-allow a shell call.
var AcceptEditsAutoAllow = map[string]bool{
	"edit":          true,
	"write":         true,
	"notebook_edit": true,
}
