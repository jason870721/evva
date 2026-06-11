package agent

import (
	agent_impl "github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/pkg/permission"
)

// PermissionMode is the typed form of the agent's permission stance.
// Use the typed constants (PermissionDefault / PermissionAcceptEdits /
// PermissionPlan / PermissionBypass) together with WithPermissionMode
// so a typo becomes a compile error rather than a silent fall-through.
type PermissionMode string

const (
	// PermissionDefault asks before every mutating tool call. Read-only
	// tools (read, grep, glob, web_*, agent self-coordination) auto-allow.
	// Bash auto-allows when the classifier reports a read-only command.
	// Best for beginners and sensitive work.
	PermissionDefault PermissionMode = "default"

	// PermissionAcceptEdits extends PermissionDefault by auto-allowing
	// edit/write/notebook_edit plus common filesystem bash commands
	// (mkdir, touch, mv, cp, ln, chmod, chown, rmdir). Best for
	// iterating on code under review.
	PermissionAcceptEdits PermissionMode = "accept_edits"

	// PermissionPlan denies every non-safelist tool outright (no
	// prompt). The model can only read and explore. Best for codebase
	// orientation before committing to an approach.
	PermissionPlan PermissionMode = "plan"

	// PermissionBypass auto-allows every tool call with no prompting —
	// except calls matching an explicit deny rule, which are still
	// rejected (deny rules bind in every mode). Dangerous-command
	// classification still happens and is logged but never blocks. Use
	// only inside isolated containers, VMs, or downstream apps that have
	// no approval UI surface — see WithHeadlessBypass.
	PermissionBypass PermissionMode = "bypass"
)

// String returns the wire form of the mode — equal to the string form
// of the PermissionMode constant.
func (m PermissionMode) String() string { return string(m) }

// Valid reports whether m is one of the four known modes.
func (m PermissionMode) Valid() bool {
	switch m {
	case PermissionDefault, PermissionAcceptEdits, PermissionPlan, PermissionBypass:
		return true
	}
	return false
}

// WithPermissionMode sets the agent's initial permission stance. Pass
// one of the typed PermissionMode constants — passing an unknown
// PermissionMode (e.g. PermissionMode("by-pass") with a typo) results
// in a no-op option, but that path is hard to reach: the constants
// are the only typed values exposed.
//
//	agent.WithPermissionMode(agent.PermissionBypass)
//
// If your config layer hands you a string (YAML / CLI flag), convert
// at the boundary with PermissionMode(s).
func WithPermissionMode(m PermissionMode) Option {
	parsed, ok := permission.ParseMode(string(m))
	if !ok {
		return func(*agent_impl.Agent) {}
	}
	return agent_impl.WithPermissionMode(parsed)
}

// WithHeadlessBypass is the convenience option for downstream apps that
// run an agent without an interactive approval surface (no TUI overlay,
// no CLI prompt). It bundles WithPermissionMode(PermissionBypass)
// behind a more discoverable name + a strong docstring.
//
// SECURITY: with bypass mode every tool call auto-succeeds. The agent
// will run any bash command the model emits, edit any file under its
// workdir, and fetch any URL it decides to. Use only in trusted
// environments (CI runners, sandboxed containers, ephemeral VMs).
// Explicit deny rules in the permission store are the one exception:
// they are still enforced under bypass, so a host can pre-seed hard
// prohibitions for an otherwise fully autonomous agent.
//
// For an interactive host that wants real prompts, omit this option —
// the default permission mode asks before each mutating call. Wire
// your UI's approval flow through agent.RespondPermission.
func WithHeadlessBypass() Option {
	return WithPermissionMode(PermissionBypass)
}
