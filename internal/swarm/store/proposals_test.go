package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// RP-23: the proposal inlet — workers file, only the leader decides, and an
// accepted proposal becomes a running task in one atomic transaction. The
// task ledger's single-writer invariant is never pierced.

func leaderA() Actor { return Actor{Name: "leader", Role: RoleLeader} }

func TestProposalCreateAndList(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.CreateProposal(Proposal{Title: "no proposer"}); !errors.Is(err, ErrIncompleteProposal) {
		t.Fatalf("err = %v, want ErrIncompleteProposal", err)
	}

	id, err := s.CreateProposal(Proposal{Proposer: "risk-monitor", Title: "add ETH stop-loss", Spec: "naked position", SuggestedAssignee: "trader"})
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	p, err := s.GetProposal(id)
	if err != nil || p.Status != ProposalOpen || p.Proposer != "risk-monitor" || p.SuggestedAssignee != "trader" {
		t.Fatalf("proposal = %+v err=%v, want open/risk-monitor/trader", p, err)
	}
	open, err := s.ListProposals(ProposalOpen)
	if err != nil || len(open) != 1 {
		t.Fatalf("open list = %v err=%v, want 1", open, err)
	}
	if n, _ := s.CountProposals(ProposalOpen); n != 1 {
		t.Fatalf("open count = %d, want 1", n)
	}
	if _, err := s.GetProposal(999); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("missing id err = %v, want ErrProposalNotFound", err)
	}
}

func TestAcceptProposalAtomically(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateProposal(Proposal{Proposer: "w", Title: "fix the leak", Spec: "valve 3"})

	if _, err := s.AcceptProposal(id, Actor{Name: "w", Role: RoleWorker}, "trader"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("worker accept err = %v, want ErrNotLeader (single-writer invariant)", err)
	}
	if _, err := s.AcceptProposal(id, leaderA(), " "); !errors.Is(err, ErrEmptyAssignee) {
		t.Fatalf("empty assignee err = %v, want ErrEmptyAssignee", err)
	}

	task, err := s.AcceptProposal(id, leaderA(), "trader")
	if err != nil {
		t.Fatalf("AcceptProposal: %v", err)
	}
	// The task exists in the ledger, already running, carrying the proposal's content.
	got, err := s.GetTask(task.ID)
	if err != nil || got.Status != StatusRunning || got.Title != "fix the leak" || got.Assignee != "trader" || got.CreatedBy != "leader" {
		t.Fatalf("task = %+v err=%v, want running/trader from the proposal", got, err)
	}
	// The proposal is accepted with the back-reference filled.
	p, _ := s.GetProposal(id)
	if p.Status != ProposalAccepted || p.DecidedBy != "leader" || p.RefTask == nil || *p.RefTask != task.ID || p.DecidedAt == nil {
		t.Fatalf("proposal after accept = %+v, want accepted with ref_task=%d", p, task.ID)
	}

	// Terminal: a second decision of any kind loses.
	if _, err := s.AcceptProposal(id, leaderA(), "trader"); !errors.Is(err, ErrProposalDecided) {
		t.Fatalf("re-accept err = %v, want ErrProposalDecided", err)
	}
	if err := s.DeclineProposal(id, leaderA(), "nope"); !errors.Is(err, ErrProposalDecided) {
		t.Fatalf("decline-after-accept err = %v, want ErrProposalDecided", err)
	}
}

func TestDeclineProposalNeedsNote(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateProposal(Proposal{Proposer: "w", Title: "rabbit hole"})

	if err := s.DeclineProposal(id, leaderA(), "  "); !errors.Is(err, ErrDeclineNeedsNote) {
		t.Fatalf("noteless decline err = %v, want ErrDeclineNeedsNote", err)
	}
	if err := s.DeclineProposal(id, Actor{Name: "w", Role: RoleWorker}, "self-decline"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("worker decline err = %v, want ErrNotLeader", err)
	}
	if err := s.DeclineProposal(id, leaderA(), "out of scope this sprint"); err != nil {
		t.Fatalf("DeclineProposal: %v", err)
	}
	p, _ := s.GetProposal(id)
	if p.Status != ProposalDeclined || p.DecideNote != "out of scope this sprint" || p.RefTask != nil {
		t.Fatalf("proposal after decline = %+v", p)
	}
	if err := s.DeclineProposal(999, leaderA(), "n"); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("missing id err = %v, want ErrProposalNotFound", err)
	}
}

// Concurrent decisions: exactly one wins, exactly one task is created.
func TestProposalDecisionRace(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateProposal(Proposal{Proposer: "w", Title: "raced"})

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = s.AcceptProposal(id, leaderA(), "trader")
			} else {
				errs[i] = s.DeclineProposal(id, leaderA(), "racing decline")
			}
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrProposalDecided) {
			t.Errorf("unexpected race error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("decision wins = %d, want exactly 1", wins)
	}
	tasks, _ := s.ListTasks(TaskFilter{})
	p, _ := s.GetProposal(id)
	if p.Status == ProposalAccepted && len(tasks) != 1 {
		t.Fatalf("accepted with %d tasks, want exactly 1", len(tasks))
	}
	if p.Status == ProposalDeclined && len(tasks) != 0 {
		t.Fatalf("declined but %d tasks exist", len(tasks))
	}
}

// RP-16 ride-along: decided proposals age out through the vacuum; open ones
// are untouchable regardless of age.
func TestVacuumArchivesDecidedProposals(t *testing.T) {
	s := openTestStore(t)
	oldStamp := time.Now().AddDate(0, 0, -60).UnixMilli()

	accepted, _ := s.CreateProposal(Proposal{Proposer: "w", Title: "old accepted", CreatedAt: oldStamp})
	if _, err := s.AcceptProposal(accepted, leaderA(), "trader"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	declined, _ := s.CreateProposal(Proposal{Proposer: "w", Title: "old declined", CreatedAt: oldStamp})
	if err := s.DeclineProposal(declined, leaderA(), "no"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	stillOpen, _ := s.CreateProposal(Proposal{Proposer: "w", Title: "ancient but open", CreatedAt: oldStamp})

	// Decisions were stamped NOW, so a cutoff in the future captures them.
	stats, err := s.Vacuum(time.Now().Add(time.Hour), false)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if stats.Proposals != 2 {
		t.Fatalf("vacuumed proposals = %d, want 2 (both decided)", stats.Proposals)
	}
	if _, err := s.GetProposal(stillOpen); err != nil {
		t.Fatalf("open proposal must survive any cutoff: %v", err)
	}
	if _, err := s.GetProposal(declined); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("declined proposal should be gone, err = %v", err)
	}
	// And the archive holds them.
	if len(stats.Files) == 0 {
		t.Fatal("no archive files written")
	}
	recs, err := ReadArchive(stats.Files[0])
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	found := 0
	for _, r := range recs {
		if r.Kind == "proposal" && r.Proposal != nil {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("archived proposal records = %d, want 2", found)
	}
}
