package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Status is one of the 6 task states. `blocked` is birth-only: a task created
// with incomplete dependencies starts there and leaves via exactly one edge
// (blocked -> pending); nothing ever transitions INTO blocked.
type Status string

const (
	StatusPending   Status = "pending"
	StatusBlocked   Status = "blocked"
	StatusRunning   Status = "running"
	StatusSuspended Status = "suspended"
	StatusVerifying Status = "verifying"
	StatusCompleted Status = "completed"
)

// Role distinguishes the writers of the ledger. The Leader holds every edge
// (all judgment flows through it); a Worker holds exactly one — reporting its
// OWN task done (running -> verifying); System is the dispatch engine, whose
// permitted edges are mechanical executions of structure the leader already
// declared at create time (DWF §4 writer matrix). System never decides.
type Role string

const (
	RoleLeader Role = "leader"
	RoleWorker Role = "worker"
	RoleSystem Role = "system"
)

// Verify policies: who settles `verifying`. VerifyLeader is the unchanged
// human-judgment flow; VerifyAuto lets the system actor complete the task on
// the worker's task_done, so declared-mechanical chains flow with zero leader
// wakes. Free-form TEXT in the schema so a future machine-evidence wave can
// add "checks" without a migration.
const (
	VerifyLeader = "leader"
	VerifyAuto   = "auto"
)

// Actor is who is performing a write — a name plus a role. Which (from, to)
// edges each role may write is the allowedWriter matrix below.
type Actor struct {
	Name string
	Role Role
}

// Task is one ledger row. Times are unix millis. ParentID is nil for a
// top-level task. DependsOn is the task's dependency edges, set once at
// creation and immutable after (acyclic by construction — an edge can only
// point at an already-existing id).
type Task struct {
	ID           int64
	Title        string
	Spec         string
	Status       Status
	Assignee     string
	CreatedBy    string
	Result       string
	VerifyNote   string
	ParentID     *int64
	VerifyPolicy string // VerifyLeader (default) | VerifyAuto
	DependsOn    []int64
	CreatedAt    int64
	UpdatedAt    int64
}

// EngineManaged reports whether the engine (system actor) owns this task's
// dispatch: one rule for the whole subsystem — has dependencies = engine
// dispatches it; depless = leader-managed exactly as before DWF.
func (t Task) EngineManaged() bool { return len(t.DependsOn) > 0 }

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

// Sentinel errors (test with errors.Is). ErrNotLeader predates the DWF writer
// matrix; it now means "this actor may not write this edge" (the leader still
// holds every edge, so the name stays truthful for the common rejection).
var (
	ErrEmptyAssignee     = errors.New("store: task requires a non-empty assignee")
	ErrNotLeader         = errors.New("store: task transition not permitted for this actor")
	ErrIllegalTransition = errors.New("store: illegal task status transition")
	ErrTaskNotFound      = errors.New("store: task not found")
	ErrDepNotFound       = errors.New("store: dependency task not found")
	ErrBadVerifyPolicy   = errors.New("store: verify policy must be \"leader\" or \"auto\"")
)

// legalTransitions is the authoritative state machine (design §7.1,
// SPRD-1-2 §4; blocked added by DWF-1). Anything not listed — including
// self-transitions, any move INTO blocked, and any move out of the terminal
// `completed` — is illegal.
var legalTransitions = map[Status]map[Status]bool{
	StatusPending:   {StatusRunning: true},
	StatusBlocked:   {StatusPending: true},
	StatusRunning:   {StatusSuspended: true, StatusVerifying: true},
	StatusSuspended: {StatusRunning: true},
	StatusVerifying: {StatusCompleted: true, StatusRunning: true},
	StatusCompleted: {},
}

// allowedWriter is the DWF §4 writer matrix: which actor may write a LEGAL
// (from, to) edge on task t. The leader holds every edge; the two mechanical
// writers hold only executions of leader-declared structure — the worker its
// own done-report, the system dispatch (dep-tasks only) and auto-verify
// settlement (verify_policy 'auto' only). Zero judgment leaves the leader.
func allowedWriter(from, to Status, by Actor, assignee, verifyPolicy string, hasDeps bool) bool {
	switch by.Role {
	case RoleLeader:
		return true
	case RoleWorker:
		return from == StatusRunning && to == StatusVerifying && by.Name == assignee
	case RoleSystem:
		switch {
		case from == StatusBlocked && to == StatusPending:
			return true
		case from == StatusPending && to == StatusRunning:
			return hasDeps
		case from == StatusVerifying && to == StatusCompleted:
			return verifyPolicy == VerifyAuto
		}
	}
	return false
}

