package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// newBlackboardWrite builds the Leader's blackboard_write tool (BB): replace
// the team blackboard — the one leader-curated document every member sees in
// every wake brief. Writing is the leader's judgment monopoly (the task
// ledger's single-writer spirit); the role-based tool injection is the gate,
// the same way skill_publish is leader-only. Whole-document replace, never
// patch: the model re-emits the bounded document, so a retried write
// converges and the size cap is enforceable at this one point.
func newBlackboardWrite(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolBlackboardWrite,
		desc: "Replace the team blackboard: the ONE standing document every member sees in every wake brief — " +
			"your current picture of the goal, standing decisions, who-owns-what, and the current phase. " +
			"Full-document replace: send the complete new board, not a diff. Updating costs zero wakes " +
			"(members see it whenever they next wake), so use it for standing context instead of broadcast mail; " +
			"broadcast only when someone must act NOW. Update at milestones, not per message, and prune stale " +
			"lines to stay under the size cap. Empty content clears the board. The operator can read the board " +
			"on the web and edit it on disk (.vero/blackboard.md).",
		schema: `{"type":"object","properties":{` +
			`"content":{"type":"string","description":"The full new blackboard document (markdown). Replaces the whole board; empty string clears it."}` +
			`},"required":["content"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("blackboard_write: invalid input: %v", err), nil
			}
			n, err := mc.Space.WriteBlackboard(mc.Name, in.Content)
			if err != nil {
				return errf("blackboard_write: %v", err), nil
			}
			if n == 0 {
				return okf("Blackboard cleared — wake briefs carry no board section until you post again."), nil
			}
			return okf("Blackboard updated (%d bytes). Every member sees it in its next wake brief — no one was "+
				"woken; if someone must act on this NOW, send_message them.", n), nil
		},
	}
}

// newBlackboardRead builds the every-member blackboard_read tool (BB): fetch
// the board mid-run. The wake brief already carries the board, so this exists
// for freshness — a long run during which a teammate mentions an update, or a
// member acting on standing context it wants re-verified before a decision.
func newBlackboardRead(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolBlackboardRead,
		desc: "Read the team blackboard — the leader's standing team picture — as it is RIGHT NOW. " +
			"Your wake brief already carries the board as of your wake; use this mid-run when it may have " +
			"changed since (a teammate mentioned an update, or you are about to act on standing context).",
		schema: `{"type":"object","properties":{}}`,
		exec: func(_ context.Context, _ json.RawMessage) (pubtools.Result, error) {
			content, mtime, by := mc.Space.Blackboard()
			if content == "" {
				return okf("The team blackboard is empty — no standing team context has been posted."), nil
			}
			head := fmt.Sprintf("Team blackboard (updated %s", swarm.AgeString(time.Since(mtime)))
			if by != "" {
				head += " by " + by
			}
			return okf("%s):\n\n%s", head, content), nil
		},
	}
}
