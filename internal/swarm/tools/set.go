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
)

// init classifies the read/self swarm tools as auto-allow in pkg/permission's
// name-keyed safelist (the one extension seam the gate exposes). The write-class
// ledger mutators — task_assign, task_update_status, task_verify — stay out, so
// they default to "ask" and the User oversees them when a space runs in a
// non-bypass permission mode (invariant #6). task_create is planning, not a
// dispatch, so it auto-allows: the Leader decomposes work freely and the gated
// commit is task_assign (the moment a Worker is actually put to work).
func init() {
	for _, n := range []string{
		toolSendMessage, toolListMembers, toolTaskList, toolMyTasks, toolTaskGet, toolTaskCreate,
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
	common := []string{toolSendMessage, toolListMembers}
	if role == agentdef.RoleLeader {
		return append(common, toolTaskCreate, toolTaskAssign, toolTaskUpdateStatus, toolTaskVerify, toolTaskList)
	}
	return append(common, toolMyTasks, toolTaskGet)
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
