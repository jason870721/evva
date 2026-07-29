package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/permission"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// Tool wire names (snake_case, as the model sees them).
const (
	toolSendMessage      = "send_message"
	toolListMembers      = "list_members"
	toolTaskCreate       = "task_create"
	toolTaskAssign       = "task_assign"
	toolTaskUpdateStatus = "task_update_status"
	toolTaskVerify       = "task_verify"
	toolTaskList         = "task_list"
	toolMyTasks          = "my_tasks"
	toolTaskGet          = "task_get"
	toolTaskDone         = "task_done"
	toolScheduleSet      = "schedule_set"
	toolScheduleClear    = "schedule_clear"
	toolAlarmSet         = "alarm_set"
	toolAlarmClear       = "alarm_clear"
	toolTaskPropose      = "task_propose"
	toolProposalList     = "proposal_list"
	toolProposalAccept   = "proposal_accept"
	toolProposalDecline  = "proposal_decline"
	toolSkillPublish     = "skill_publish"
	toolMemberSpawn      = "member_spawn"
	toolMemberRetire     = "member_retire"
	toolBlackboardWrite  = "blackboard_write"
	toolBlackboardRead   = "blackboard_read"
	toolWorktreeMerge    = "worktree_merge"
)

// init classifies the swarm's coordination tools as auto-allow in
// pkg/permission's name-keyed safelist (the one extension seam the gate
// exposes). This includes the Leader's task-ledger writes — task_assign,
// task_update_status, task_verify: they are team coordination, not file/shell
// side effects, and the store already enforces the leader-only guard
// (store.ErrNotLeader), so routing them through a human approval bought no real
// safety while stalling the swarm's core create→assign→verify loop on every
// dispatch. The actual permission boundary is a Worker's file/shell writes,
// which are NOT listed here and still gate in a non-bypass mode (invariant #6).
// Use permission_mode: bypass only when you also want worker writes ungated.
//
// skill_publish DOES write a file, but only ever inside the space's own
// agents/skills/ dir, and its recourse is governance-shaped rather than
// approval-shaped: the tool_use event self-audits (RP-17), the web lists and
// deletes shared skills (User final arbiter), and an RP-24 deny rule on the
// name blocks it outright in any mode. Gating it on human approval would stall
// exactly the unattended institutionalization it exists for (EX-6).
//
// worktree_merge (SWT) is deliberately ABSENT from this list: it rewrites the
// operator's base branch, and the governance-shaped argument above does not
// extend to the user's repo history. It gates like any other write in
// `default` mode; unattended swarms open it with the existing levers (leader
// permission_mode: bypass, or an RP-11 allow rule).
func init() {
	for _, n := range []string{
		toolSendMessage, toolListMembers, toolTaskList, toolMyTasks, toolTaskGet,
		toolTaskCreate, toolTaskAssign, toolTaskUpdateStatus, toolTaskVerify, toolTaskDone,
		toolScheduleSet, toolScheduleClear,
		toolAlarmSet, toolAlarmClear,
		toolTaskPropose, toolProposalList, toolProposalAccept, toolProposalDecline,
		toolSkillPublish,
		// member_spawn/member_retire are governance-shaped like the rest:
		// spawning writes no file and runs no shell — it clones an existing,
		// operator-authored definition, bounded by settings.max_members and
		// each clone's inherited budget cap; retiring touches only spawned
		// members. The events self-audit and the web lists every clone.
		toolMemberSpawn, toolMemberRetire,
		// blackboard_write is governance-shaped like skill_publish: it writes
		// exactly one size-capped file inside the space's own .vero/, the
		// blackboard_updated event self-audits every change, and the web +
		// disk keep the operator final arbiter. Gating the leader's standing
		// brief on human approval would defeat its zero-wake economics.
		toolBlackboardWrite, toolBlackboardRead,
	} {
		permission.ReadOnlyOrSelfTools[n] = true
	}
}

// Set implements swarm.ToolSet: it attaches the role-appropriate swarm custom
// tools to each agent at construction.
type Set struct{}

// For returns the WithCustomTool options for a member's role. Per-agent identity
// (sender name, the space) does NOT ride these options: pkg/agent.WithCustomTool
// registers one factory per tool name process-wide, so each factory instead
// reads the member's identity from the per-agent Config it is built against
// (swarm.MemberContext, bound at construction). Hence only role is needed here.
func (Set) For(_ string, role agentdef.Role, _ *swarm.SwarmSpace) []agent.Option {
	names := toolNamesForRole(role)
	opts := make([]agent.Option, 0, len(names))
	for _, n := range names {
		opts = append(opts, agent.WithCustomTool(pubtools.ToolName(n), factories[n]))
	}
	return opts
}

