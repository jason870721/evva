package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/swarm"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// newWorktreeMerge builds the Leader's worktree_merge tool (SWT): integrate one
// member's committed work from its isolated worktree onto the space's base
// branch.
//
// Leader-only for a structural reason (§4): the base checkout is one shared
// resource and `git merge` from two processes into it is unsafe. The leader is
// one agent with one run slot, so leader-driven merges serialize by
// construction — no new locking, and the base branch becomes just another
// leader-owned ledger alongside the task store.
//
// Unlike the other leader tools this one is deliberately NOT registered in
// permission.ReadOnlyOrSelfTools: it rewrites the operator's base branch, and
// the governance-shaped auto-allow argument that covers the task ledger does
// not extend to the user's repo history. In `default` mode it prompts through
// the normal gate; unattended swarms use the existing levers (leader
// permission_mode: bypass, or an allow rule in the leader's permissions.json).
func newWorktreeMerge(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolWorktreeMerge,
		desc: "Merge a worker's committed work from its isolated worktree onto the team's base branch. " +
			"Run this during verification, AFTER you have inspected the work and BEFORE task_verify: " +
			"inspect → worktree_merge → task_verify. Only committed work merges — if the result says " +
			"nothing to integrate, the worker did not commit, so bounce the task back instead of approving it. " +
			"On CONFLICT nothing is applied (the merge is aborted and the base branch left clean): reject the " +
			"task back to running with task_verify {approve:false} and a note listing the conflicted paths, " +
			"telling the worker to merge the base branch into its OWN branch, resolve there, recommit, and " +
			"report again. Conflicts are always resolved on the worker's side, never on the base checkout.",
		schema: `{"type":"object","properties":{` +
			`"member":{"type":"string","description":"The member whose branch to merge."},` +
			`"task_id":{"type":"integer","description":"Optional: the task this merge settles. Recorded with the result so a conflict is traceable to its task."}` +
			`},"required":["member"]}`,
		exec: func(ctx context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Member string `json:"member"`
				TaskID int64  `json:"task_id"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("worktree_merge: invalid input: %v", err), nil
			}
			member := strings.TrimSpace(in.Member)
			if member == "" {
				return errf("worktree_merge: member is required"), nil
			}

			res, err := mc.Space.MergeMemberWorktree(ctx, member)
			if err != nil {
				return errf("worktree_merge: %v", err), nil
			}

			forTask := ""
			if in.TaskID > 0 {
				forTask = fmt.Sprintf(" (task #%d)", in.TaskID)
			}

			switch {
			case res.NoOp:
				return okf("Nothing to integrate%s: %s has no commits on %q beyond %q. The worker has not "+
					"committed its work — bounce the task back with task_verify {approve:false} telling it to commit.",
					forTask, member, res.Branch, res.BaseBranch), nil

			case len(res.Conflicts) > 0:
				return okf("MERGE CONFLICT%s — aborted, nothing applied, %q left clean. %s's branch %q conflicts in:\n%s\n\n"+
					"Reject the task: task_verify {approve:false, note:\"<these paths> — merge %s into your branch, "+
					"resolve in your own worktree, recommit, report again\"}. Do NOT try to resolve this yourself on "+
					"the base checkout.",
					forTask, res.BaseBranch, member, res.Branch, bullets(res.Conflicts), res.BaseBranch), nil
			}

			msg := fmt.Sprintf("Merged %s's branch %q into %q%s: %d commit(s), %d file(s) changed.",
				member, res.Branch, res.BaseBranch, forTask, res.Ahead, res.FilesChanged)
			if res.RefreshWarning != "" {
				msg += fmt.Sprintf("\n\nNote: %s's worktree was left on its old base (%s). It will pick the new "+
					"base up when it next merges %s into its branch at the start of a task.",
					member, res.RefreshWarning, res.BaseBranch)
			} else {
				msg += fmt.Sprintf(" %s's worktree now tracks the new %s tip.", member, res.BaseBranch)
			}
			return okf("%s", msg), nil
		},
	}
}

// bullets renders paths as a markdown list for the model-facing result.
func bullets(paths []string) string {
	var b strings.Builder
	for i, p := range paths {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  - ")
		b.WriteString(p)
	}
	return b.String()
}
