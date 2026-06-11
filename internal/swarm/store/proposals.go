package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// proposals.go is the RP-23 bottom-up work inlet. Workers file proposals;
// only the leader decides them — and an accepted proposal becomes a real
// task atomically, in one transaction, so the task ledger's single-writer
// invariant is never pierced: workers still have NO write path into tasks.

// ProposalStatus is the three-state, terminal lifecycle of a proposal.
// There is no reopen — re-raising means a new proposal, preserving history.
type ProposalStatus string

const (
	ProposalOpen     ProposalStatus = "open"
	ProposalAccepted ProposalStatus = "accepted"
	ProposalDeclined ProposalStatus = "declined"
)

// Proposal is one row of the bottom-up inlet. Times are unix millis;
// DecidedAt/RefTask are nil while open (RefTask stays nil on a decline).
type Proposal struct {
	ID                int64
	Proposer          string
	Title             string
	Spec              string
	SuggestedAssignee string
	Status            ProposalStatus
	DecidedBy         string
	DecideNote        string
	RefTask           *int64
	CreatedAt         int64
	DecidedAt         *int64
}

// Proposal sentinel errors (test with errors.Is).
var (
	ErrIncompleteProposal = errors.New("store: proposal requires a proposer and a title")
	ErrProposalNotFound   = errors.New("store: proposal not found")
	ErrProposalDecided    = errors.New("store: proposal already decided")
	ErrDeclineNeedsNote   = errors.New("store: declining a proposal requires a note")
)

const proposalCols = `id, proposer, title, spec, suggested_assignee, status, decided_by, decide_note, ref_task, created_at, decided_at`

// CreateProposal files a new open proposal. Any member may call it — filing
// work is exactly what this table exists to open up.
func (s *Store) CreateProposal(p Proposal) (int64, error) {
	if strings.TrimSpace(p.Proposer) == "" || strings.TrimSpace(p.Title) == "" {
		return 0, ErrIncompleteProposal
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().UnixMilli()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`INSERT INTO proposals (proposer, title, spec, suggested_assignee, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		p.Proposer, p.Title, p.Spec, p.SuggestedAssignee, string(ProposalOpen), p.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("store: create proposal: %w", err)
	}
	return res.LastInsertId()
}

// GetProposal returns one proposal by id, or ErrProposalNotFound.
func (s *Store) GetProposal(id int64) (Proposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getProposalLocked(id)
}

func (s *Store) getProposalLocked(id int64) (Proposal, error) {
	p, err := scanProposal(s.db.QueryRow(`SELECT `+proposalCols+` FROM proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, ErrProposalNotFound
	}
	if err != nil {
		return Proposal{}, fmt.Errorf("store: get proposal %d: %w", id, err)
	}
	return p, nil
}

// ListProposals returns proposals filtered by status ("" = all), oldest
// first — the leader reviews in arrival order.
func (s *Store) ListProposals(status ProposalStatus) ([]Proposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := `SELECT ` + proposalCols + ` FROM proposals ORDER BY id`
	args := []any{}
	if status != "" {
		q = `SELECT ` + proposalCols + ` FROM proposals WHERE status = ? ORDER BY id`
		args = append(args, string(status))
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list proposals: %w", err)
	}
	defer rows.Close()

	out := make([]Proposal, 0)
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan proposal: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountProposals counts proposals in a status ("" = all).
func (s *Store) CountProposals(status ProposalStatus) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q, args := `SELECT COUNT(*) FROM proposals`, []any{}
	if status != "" {
		q, args = q+` WHERE status = ?`, append(args, string(status))
	}
	var n int
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count proposals: %w", err)
	}
	return n, nil
}

// AcceptProposal atomically turns an open proposal into a running task: one
// transaction claims the proposal (open → accepted; a concurrent decision
// loses with ErrProposalDecided), inserts the task directly in `running`
// (create+assign collapsed — the same legal pending→running path, in one
// step), and backfills ref_task. Leader-only, like every task-ledger write;
// assignee must be non-empty (the caller resolves suggested_assignee).
// Returns the new task so the caller can notify proposer and assignee.
func (s *Store) AcceptProposal(id int64, by Actor, assignee string) (Task, error) {
	if by.Role != RoleLeader {
		return Task{}, ErrNotLeader
	}
	if strings.TrimSpace(assignee) == "" {
		return Task{}, ErrEmptyAssignee
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.getProposalLocked(id)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UnixMilli()

	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, fmt.Errorf("store: accept proposal begin: %w", err)
	}
	res, err := tx.Exec(
		`UPDATE proposals SET status = ?, decided_by = ?, decided_at = ? WHERE id = ? AND status = ?`,
		string(ProposalAccepted), by.Name, now, id, string(ProposalOpen),
	)
	if err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("store: accept proposal %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_ = tx.Rollback()
		return Task{}, ErrProposalDecided
	}
	taskRes, err := tx.Exec(
		`INSERT INTO tasks (title, spec, status, assignee, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Title, p.Spec, string(StatusRunning), assignee, by.Name, now, now,
	)
	if err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("store: accept proposal %d create task: %w", id, err)
	}
	taskID, err := taskRes.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("store: accept proposal %d task id: %w", id, err)
	}
	if _, err := tx.Exec(`UPDATE proposals SET ref_task = ? WHERE id = ?`, taskID, id); err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("store: accept proposal %d backfill: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("store: accept proposal commit: %w", err)
	}

	return Task{
		ID: taskID, Title: p.Title, Spec: p.Spec, Status: StatusRunning,
		Assignee: assignee, CreatedBy: by.Name, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// DeclineProposal closes an open proposal with a mandatory note — the RP-12
// closure discipline enforced at the schema, not just in the prompt. A
// concurrent decision loses with ErrProposalDecided.
func (s *Store) DeclineProposal(id int64, by Actor, note string) error {
	if by.Role != RoleLeader {
		return ErrNotLeader
	}
	if strings.TrimSpace(note) == "" {
		return ErrDeclineNeedsNote
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`UPDATE proposals SET status = ?, decided_by = ?, decide_note = ?, decided_at = ? WHERE id = ? AND status = ?`,
		string(ProposalDeclined), by.Name, note, time.Now().UnixMilli(), id, string(ProposalOpen),
	)
	if err != nil {
		return fmt.Errorf("store: decline proposal %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.getProposalLocked(id); errors.Is(err, ErrProposalNotFound) {
			return ErrProposalNotFound
		}
		return ErrProposalDecided
	}
	return nil
}

// scanProposal reads one proposal row from a *sql.Row or *sql.Rows.
func scanProposal(sc rowScanner) (Proposal, error) {
	var (
		p         Proposal
		status    string
		refTask   sql.NullInt64
		decidedAt sql.NullInt64
	)
	if err := sc.Scan(&p.ID, &p.Proposer, &p.Title, &p.Spec, &p.SuggestedAssignee,
		&status, &p.DecidedBy, &p.DecideNote, &refTask, &p.CreatedAt, &decidedAt); err != nil {
		return Proposal{}, err
	}
	p.Status = ProposalStatus(status)
	if refTask.Valid {
		v := refTask.Int64
		p.RefTask = &v
	}
	if decidedAt.Valid {
		v := decidedAt.Int64
		p.DecidedAt = &v
	}
	return p, nil
}
