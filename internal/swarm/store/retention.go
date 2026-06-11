package store

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// archiveDirName is where vacuumed rows land, next to the db:
// <workdir>/.vero/archive/YYYY-MM.jsonl.gz (RP-16).
const archiveDirName = "archive"

// ArchiveRecord is one line of an archive file. Exactly one of Message/Task is
// set, per Kind. ArchivedAt is unix millis (when the vacuum pass ran).
type ArchiveRecord struct {
	Kind       string    `json:"kind"` // "message" | "task" | "proposal"
	ArchivedAt int64     `json:"archived_at"`
	Message    *Message  `json:"message,omitempty"`
	Task       *Task     `json:"task,omitempty"`
	Proposal   *Proposal `json:"proposal,omitempty"`
}

// VacuumStats reports one retention pass. A dry run fills the counts and
// leaves Files empty.
type VacuumStats struct {
	Messages  int      // message rows archived + deleted
	Tasks     int      // completed-task rows archived + deleted
	Proposals int      // decided-proposal rows archived + deleted (RP-23)
	Files     []string // archive files appended this pass
}

// Vacuum archives-then-deletes consumed history older than cutoff (RP-16).
//
// Eligible — and ONLY these:
//   - messages already read (read_at non-NULL) with read_at <= cutoff. The
//     clock is the READ time, so nothing disappears until retention_days after
//     it was consumed. Unread and claimed (in-flight) mail is untouchable.
//   - tasks in the terminal `completed` state with updated_at (the completion
//     stamp) <= cutoff — and not referenced by anything that survives: a
//     surviving message's ref_task or a surviving task's parent_id pins its
//     target (transitively), since both carry FOREIGN KEYs.
//
// Rows are appended to <.vero>/archive/YYYY-MM.jsonl.gz (bucketed by the row's
// own created_at month, local time per the pkg/common zone discipline) BEFORE
// the delete; an archive write failure aborts the pass with nothing deleted. A
// re-run after a failed delete may append the same rows again — the archive is
// an append-only log, duplicates are harmless. dryRun computes the identical
// row set and counts without writing or deleting anything.
//
// The whole pass holds the store's write lock, so eligibility cannot shift
// between the scan and the delete. Do not call other Store methods from here —
// the mutex is not reentrant.
func (s *Store) Vacuum(cutoff time.Time, dryRun bool) (VacuumStats, error) {
	cut := cutoff.UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	msgs, err := s.vacuumMessagesLocked(cut)
	if err != nil {
		return VacuumStats{}, err
	}
	tasks, err := s.vacuumTasksLocked(cut)
	if err != nil {
		return VacuumStats{}, err
	}
	props, err := s.vacuumProposalsLocked(cut)
	if err != nil {
		return VacuumStats{}, err
	}
	stats := VacuumStats{Messages: len(msgs), Tasks: len(tasks), Proposals: len(props)}
	if dryRun || len(msgs)+len(tasks)+len(props) == 0 {
		return stats, nil
	}

	stats.Files, err = s.appendArchive(msgs, tasks, props)
	if err != nil {
		return VacuumStats{}, fmt.Errorf("store: vacuum archive: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return VacuumStats{}, fmt.Errorf("store: vacuum begin: %w", err)
	}
	// Deleted parents and children land in the same pass; deferring FK checks
	// to commit frees us from ordering the deletes by tree depth.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		_ = tx.Rollback()
		return VacuumStats{}, fmt.Errorf("store: vacuum defer fks: %w", err)
	}
	for _, m := range msgs {
		if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, m.ID); err != nil {
			_ = tx.Rollback()
			return VacuumStats{}, fmt.Errorf("store: vacuum delete message %s: %w", m.ID, err)
		}
	}
	for _, t := range tasks {
		if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, t.ID); err != nil {
			_ = tx.Rollback()
			return VacuumStats{}, fmt.Errorf("store: vacuum delete task %d: %w", t.ID, err)
		}
	}
	for _, p := range props {
		if _, err := tx.Exec(`DELETE FROM proposals WHERE id = ?`, p.ID); err != nil {
			_ = tx.Rollback()
			return VacuumStats{}, fmt.Errorf("store: vacuum delete proposal %d: %w", p.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return VacuumStats{}, fmt.Errorf("store: vacuum commit: %w", err)
	}

	// Give the space back to the OS. Best-effort: the deletes above are already
	// durable, a failed shrink only costs disk.
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		slog.Warn("swarm store: vacuum checkpoint failed", "err", err)
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		slog.Warn("swarm store: sqlite VACUUM failed", "err", err)
	}
	slog.Info("swarm store vacuumed", "messages", stats.Messages, "tasks", stats.Tasks, "proposals", stats.Proposals, "files", stats.Files)
	return stats, nil
}

