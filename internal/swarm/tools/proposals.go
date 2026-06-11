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

// proposals.go is the RP-23 bottom-up work inlet, tool side. A worker files
// trackable work with task_propose; the leader reviews with proposal_list and
// settles each with proposal_accept (one atomic store call → a running task)
// or proposal_decline (note mandatory — the RP-12 closure discipline as
// schema, not etiquette). Workers keep ZERO write access to the task ledger.

// newTaskPropose is the Worker's inlet: file an open proposal and wake the
// leader with its content.
func newTaskPropose(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskPropose,
		desc: "Propose a piece of work for the board — use this whenever you discover something that should be " +
			"TRACKED and verified (a defect, a risk, a follow-up worth doing), instead of burying it in a chat message. " +
			"The leader is notified and will accept it into a real task (you'll hear back with the task id) or " +
			"decline it with a reason. Proposing does not assign work to anyone by itself.",
		schema: `{"type":"object","properties":{` +
			`"title":{"type":"string","description":"Short imperative title for the work."},` +
			`"spec":{"type":"string","description":"What needs doing and how to verify it. Self-contained — the assignee may have no other context."},` +
			`"suggested_assignee":{"type":"string","description":"Optional: the member best placed to do it (see list_members)."}` +
			`},"required":["title","spec"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Title             string `json:"title"`
				Spec              string `json:"spec"`
				SuggestedAssignee string `json:"suggested_assignee"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_propose: invalid input: %v", err), nil
			}
			suggested := strings.TrimSpace(in.SuggestedAssignee)
			if suggested != "" {
				if ok, names := rosterHas(mc.Space, suggested); !ok {
					return errf("task_propose: no member named %q to suggest. Valid members: %s.",
						suggested, strings.Join(names, ", ")), nil
				}
			}
			id, err := mc.Space.Store.CreateProposal(store.Proposal{
				Proposer: mc.Name, Title: strings.TrimSpace(in.Title), Spec: in.Spec,
				SuggestedAssignee: suggested,
			})
			if err != nil {
				return errf("task_propose: %v", err), nil
			}

			leader := mc.Space.Roster.LeaderName()
			if leader == "" || leader == mc.Name {
				return okf("Proposal #%d filed.", id), nil
			}
			body := fmt.Sprintf("%s proposes new work — proposal #%d: %s\n\n%s", mc.Name, id, in.Title, in.Spec)
			if suggested != "" {
				body += fmt.Sprintf("\n\nSuggested assignee: %s", suggested)
			}
			body += fmt.Sprintf("\n\nDecide it with proposal_accept {proposal_id: %d} or proposal_decline {proposal_id: %d, note: \"…\"}.", id, id)
			if _, err := mc.Space.Bus.Send(store.Message{
				Sender: mc.Name, Recipient: leader,
				Subject: fmt.Sprintf("Proposal #%d: %s", id, in.Title),
				Body:    body,
			}); err != nil {
				return errf("task_propose: proposal #%d filed but notifying the leader failed: %v", id, err), nil
			}
			return okf("Proposal #%d filed and sent to %s. You'll be notified when it is decided.", id, leader), nil
		},
	}
}

// newProposalList is the Leader's review view — re-queryable, so a leader
// whose context was compacted can recover open proposal ids any time (the
// same reason task_list exists).
func newProposalList(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolProposalList,
		desc: "List open work proposals filed by workers (id, proposer, title, spec, suggested assignee), oldest " +
			"first. Decide each with proposal_accept or proposal_decline. Read-only.",
		schema: `{"type":"object","properties":{}}`,
		exec: func(_ context.Context, _ json.RawMessage) (pubtools.Result, error) {
			open, err := mc.Space.Store.ListProposals(store.ProposalOpen)
			if err != nil {
				return errf("proposal_list: %v", err), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Open proposals (%d):\n", len(open))
			for _, p := range open {
				fmt.Fprintf(&b, "#%d [%s] %s", p.ID, p.Proposer, p.Title)
				if p.SuggestedAssignee != "" {
					fmt.Fprintf(&b, " (suggests: %s)", p.SuggestedAssignee)
				}
				if p.Spec != "" {
					fmt.Fprintf(&b, "\n    spec: %s", p.Spec)
				}
				b.WriteByte('\n')
			}
			return pubtools.Result{Content: b.String(), Metadata: open}, nil
		},
	}
}

