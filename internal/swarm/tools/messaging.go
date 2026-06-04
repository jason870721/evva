package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/store"
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
			var b strings.Builder
			fmt.Fprintf(&b, "Swarm members (%d):\n", len(members))
			for _, m := range members {
				fmt.Fprintf(&b, "- %s [%s] %s/%s", m.Name, m.Role, m.Membership, m.Run)
				if m.CurrentTask != 0 {
					fmt.Fprintf(&b, " task#%d", m.CurrentTask)
				}
				if m.WhenToUse != "" {
					fmt.Fprintf(&b, " — %s", m.WhenToUse)
				}
				b.WriteByte('\n')
			}
			return pubtools.Result{Content: b.String(), Metadata: members}, nil
		},
	}
}