// vacuumProposalsLocked returns every DECIDED proposal whose decision is old
// enough to clear (RP-23 riding the RP-16 window). Open proposals are
// untouchable regardless of age — undecided work must stay on the board.
// ref_task is a plain audit pointer (no FK), so proposals never pin tasks
// and need no fixpoint pass. Caller holds s.mu.
func (s *Store) vacuumProposalsLocked(cut int64) ([]Proposal, error) {
	rows, err := s.db.Query(
		`SELECT `+proposalCols+` FROM proposals
		 WHERE status != ? AND decided_at IS NOT NULL AND decided_at <= ? ORDER BY id`,
		string(ProposalOpen), cut)
	if err != nil {
		return nil, fmt.Errorf("store: vacuum scan proposals: %w", err)
	}
	defer rows.Close()
	out := make([]Proposal, 0)
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("store: vacuum scan proposal: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// vacuumMessagesLocked returns the full rows of every archivable message:
// read, and read at or before cut. Caller holds s.mu.
func (s *Store) vacuumMessagesLocked(cut int64) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT `+msgCols+` FROM messages WHERE read_at IS NOT NULL AND read_at <= ? ORDER BY created_at, id`, cut)
	if err != nil {
		return nil, fmt.Errorf("store: vacuum scan messages: %w", err)
	}
	defer rows.Close()
	out := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: vacuum scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// vacuumTasksLocked returns every completed task old enough to clear AND not
// pinned by a survivor. Pinning is transitive (a surviving child pins its
// completed parent, whose own parent is then pinned too), so candidates are
// pruned to a fixpoint. Caller holds s.mu.
func (s *Store) vacuumTasksLocked(cut int64) ([]Task, error) {
	rows, err := s.db.Query(
		`SELECT `+taskCols+` FROM tasks WHERE status = ? AND updated_at <= ? ORDER BY id`,
		string(StatusCompleted), cut)
	if err != nil {
		return nil, fmt.Errorf("store: vacuum scan tasks: %w", err)
	}
	cands := make(map[int64]Task)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: vacuum scan task: %w", err)
		}
		cands[t.ID] = t
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(cands) == 0 {
		return []Task{}, nil
	}

	// Pin pass 1: tasks referenced by messages that will SURVIVE this vacuum.
	refRows, err := s.db.Query(
		`SELECT DISTINCT ref_task FROM messages
		 WHERE ref_task IS NOT NULL AND NOT (read_at IS NOT NULL AND read_at <= ?)`, cut)
	if err != nil {
		return nil, fmt.Errorf("store: vacuum scan message refs: %w", err)
	}
	for refRows.Next() {
		var id int64
		if err := refRows.Scan(&id); err != nil {
			refRows.Close()
			return nil, fmt.Errorf("store: vacuum scan message ref: %w", err)
		}
		delete(cands, id)
	}
	if err := refRows.Err(); err != nil {
		refRows.Close()
		return nil, err
	}
	refRows.Close()

	// Pin pass 2 (fixpoint): a parent survives while any of its children does.
	type edge struct{ child, parent int64 }
	edgeRows, err := s.db.Query(`SELECT id, parent_id FROM tasks WHERE parent_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: vacuum scan parent edges: %w", err)
	}
	var edges []edge
	for edgeRows.Next() {
		var e edge
		if err := edgeRows.Scan(&e.child, &e.parent); err != nil {
			edgeRows.Close()
			return nil, fmt.Errorf("store: vacuum scan parent edge: %w", err)
		}
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		edgeRows.Close()
		return nil, err
	}
	edgeRows.Close()
	for changed := true; changed; {
		changed = false
		for _, e := range edges {
			if _, parentGoing := cands[e.parent]; !parentGoing {
				continue
			}
			if _, childGoing := cands[e.child]; !childGoing {
				delete(cands, e.parent) // child survives → parent must too
				changed = true
			}
		}
	}

	out := make([]Task, 0, len(cands))
	for _, t := range cands {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// appendArchive writes the doomed rows to their month files (by each row's
// created_at, local zone) and returns the file paths touched. Each call closes
// its gzip writer per file, appending one self-contained gzip member —
// concatenated members are what gzip readers (incl. ReadArchive and zcat)
// expect, so the file stays readable across any number of vacuum passes.
func (s *Store) appendArchive(msgs []Message, tasks []Task, props []Proposal) ([]string, error) {
	dir := filepath.Join(s.dir, archiveDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()

	byMonth := make(map[string][]ArchiveRecord)
	for i := range msgs {
		k := monthKey(msgs[i].CreatedAt)
		byMonth[k] = append(byMonth[k], ArchiveRecord{Kind: "message", ArchivedAt: now, Message: &msgs[i]})
	}
	for i := range tasks {
		k := monthKey(tasks[i].CreatedAt)
		byMonth[k] = append(byMonth[k], ArchiveRecord{Kind: "task", ArchivedAt: now, Task: &tasks[i]})
	}
	for i := range props {
		k := monthKey(props[i].CreatedAt)
		byMonth[k] = append(byMonth[k], ArchiveRecord{Kind: "proposal", ArchivedAt: now, Proposal: &props[i]})
	}

	months := make([]string, 0, len(byMonth))
	for k := range byMonth {
		months = append(months, k)
	}
	sort.Strings(months)

	files := make([]string, 0, len(months))
	for _, k := range months {
		path := filepath.Join(dir, k+".jsonl.gz")
		if err := appendArchiveFile(path, byMonth[k]); err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	return files, nil
}

func appendArchiveFile(path string, recs []ArchiveRecord) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	enc := json.NewEncoder(zw)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			_ = zw.Close()
			_ = f.Close()
			return fmt.Errorf("encode archive record: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("close archive gzip member: %w", err)
	}
	return f.Close()
}

// monthKey buckets a unix-millis stamp into its local-time month ("2026-06").
// Local on purpose: the operator's calendar, consistent with the rest of the
// swarm's wall-clock semantics (pkg/common).
func monthKey(unixMilli int64) string {
	return time.UnixMilli(unixMilli).Local().Format("2006-01")
}

// ReadArchive decodes every record of one archive file, across all of its
// appended gzip members. The tooling/test seam proving archives stay readable.
func ReadArchive(path string) ([]ArchiveRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f) // multistream by default: reads concatenated members
	if err != nil {
		return nil, fmt.Errorf("store: open archive %s: %w", path, err)
	}
	defer zr.Close()

	dec := json.NewDecoder(zr)
	out := make([]ArchiveRecord, 0)
	for {
		var r ArchiveRecord
		if err := dec.Decode(&r); errors.Is(err, io.EOF) {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("store: decode archive %s: %w", path, err)
		}
		out = append(out, r)
	}
}
