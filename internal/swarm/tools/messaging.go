package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/common"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// newSendMessage builds the per-agent send_message tool. The sender is baked
// from the MemberContext (§6.1): Execute has no "who am I", so each agent's
// instance carries its own name. Delivery is durable + non-blocking (the bus
// writes the row, then signals the recipient's mailbox).
func newSendMessage(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolSendMessage,
		desc: "Send a message to another swarm member, or broadcast to the whole team. " +
			"Use `to` with a member name (see list_members for valid names) or \"all\" to reach every active member. " +
			"The recipient sees who sent it, so this is how you ask a teammate for something, hand off context, or report progress to the leader.",
		schema: `{"type":"object","properties":{` +
			`"to":{"type":"string","description":"Recipient member name, or \"all\" to broadcast to every active member. Check list_members for valid names."},` +
			`"subject":{"type":"string","description":"Optional short subject line."},` +
			`"body":{"type":"string","description":"The message body."},` +
			`"ref_task":{"type":"integer","description":"Optional id of the task this message relates to."}` +
			`},"required":["to","body"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				To      string `json:"to"`
				Subject string `json:"subject"`
				Body    string `json:"body"`
				RefTask *int64 `json:"ref_task"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("send_message: invalid input: %v", err), nil
			}
			if strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Body) == "" {
				return errf("send_message: both 'to' and 'body' are required"), nil
			}

			// Role-addressing (§3.5): a bare role like "leader" resolves to that
			// role's unique member name, so the model needn't know it's "lead"/"pm".
			in.To = mc.Space.Roster.ResolveRecipient(in.To)

			// Validate the recipient against the live roster (the "all" broadcast
			// is exempt). Without this, a wrong name is silently dead-lettered (see
			// rosterHas). Surfacing a correctable error with the real names lets the
			// model retry.
			if in.To != store.RecipientAll {
				if ok, names := rosterHas(mc.Space, in.To); !ok {
					return errf("send_message: no swarm member named %q. Valid recipients: %s (or \"all\"). "+
						"Run list_members for exact names — the leader's role is \"leader\" but its member name may differ.",
						in.To, strings.Join(names, ", ")), nil
				}
			}

			uuid, err := mc.Space.Bus.Send(store.Message{
				Sender:    mc.Name,
				Recipient: in.To,
				Subject:   in.Subject,
				Body:      in.Body,
				RefTask:   in.RefTask,
			})
			if err != nil {
				return errf("send_message: %v", err), nil
			}
			if in.To == store.RecipientAll {
				return okf("Broadcast from %s delivered to all active members.", mc.Name), nil
			}
			return okf("Message delivered to %s (id %s).", in.To, uuid), nil
		},
	}
}

// newListMembers builds the read-only roster view: who is on the team, their
// role/specialty, and their current membership/run status — used before sending
// mail to pick the right recipient (§5.3).
func newListMembers(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolListMembers,
		desc: "List the current swarm members and their status: name, role, specialty, " +
			"membership (active/frozen), run status (idle/busy/suspended), and current task. " +
			"Use this to find the right teammate before send_message.",
		schema: `{"type":"object","properties":{}}`,
		exec: func(_ context.Context, _ json.RawMessage) (pubtools.Result, error) {
			members := mc.Space.Roster.Snapshot()
			pendingAlarms := mc.Space.AlarmScheduler().List()
			var b strings.Builder
			fmt.Fprintf(&b, "Swarm members (%d) — times are local %s:\n", len(members), common.ZoneLabel())
			for _, m := range members {
				// DisplayPhase shows the fine event-derived sub-phase (e.g.
				// "executing:bash", "waiting-approval:bash") so a teammate can see
				// what a member is actually doing, not just that it is "busy".
				fmt.Fprintf(&b, "- %s [%s] %s/%s", m.Name, m.Role, m.Membership, m.DisplayPhase())
				if m.CurrentTask != 0 {
					fmt.Fprintf(&b, " task#%d", m.CurrentTask)
				}
				if m.WhenToUse != "" {
					fmt.Fprintf(&b, " — %s", m.WhenToUse)
				}
				// Always surface the member's crontab (RP-7 §3.5): read live from
				// the space (the schedule's owner) so a leader whose context was
				// compacted re-learns who it put on duty every time it lists.
				if sch, ok := mc.Space.ScheduleFor(m.Name); ok {
					fmt.Fprintf(&b, "  ⏰ %s", formatSchedule(sch))
				}
				b.WriteByte('\n')
				// One-shot alarms aimed at this member (RP-7 sibling): surface them
				// so the leader re-learns pending wakes after a context compaction,
				// and so alarm_clear has an id source.
				for _, a := range pendingAlarms {
					if a.Target == m.Name {
						label := ""
						if a.Label != "" {
							label = " " + a.Label
						}
						fmt.Fprintf(&b, "    ⏰ %s at %s%s\n", a.ID, common.Stamp(a.FireAt), label)
					}
				}
			}
			return pubtools.Result{Content: b.String(), Metadata: members}, nil
		},
	}
}