const taskCols = `id, title, spec, status, assignee, created_by, result, verify_note, parent_id, verify_policy, created_at, updated_at`

// CreateTask inserts a new task. Push model: a non-empty Assignee is required.
// The caller-supplied Status is ignored — the birth state is computed: blocked
// iff any dependency is incomplete, else pending. Every DependsOn id must
// reference an existing task (ErrDepNotFound otherwise); a dep on an
// already-completed task is satisfied at birth. Task row and dep edges commit
// in one transaction.
func (s *Store) CreateTask(t Task) (int64, error) {
	if strings.TrimSpace(t.Assignee) == "" {
		return 0, ErrEmptyAssignee
	}
	policy := t.VerifyPolicy
	if policy == "" {
		policy = VerifyLeader
	}
	if policy != VerifyLeader && policy != VerifyAuto {
		return 0, fmt.Errorf("%w (got %q)", ErrBadVerifyPolicy, t.VerifyPolicy)
	}
	deps := dedupeIDs(t.DependsOn)
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: create task: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	status := StatusPending
	for _, dep := range deps {
		var depStatus string
		err := tx.QueryRow(`SELECT status FROM tasks WHERE id = ?`, dep).Scan(&depStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: #%d", ErrDepNotFound, dep)
		}
		if err != nil {
			return 0, fmt.Errorf("store: create task: read dep #%d: %w", dep, err)
		}
		if Status(depStatus) != StatusCompleted {
			status = StatusBlocked
		}
	}

	res, err := tx.Exec(
		`INSERT INTO tasks (title, spec, status, assignee, created_by, result, verify_note, parent_id, verify_policy, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Title, t.Spec, string(status), t.Assignee, t.CreatedBy,
		nullableStr(t.Result), nullableStr(t.VerifyNote), nullableInt(t.ParentID), policy, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("store: create task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create task: last insert id: %w", err)
	}
	for _, dep := range deps {
		if _, err := tx.Exec(`INSERT INTO task_deps (task_id, depends_on_id) VALUES (?, ?)`, id, dep); err != nil {
			return 0, fmt.Errorf("store: create task: dep edge #%d->#%d: %w", id, dep, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: create task: commit: %w", err)
	}
	slog.Info("swarm task created", "id", id, "title", t.Title, "assignee", t.Assignee, "by", t.CreatedBy,
		"status", status, "deps", len(deps), "verify", policy)
	return id, nil
}

// dedupeIDs returns ids with duplicates removed, order preserved (the
// task_deps PRIMARY KEY would reject a duplicate edge; a caller repeating an
// id means the same one dependency, not an error).
func dedupeIDs(ids []int64) []int64 {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[int64]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// TransitionTask moves a task to `to`, enforcing the state machine and the
// DWF §4 writer matrix. Legality (the edge exists) is checked before
// authorship (this actor may write it), so a structurally-impossible move is
// ErrIllegalTransition for everyone and a legal edge by the wrong writer is
// ErrNotLeader. Neither mutates the row. A non-empty note is written to
// verify_note.
func (s *Store) TransitionTask(id int64, to Status, by Actor, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		fromStr, assignee, policy string
		depCount                  int
	)
	err := s.db.QueryRow(
		`SELECT status, assignee, verify_policy,
		        (SELECT COUNT(*) FROM task_deps d WHERE d.task_id = tasks.id)
		 FROM tasks WHERE id = ?`, id).Scan(&fromStr, &assignee, &policy, &depCount)
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
	if !allowedWriter(from, to, by, assignee, policy, depCount > 0) {
		return fmt.Errorf("%w: %s -> %s by %s(%s)", ErrNotLeader, from, to, by.Name, by.Role)
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
	slog.Info("swarm task transition", "id", id, "from", from, "to", to, "by", by.Name, "role", by.Role)
	return nil
}

// CompleteWork is the worker's task_done write: one transaction records the
// result and moves the task running -> verifying. The writer matrix applies —
// the task's own assignee (or the leader); the system actor is refused.
func (s *Store) CompleteWork(id int64, by Actor, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var fromStr, assignee, policy string
	err := s.db.QueryRow(`SELECT status, assignee, verify_policy FROM tasks WHERE id = ?`, id).
		Scan(&fromStr, &assignee, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("store: read task %d: %w", id, err)
	}
	from := Status(fromStr)

	if !legalTransitions[from][StatusVerifying] {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, StatusVerifying)
	}
	if !allowedWriter(from, StatusVerifying, by, assignee, policy, false) {
		return fmt.Errorf("%w: task_done on #%d by %s(%s)", ErrNotLeader, id, by.Name, by.Role)
	}

	now := time.Now().UnixMilli()
	if _, err := s.db.Exec(`UPDATE tasks SET status = ?, result = ?, updated_at = ? WHERE id = ?`,
		string(StatusVerifying), result, now, id); err != nil {
		return fmt.Errorf("store: complete work on task %d: %w", id, err)
	}
	slog.Info("swarm task done", "id", id, "by", by.Name)
	return nil
}

// SweepDispatchable finds every engine-managed task stranded short of running
// — blocked with all dependencies complete (a completion landed; the unblock
// may or may not have), or pending with dependencies (crash between unblock
// and dispatch, a leader force-unblock, or born with already-complete deps) —
// marks them running in one transaction, and returns them for mail dispatch.
// Idempotent: the marked tasks leave the query's reach, so a second sweep
// returns nothing. Both the completion hook and the rescan tick call this;
// the DB is the truth, the sweep makes it converge.
func (s *Store) SweepDispatchable() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: sweep dispatchable: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT ` + taskCols + ` FROM tasks t
		WHERE (t.status = 'blocked' AND NOT EXISTS (
		         SELECT 1 FROM task_deps d JOIN tasks p ON p.id = d.depends_on_id
		         WHERE d.task_id = t.id AND p.status != 'completed'))
		   OR (t.status = 'pending' AND EXISTS (
		         SELECT 1 FROM task_deps d WHERE d.task_id = t.id))
		ORDER BY t.id`)
	if err != nil {
		return nil, fmt.Errorf("store: sweep dispatchable: query: %w", err)
	}
	ready := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: sweep dispatchable: scan: %w", err)
		}
		ready = append(ready, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: sweep dispatchable: rows: %w", err)
	}

	now := time.Now().UnixMilli()
	for i := range ready {
		if _, err := tx.Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
			string(StatusRunning), now, ready[i].ID); err != nil {
			return nil, fmt.Errorf("store: sweep dispatchable: mark #%d: %w", ready[i].ID, err)
		}
		from := ready[i].Status
		ready[i].Status = StatusRunning
		ready[i].UpdatedAt = now
		slog.Info("swarm task auto-dispatch", "id", ready[i].ID, "from", from, "assignee", ready[i].Assignee)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: sweep dispatchable: commit: %w", err)
	}
	if err := s.fillDeps(ready); err != nil {
		return nil, err
	}
	return ready, nil
}

