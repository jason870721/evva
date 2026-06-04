package swarm

import (
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/config"
)

// MemberContext is one swarm member's runtime identity, handed to that member's
// custom tools (SPRD-1-7) so send_message can bake the sender and the task tools
// can reach the ledger/bus/roster.
//
// It travels through the agent's Config rather than a factory closure on
// purpose: pkg/agent.WithCustomTool registers ONE factory per tool name
// process-wide (the first registration wins), and a shared factory can't carry
// per-agent identity in a closure — nor could a closure-captured *SwarmSpace,
// which would leak across spaces. So each member's tools read their identity
// from the per-agent Config they are built against. The Space pointer is
// in-memory only: a swarm agent's Config must never be serialized (the custom
// bag holds a live pointer).
type MemberContext struct {
	Name  string
	Role  agentdef.Role
	Space *SwarmSpace
}

// memberContextKey namespaces the entry in Config.CustomConfig (the
// downstream-app-defined bag evva itself never reads).
const memberContextKey = "swarm.member_context"

// BindMemberContext stashes mc on cfg's custom bag in memory. It writes the map
// directly rather than via Config.SetCustom, which persists to disk and cannot
// marshal the live *SwarmSpace pointer.
func BindMemberContext(cfg *config.Config, mc MemberContext) {
	if cfg == nil {
		return
	}
	if cfg.CustomConfig == nil {
		cfg.CustomConfig = map[string]any{}
	}
	cfg.CustomConfig[memberContextKey] = mc
}

// MemberContextFrom recovers the MemberContext a member's tools were built
// against, or false when none was bound.
func MemberContextFrom(cfg *config.Config) (MemberContext, bool) {
	if cfg == nil {
		return MemberContext{}, false
	}
	v, ok := cfg.GetCustom(memberContextKey)
	if !ok {
		return MemberContext{}, false
	}
	mc, ok := v.(MemberContext)
	return mc, ok
}