// newProposalAccept turns an open proposal into a running task — one atomic
// store transaction (claim + create + backfill), then notifies proposer and
// assignee. Only the Leader decides.
func newProposalAccept(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolProposalAccept,
		desc: "Accept a worker's proposal: it becomes a real task, assigned and running, in one step. The assignee " +
			"defaults to the proposal's suggestion; pass one to override. The proposer is notified with the task id. " +
			"Only the Leader decides proposals.",
		schema: `{"type":"object","properties":{` +
			`"proposal_id":{"type":"integer","description":"Id of the open proposal (see proposal_list)."},` +
			`"assignee":{"type":"string","description":"Optional: who does the work; defaults to the proposal's suggested assignee."}` +
			`},"required":["proposal_id"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				ProposalID int64  `json:"proposal_id"`
				Assignee   string `json:"assignee"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("proposal_accept: invalid input: %v", err), nil
			}
			p, err := mc.Space.Store.GetProposal(in.ProposalID)
			if err != nil {
				return errf("proposal_accept: %v", err), nil
			}
			assignee := strings.TrimSpace(in.Assignee)
			if assignee == "" {
				assignee = p.SuggestedAssignee
			}
			if assignee == "" {
				return errf("proposal_accept: proposal #%d suggests no assignee — pass one (see list_members).", p.ID), nil
			}
			if ok, names := rosterHas(mc.Space, assignee); !ok {
				return errf("proposal_accept: no member named %q. Valid members: %s.", assignee, strings.Join(names, ", ")), nil
			}

			task, err := mc.Space.Store.AcceptProposal(p.ID, leaderActor(mc), assignee)
			if err != nil {
				return errf("proposal_accept: %v", err), nil
			}

			// Wake the assignee exactly like task_assign would, then close the
			// loop with the proposer (RP-12: input that landed must hear back).
			refID := task.ID
			body := fmt.Sprintf("You are assigned task #%d: %s", task.ID, task.Title)
			if task.Spec != "" {
				body += "\n\n" + task.Spec
			}
			body += fmt.Sprintf("\n\n(From proposal #%d by %s.)", p.ID, p.Proposer)
			if _, err := mc.Space.Bus.Send(store.Message{
				Sender: mc.Name, Recipient: task.Assignee,
				Subject: fmt.Sprintf("Task #%d assigned", task.ID),
				Body:    body, RefTask: &refID,
			}); err != nil {
				return errf("proposal_accept: task #%d created but notifying %s failed: %v", task.ID, task.Assignee, err), nil
			}
			if p.Proposer != task.Assignee && p.Proposer != mc.Name {
				_, _ = mc.Space.Bus.Send(store.Message{
					Sender: mc.Name, Recipient: p.Proposer,
					Subject: fmt.Sprintf("Proposal #%d accepted → task #%d", p.ID, task.ID),
					Body: fmt.Sprintf("Your proposal #%d %q was accepted and is now task #%d, assigned to %s.",
						p.ID, p.Title, task.ID, task.Assignee),
					RefTask: &refID,
				})
			}
			return okf("Proposal #%d accepted → task #%d, assigned to %s and running.", p.ID, task.ID, task.Assignee), nil
		},
	}
}

// newProposalDecline closes an open proposal with a mandatory reason and
// tells the proposer why — schema-enforced closure, not prompt etiquette.
func newProposalDecline(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolProposalDecline,
		desc: "Decline a worker's proposal. A note explaining WHY is required — the proposer is told, so they can " +
			"calibrate future proposals. Re-raising later means filing a new proposal. Only the Leader decides.",
		schema: `{"type":"object","properties":{` +
			`"proposal_id":{"type":"integer","description":"Id of the open proposal (see proposal_list)."},` +
			`"note":{"type":"string","description":"Required: why it is declined (or deferred)."}` +
			`},"required":["proposal_id","note"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				ProposalID int64  `json:"proposal_id"`
				Note       string `json:"note"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("proposal_decline: invalid input: %v", err), nil
			}
			if strings.TrimSpace(in.Note) == "" {
				return errf("proposal_decline: a note explaining why is required."), nil
			}
			p, err := mc.Space.Store.GetProposal(in.ProposalID)
			if err != nil {
				return errf("proposal_decline: %v", err), nil
			}
			if err := mc.Space.Store.DeclineProposal(p.ID, leaderActor(mc), in.Note); err != nil {
				return errf("proposal_decline: %v", err), nil
			}
			if p.Proposer != mc.Name {
				_, _ = mc.Space.Bus.Send(store.Message{
					Sender: mc.Name, Recipient: p.Proposer,
					Subject: fmt.Sprintf("Proposal #%d declined", p.ID),
					Body:    fmt.Sprintf("Your proposal #%d %q was declined: %s", p.ID, p.Title, in.Note),
				})
			}
			return okf("Proposal #%d declined; %s has been told why.", p.ID, p.Proposer), nil
		},
	}
}