// UnmetDeps lists the incomplete dependencies holding a task in blocked —
// the tool layer renders these into "blocked by #2, #3" messages.
func (s *Store) UnmetDeps(id int64) ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT d.depends_on_id FROM task_deps d JOIN tasks p ON p.id = d.depends_on_id
		 WHERE d.task_id = ? AND p.status != 'completed' ORDER BY d.depends_on_id`, id)
	if err != nil {
		return nil, fmt.Errorf("store: unmet deps of %d: %w", id, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var dep int64
		if err := rows.Scan(&dep); err != nil {
			return nil, fmt.Errorf("store: unmet deps of %d: scan: %w", id, err)
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

// GetTask returns one task by id (DependsOn included), or ErrTaskNotFound.
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
	ts := []Task{t}
	if err := s.fillDeps(ts); err != nil {
		return Task{}, err
	}
	return ts[0], nil
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.fillDeps(out); err != nil {
		return nil, err
	}
	return out, nil
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
		&result, &verifyNote, &parentID, &t.VerifyPolicy, &t.CreatedAt, &t.UpdatedAt); err != nil {
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

// fillDeps loads DependsOn for every task in ts — one IN query, joined in
// memory. Called after the rows scan (never inside an open rows loop).
func (s *Store) fillDeps(ts []Task) error {
	if len(ts) == 0 {
		return nil
	}
	ph := make([]string, len(ts))
	args := make([]any, len(ts))
	idx := make(map[int64]int, len(ts))
	for i := range ts {
		ph[i] = "?"
		args[i] = ts[i].ID
		idx[ts[i].ID] = i
	}
	rows, err := s.db.Query(
		`SELECT task_id, depends_on_id FROM task_deps
		 WHERE task_id IN (`+strings.Join(ph, ",")+`) ORDER BY task_id, depends_on_id`, args...)
	if err != nil {
		return fmt.Errorf("store: load deps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, dep int64
		if err := rows.Scan(&taskID, &dep); err != nil {
			return fmt.Errorf("store: load deps: scan: %w", err)
		}
		i := idx[taskID]
		ts[i].DependsOn = append(ts[i].DependsOn, dep)
	}
	return rows.Err()
}
