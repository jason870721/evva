package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Status is one of the 5 task states.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSuspended Status = "suspended"
	StatusVerifying Status = "verifying"
	StatusCompleted Status = "completed"
)

// Role distinguishes the Leader (the only actor allowed to write task status)
// from Workers (read-only on the ledger).
type Role string

const (
	RoleLeader Role = "leader"
	RoleWorker Role = "worker"
)

// Actor is who is performing a write — a name plus a role. Task status writes
// require RoleLeader.
type Actor struct {
	Name string
	Role Role
}

// Task is one ledger row. Times are unix millis. ParentID is nil for a
// top-level task.
type Task struct {
	ID         int64
	Title      string
	Spec       string
	Status     Status
	Assignee   string
	CreatedBy  string
	Result     string
	VerifyNote string
	ParentID   *int64
	CreatedAt  int64
	UpdatedAt  int64
}

// TaskFilter narrows ListTasks (and CountTasks). Zero-value fields are
// wildcards, so TaskFilter{} keeps the original "all tasks, oldest-first"
// behavior. Limit/Offset/Newest page and order the result; CountTasks ignores
// them and reports the full match total (RP-6 — completed is monotonic, so
// every read must be bounded).
type TaskFilter struct {
	Status   Status   // "" = any
	Assignee string   // "" = any
	Statuses []Status // non-empty = status IN (...); takes precedence over Status (the board's active-set query)
	Limit    int      // 0 = no LIMIT (caller applies its own default); >0 = LIMIT ? OFFSET ?
	Offset   int      // pagination offset; only applied when Limit > 0
	Newest   bool     // true = ORDER BY id DESC (most-recent first, for completed history)
}

// Sentinel errors (test with errors.Is).
var (
	ErrEmptyAssignee     = errors.New("store: task requires a non-empty assignee")
	ErrNotLeader         = errors.New("store: task status writes are leader-only")
	ErrIllegalTransition = errors.New("store: illegal task status transition")
	ErrTaskNotFound      = errors.New("store: task not found")
)

// legalTransitions is the authoritative 5-state machine (design §7.1,
// SPRD-1-2 §4). Anything not listed — including self-transitions and any move
// out of the terminal `completed` — is illegal.
var legalTransitions = map[Status]map[Status]bool{
	StatusPending:   {StatusRunning: true},
	StatusRunning:   {StatusSuspended: true, StatusVerifying: true},
	StatusSuspended: {StatusRunning: true},
	StatusVerifying: {StatusCompleted: true, StatusRunning: true},
	StatusCompleted: {},
}

const taskCols = `id, title, spec, status, assignee, created_by, result, verify_note, parent_id, created_at, updated_at`

// CreateTask inserts a new task in the `pending` state. Push model: a non-empty
// Assignee is required. The caller-supplied Status is ignored (always pending).
func (s *Store) CreateTask(t Task) (int64, error) {
	if strings.TrimSpace(t.Assignee) == "" {
		return 0, ErrEmptyAssignee
	}
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`INSERT INTO tasks (title, spec, status, assignee, created_by, result, verify_note, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Title, t.Spec, string(StatusPending), t.Assignee, t.CreatedBy,
		nullableStr(t.Result), nullableStr(t.VerifyNote), nullableInt(t.ParentID), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("store: create task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create task: last insert id: %w", err)
	}
	slog.Info("swarm task created", "id", id, "title", t.Title, "assignee", t.Assignee, "by", t.CreatedBy)
	return id, nil
}

// TransitionTask moves a task to `to`, enforcing the state machine. It is
// Leader-only: a non-leader Actor is rejected before any read or write. An
// illegal transition returns ErrIllegalTransition and does not mutate the row.
// A non-empty note is written to verify_note.
func (s *Store) TransitionTask(id int64, to Status, by Actor, note string) error {
	if by.Role != RoleLeader {
		return ErrNotLeader
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var fromStr string
	err := s.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, id).Scan(&fromStr)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("store: read task %d: %w", id, err)
	}
	from := Status(fromStr)

	if !legalTransitions[from][to] {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
	}

	now := time.Now().UnixMilli()
	if note != "" {
		_, err = s.db.Exec(`UPDATE tasks SET status = ?, verify_note = ?, updated_at = ? WHERE id = ?`,
			string(to), note, now, id)
	} else {
		_, err = s.db.Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
			string(to), now, id)
	}
	if err != nil {
		return fmt.Errorf("store: transition task %d (%s -> %s): %w", id, from, to, err)
	}
	slog.Info("swarm task transition", "id", id, "from", from, "to", to, "by", by.Name)
	return nil
}

// GetTask returns one task by id, or ErrTaskNotFound.
func (s *Store) GetTask(id int64) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, err := scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrTaskNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("store: get task %d: %w", id, err)
	}
	return t, nil
}

// taskWhere builds the WHERE clause (leading " WHERE ", or "") and its args,
// shared by ListTasks and CountTasks so the two can never disagree about which
// rows match. A non-empty Statuses list takes precedence over the single Status.
func taskWhere(f TaskFilter) (string, []any) {
	var where []string
	var args []any
	switch {
	case len(f.Statuses) > 0:
		ph := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			ph[i] = "?"
			args = append(args, string(s))
		}
		where = append(where, "status IN ("+strings.Join(ph, ",")+")")
	case f.Status != "":
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Assignee != "" {
		where = append(where, "assignee = ?")
		args = append(args, f.Assignee)
	}
	if len(where) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// ListTasks returns tasks matching the filter. Oldest-first by default; Newest
// flips to most-recent-first. Limit > 0 applies LIMIT/OFFSET for paging (Offset
// alone, without Limit, is ignored — SQLite needs a LIMIT for OFFSET).
func (s *Store) ListTasks(f TaskFilter) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clause, args := taskWhere(f)
	q := `SELECT ` + taskCols + ` FROM tasks` + clause
	if f.Newest {
		q += " ORDER BY id DESC"
	} else {
		q += " ORDER BY id"
	}
	if f.Limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, f.Limit, f.Offset)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list tasks: %w", err)
	}
	defer rows.Close()

	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountTasks reports how many tasks match the filter's WHERE clause. Limit /
// Offset / Newest are ignored — it always returns the full total, so a paged
// caller can render "showing N of TOTAL".
func (s *Store) CountTasks(f TaskFilter) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clause, args := taskWhere(f)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`+clause, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count tasks: %w", err)
	}
	return n, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(sc rowScanner) (Task, error) {
	var (
		t          Task
		statusStr  string
		result     sql.NullString
		verifyNote sql.NullString
		parentID   sql.NullInt64
	)
	if err := sc.Scan(&t.ID, &t.Title, &t.Spec, &statusStr, &t.Assignee, &t.CreatedBy,
		&result, &verifyNote, &parentID, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Task{}, err
	}
	t.Status = Status(statusStr)
	t.Result = result.String
	t.VerifyNote = verifyNote.String
	if parentID.Valid {
		v := parentID.Int64
		t.ParentID = &v
	}
	return t, nil
}
