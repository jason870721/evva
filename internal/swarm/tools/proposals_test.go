package tools

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
)

// RP-23 tool surface: task_propose (worker inlet) → leader notification;
// proposal_accept → real running task + both notifications; proposal_decline
// → mandatory note + proposer told; proposal_list re-queryable.

func TestTaskProposeFilesAndNotifiesLeader(t *testing.T) {
	sp := realSpace(t) // members: leader, worker-a, worker-b
	tool := newTaskPropose(workerMC(sp, "worker-a"))

	res := exec(t, tool, `{"title":"add ETH stop-loss","spec":"naked position on ETHUSDT","suggested_assignee":"worker-b"}`)
	if res.IsError {
		t.Fatalf("task_propose: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Proposal #1 filed and sent to leader") {
		t.Errorf("result should confirm filing + leader notify: %s", res.Content)
	}

	open, _ := sp.Store.ListProposals(store.ProposalOpen)
	if len(open) != 1 || open[0].Proposer != "worker-a" || open[0].SuggestedAssignee != "worker-b" {
		t.Fatalf("open proposals = %+v, want one from worker-a suggesting worker-b", open)
	}
	unread, _ := sp.Store.UnreadFor("leader")
	if len(unread) != 1 {
		t.Fatalf("leader unread = %d, want the proposal notice", len(unread))
	}
	m, _ := sp.Store.GetMessage(unread[0])
	if !strings.Contains(m.Body, "proposal #1") || !strings.Contains(m.Body, "proposal_accept") {
		t.Errorf("leader notice should carry the id and the decide instructions:\n%s", m.Body)
	}

	// A suggested assignee must be a real member.
	if r := exec(t, tool, `{"title":"x","spec":"y","suggested_assignee":"ghost"}`); !r.IsError {
		t.Error("unknown suggested_assignee should be rejected")
	}
}

func TestProposalAcceptCreatesRunningTask(t *testing.T) {
	sp := realSpace(t)
	propose := newTaskPropose(workerMC(sp, "worker-a"))
	accept := newProposalAccept(leaderMC(sp))

	if r := exec(t, propose, `{"title":"patch the regression","spec":"see logs","suggested_assignee":"worker-b"}`); r.IsError {
		t.Fatalf("propose: %s", r.Content)
	}
	res := exec(t, accept, `{"proposal_id":1}`)
	if res.IsError {
		t.Fatalf("proposal_accept: %s", res.Content)
	}
	if !strings.Contains(res.Content, "task #1") || !strings.Contains(res.Content, "worker-b") {
		t.Errorf("accept result should name the task and assignee: %s", res.Content)
	}

	task, err := sp.Store.GetTask(1)
	if err != nil || task.Status != store.StatusRunning || task.Assignee != "worker-b" {
		t.Fatalf("task = %+v err=%v, want running/worker-b", task, err)
	}
	// The assignee is woken with the task, the proposer with the verdict.
	assigneeMail, _ := sp.Store.UnreadFor("worker-b")
	if len(assigneeMail) != 1 {
		t.Fatalf("assignee unread = %d, want the assignment", len(assigneeMail))
	}
	proposerMail, _ := sp.Store.UnreadFor("worker-a")
	if len(proposerMail) != 1 {
		t.Fatalf("proposer unread = %d, want the accepted notice", len(proposerMail))
	}
	pm, _ := sp.Store.GetMessage(proposerMail[0])
	if !strings.Contains(pm.Body, "accepted") || !strings.Contains(pm.Body, "task #1") {
		t.Errorf("proposer notice should close the loop with the task id:\n%s", pm.Body)
	}

	// Deciding it again fails loudly.
	if r := exec(t, accept, `{"proposal_id":1}`); !r.IsError {
		t.Error("re-accepting a decided proposal should error")
	}
}

func TestProposalAcceptAssigneeResolution(t *testing.T) {
	sp := realSpace(t)
	propose := newTaskPropose(workerMC(sp, "worker-a"))
	accept := newProposalAccept(leaderMC(sp))

	// No suggestion + no override → correctable error.
	if r := exec(t, propose, `{"title":"unowned","spec":"s"}`); r.IsError {
		t.Fatalf("propose: %s", r.Content)
	}
	if r := exec(t, accept, `{"proposal_id":1}`); !r.IsError || !strings.Contains(r.Content, "pass one") {
		t.Fatalf("accept without any assignee should ask for one: %+v", r)
	}
	// Override wins over the (absent) suggestion; unknown override rejected.
	if r := exec(t, accept, `{"proposal_id":1,"assignee":"ghost"}`); !r.IsError {
		t.Error("unknown assignee should be rejected")
	}
	if r := exec(t, accept, `{"proposal_id":1,"assignee":"worker-b"}`); r.IsError {
		t.Fatalf("accept with override: %s", r.Content)
	}
	task, _ := sp.Store.GetTask(1)
	if task.Assignee != "worker-b" {
		t.Fatalf("assignee = %q, want the override", task.Assignee)
	}
}

func TestProposalDeclineClosesTheLoop(t *testing.T) {
	sp := realSpace(t)
	propose := newTaskPropose(workerMC(sp, "worker-a"))
	decline := newProposalDecline(leaderMC(sp))

	if r := exec(t, propose, `{"title":"rabbit hole","spec":"deep dive"}`); r.IsError {
		t.Fatalf("propose: %s", r.Content)
	}
	if r := exec(t, decline, `{"proposal_id":1}`); !r.IsError {
		t.Error("decline without a note must be rejected (RP-12 closure as schema)")
	}
	if r := exec(t, decline, `{"proposal_id":1,"note":"out of scope this sprint"}`); r.IsError {
		t.Fatalf("decline: %s", r.Content)
	}

	p, _ := sp.Store.GetProposal(1)
	if p.Status != store.ProposalDeclined {
		t.Fatalf("proposal = %+v, want declined", p)
	}
	mail, _ := sp.Store.UnreadFor("worker-a")
	if len(mail) != 1 {
		t.Fatalf("proposer unread = %d, want the declined notice", len(mail))
	}
	m, _ := sp.Store.GetMessage(mail[0])
	if !strings.Contains(m.Body, "out of scope this sprint") {
		t.Errorf("declined notice should carry the why:\n%s", m.Body)
	}
	// No task was created.
	if tasks, _ := sp.Store.ListTasks(store.TaskFilter{}); len(tasks) != 0 {
		t.Fatalf("decline created %d tasks, want 0", len(tasks))
	}
}

func TestProposalListAndTaskListTail(t *testing.T) {
	sp := realSpace(t)
	propose := newTaskPropose(workerMC(sp, "worker-a"))
	if r := exec(t, propose, `{"title":"first","spec":"a","suggested_assignee":"worker-b"}`); r.IsError {
		t.Fatalf("propose: %s", r.Content)
	}
	if r := exec(t, propose, `{"title":"second","spec":"b"}`); r.IsError {
		t.Fatalf("propose: %s", r.Content)
	}

	res := exec(t, newProposalList(leaderMC(sp)), `{}`)
	if res.IsError {
		t.Fatalf("proposal_list: %s", res.Content)
	}
	for _, frag := range []string{"Open proposals (2)", "#1 [worker-a] first (suggests: worker-b)", "#2 [worker-a] second"} {
		if !strings.Contains(res.Content, frag) {
			t.Errorf("proposal_list missing %q:\n%s", frag, res.Content)
		}
	}

	// task_list points the leader at the waiting inbox.
	tl := exec(t, newTaskList(leaderMC(sp)), `{}`)
	if !strings.Contains(tl.Content, "Open proposals: 2") || !strings.Contains(tl.Content, "proposal_list") {
		t.Errorf("task_list tail missing the open-proposal pointer:\n%s", tl.Content)
	}
}
