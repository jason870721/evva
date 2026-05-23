package mode

import "github.com/johnny1110/evva/internal/permission"

// PlanModeController is the seam between the EnterPlanMode / ExitPlanMode
// tools and the owning agent. The agent satisfies it directly; the tool
// constructors take a lookup closure (returns a PlanModeController) so
// builtin registration can stay late-bound — the agent installs itself
// after toolset.NewToolState runs.
//
// The interface is intentionally narrow: only what the two plan-mode
// tools actually touch. Permission mode mutation, the pre-plan stash
// (used by ExitPlanMode to restore), the workdir (for the plan-file
// path), the approval broker (for ExitPlanMode's user-approval gate),
// and the AgentID that the broker uses to route the approval event.
type PlanModeController interface {
	PermissionMode() permission.Mode
	SetPermissionMode(m permission.Mode)
	PrePlanMode() permission.Mode
	SetPrePlanMode(m permission.Mode)
	Workdir() string
	Broker() permission.Broker
	AgentID() string
	PlanName() string
	SetPlanName(name string)
}

// ControllerLookup is the late-bound factory closure passed to the
// EnterPlanMode / ExitPlanMode constructors. Returning nil disables the
// tool — Execute surfaces a clear "no controller installed" error
// instead of crashing.
type ControllerLookup func() PlanModeController