// toolNamesForRole is the role→tool-set map — the permission boundary IS the
// tool boundary. Every agent gets send_message + list_members; the Leader adds
// the task-ledger writes, a Worker the read-only task views.
func toolNamesForRole(role agentdef.Role) []string {
	// Every member gets one-shot alarms: a worker may wake itself, the leader
	// may also target a teammate (gated inside alarm_set). schedule_set (below)
	// stays leader-only because it is recurring cross-member duty. Every
	// member reads the team blackboard (a wake brief can go stale mid-run);
	// writing it is the leader's curation monopoly (BB §4).
	common := []string{toolSendMessage, toolListMembers, toolAlarmSet, toolAlarmClear, toolBlackboardRead}
	if role == agentdef.RoleLeader {
		// skill_publish is leader-only by the EX-6 governance shape: the one
		// agent allowed to author is the one whose job is institutionalizing
		// team procedure, and only into the shared dir. blackboard_write is
		// leader-only by the same judgment monopoly the task ledger enforces:
		// the board is the leader's synthesized picture; workers influence it
		// through reports and task_propose.
		// worktree_merge is leader-only for a structural reason, not a trust
		// one (SWT §4): the base checkout is one shared resource, and the
		// leader's single run slot serializes merges into it by construction.
		return append(common, toolTaskCreate, toolTaskAssign, toolTaskUpdateStatus, toolTaskVerify, toolTaskList,
			toolScheduleSet, toolScheduleClear, toolProposalList, toolProposalAccept, toolProposalDecline,
			toolSkillPublish, toolMemberSpawn, toolMemberRetire, toolBlackboardWrite, toolWorktreeMerge)
	}
	// task_propose is the worker's ONLY work-inlet (RP-23): file trackable
	// work without piercing the ledger's single-writer invariant. The leader
	// doesn't get it — it just task_creates. task_done is the worker's ONLY
	// ledger write (DWF §4): its own task, running→verifying, result attached
	// — the store's writer matrix enforces ownership.
	return append(common, toolMyTasks, toolTaskGet, toolTaskDone, toolTaskPropose)
}

// factories maps a tool name to its build factory. Each recovers the member's
// MemberContext from its Config and constructs the tool bound to that identity.
var factories = map[string]func(pubtools.State) (pubtools.Tool, error){
	toolSendMessage:      bind(newSendMessage),
	toolListMembers:      bind(newListMembers),
	toolTaskCreate:       bind(newTaskCreate),
	toolTaskAssign:       bind(newTaskAssign),
	toolTaskUpdateStatus: bind(newTaskUpdateStatus),
	toolTaskVerify:       bind(newTaskVerify),
	toolTaskList:         bind(newTaskList),
	toolMyTasks:          bind(newMyTasks),
	toolTaskGet:          bind(newTaskGet),
	toolTaskDone:         bind(newTaskDone),
	toolScheduleSet:      bind(newScheduleSet),
	toolScheduleClear:    bind(newScheduleClear),
	toolAlarmSet:         bind(newAlarmSet),
	toolAlarmClear:       bind(newAlarmClear),
	toolTaskPropose:      bind(newTaskPropose),
	toolProposalList:     bind(newProposalList),
	toolProposalAccept:   bind(newProposalAccept),
	toolProposalDecline:  bind(newProposalDecline),
	toolSkillPublish:     bind(newSkillPublish),
	toolMemberSpawn:      bind(newMemberSpawn),
	toolMemberRetire:     bind(newMemberRetire),
	toolBlackboardWrite:  bind(newBlackboardWrite),
	toolBlackboardRead:   bind(newBlackboardRead),
	toolWorktreeMerge:    bind(newWorktreeMerge),
}

// bind adapts a MemberContext tool constructor into a pkg/toolset factory: it
// reads the per-agent MemberContext off the Config at build time.
func bind(ctor func(swarm.MemberContext) pubtools.Tool) func(pubtools.State) (pubtools.Tool, error) {
	return func(s pubtools.State) (pubtools.Tool, error) {
		mc, ok := swarm.MemberContextFrom(s.Config())
		if !ok {
			return nil, fmt.Errorf("swarm tools: agent config carries no member context")
		}
		return ctor(mc), nil
	}
}

// swarmTool is the shared pkg/tools.Tool shell; each tool supplies its name,
// description, schema, and an exec closure that captures the MemberContext.
type swarmTool struct {
	name   string
	desc   string
	schema string
	exec   func(ctx context.Context, input json.RawMessage) (pubtools.Result, error)
}

func (t *swarmTool) Name() string            { return t.name }
func (t *swarmTool) Description() string     { return t.desc }
func (t *swarmTool) Schema() json.RawMessage { return json.RawMessage(t.schema) }

func (t *swarmTool) Execute(ctx context.Context, _ *slog.Logger, input json.RawMessage) (pubtools.Result, error) {
	return t.exec(ctx, input)
}

// errf builds a model-visible tool error (IsError), not a Go error, so a
// rejection (illegal transition, bad input) surfaces to the model without
// aborting the run.
func errf(format string, args ...any) pubtools.Result {
	return pubtools.Result{IsError: true, Content: fmt.Sprintf(format, args...)}
}

func okf(format string, args ...any) pubtools.Result {
	return pubtools.Result{Content: fmt.Sprintf(format, args...)}
}

// rosterHas reports whether name is a current member of the space, and returns
// the full list of member names for a correctable error message. It is the
// shared recipient/assignee guard for send_message and task_create: addressing
// a non-member (e.g. the classic "leader" vs member-name "lead" slip) would
// otherwise dead-letter — durably stored to a mailbox nobody drains, waking no
// one. A space with no roster (the lite unit-test construction) is treated as
// valid so the check is a no-op there; production spaces always carry a roster.
func rosterHas(sp *swarm.SwarmSpace, name string) (ok bool, names []string) {
	if sp == nil || sp.Roster == nil {
		return true, nil
	}
	for _, m := range sp.Roster.Snapshot() {
		names = append(names, m.Name)
		if m.Name == name {
			ok = true
		}
	}
	return ok, names
}
