package mode

import (
	"log/slog"
	"time"
)

// WorktreeController is the seam between the EnterWorktree / ExitWorktree
// tools and the owning agent — same shape and lifecycle as
// PlanModeController: the agent satisfies it directly, the tool
// constructors take a lookup closure so the agent can install itself
// after toolset construction without an init ordering hazard.
//
// SwitchWorkdir is the load-bearing call: implementations rebuild any
// active tool that captured the OLD workdir at construction time
// (filesystem tools, the bash tool), reload the EVVA.md / USER_PROFILE.md
// memory snapshot from the new path, and re-render the system prompt so
// the next LLM turn sees the new working directory.
//
// BeginWorktreeSession / EndWorktreeSession bracket a logical session;
// while a session is live, WorktreeSession() returns a non-nil pointer
// so EnterWorktree can refuse re-entry and ExitWorktree knows whether to
// no-op or actually tear the worktree down.
type WorktreeController interface {
	Workdir() string
	SwitchWorkdir(path string) error
	WorktreeSession() *WorktreeSession
	BeginWorktreeSession(s WorktreeSession)
	EndWorktreeSession()
	AgentID() string
	Logger() *slog.Logger
}

// WorktreeControllerLookup is the late-bound factory closure passed to
// the EnterWorktree / ExitWorktree constructors. Returning nil disables
// the tool — Execute surfaces a clear "no controller installed" error
// instead of crashing.
type WorktreeControllerLookup func() WorktreeController

// WorktreeSession records the metadata the worktree tools and AgentTool
// isolation need to clean up a worktree on exit. It is also what the
// subagent spawner threads into a child constructed under
// `isolation: "worktree"` — the child carries it so its own
// ExitWorktree (rarely invoked from a subagent but possible) and the
// post-run cleanup path in spawn.go agree on what to tear down.
type WorktreeSession struct {
	// OriginalWorkdir is the workdir the agent was in before EnterWorktree
	// fired. ExitWorktree restores it.
	OriginalWorkdir string
	// Path is the absolute path to the worktree directory.
	Path string
	// Branch is the branch name created with `git worktree add -b`.
	Branch string
	// Slug is the flattened slug used to compose Path and Branch — kept
	// for logging and the tool's confirmation message.
	Slug string
	// RepoRoot is the canonical repository root Path was anchored at. Set by
	// ProvisionMemberWorktree (SWT) — the swarm needs it to map a space
	// workdir nested inside a larger repo onto the same relative position
	// inside the worktree. Empty on sessions from the single-agent tools,
	// which have no such mapping to do.
	RepoRoot string
	// CreatedBySubagent marks worktrees the AgentTool's isolation path
	// created (vs the user-invoked EnterWorktree tool). The post-run
	// cleanup in spawn.go uses this to decide whether to auto-remove on
	// clean exit.
	CreatedBySubagent bool
	CreatedAt         time.Time
}

func resolveWorktreeController(lookup WorktreeControllerLookup) WorktreeController {
	if lookup == nil {
		return nil
	}
	return lookup()
}
