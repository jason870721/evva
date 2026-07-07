package workflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/observable"
)

// Sentinel errors callers branch on. Detail is wrapped with %w.
var (
	ErrNotFound    = errors.New("task not found")
	ErrDepNotFound = errors.New("dependency not found")
)

// Store is the per-agent board: tasks, their dependency edges, and the
// append-only session log that rebuilds them on resume.
//
// Mutations are guarded by mu; Notify fires after the lock is released so
// observers may freely call back into the store. Safe for concurrent
// access — the wf_task_* tools (root actor) and the dispatch engine
// (system actor) share one instance via ToolState.
type Store struct {
	observable.Observable

	mu      sync.Mutex
	tasks   map[string]*Task
	order   []string
	nextID  int64
	dir     string // persistence root; "" = in-memory only
	session string
	logFile *os.File
}

// NewStore returns an empty, in-memory board. Call SetPersistence +
// SetSession to attach it to a session log.
func NewStore() *Store {
	return &Store{tasks: map[string]*Task{}}
}

// Domain identifies this store on the change stream. Required by the
// observable.Store interface.
func (s *Store) Domain() string { return Domain }

// SetPersistence sets the directory session logs live under
// (<dir>/<session>.jsonl). Takes effect at the next SetSession.
func (s *Store) SetPersistence(dir string) {
	s.mu.Lock()
	s.dir = dir
	s.mu.Unlock()
}

// SetSession rotates the board to the given session id: the in-memory
// state is reset and, when a persistence dir is configured, the session's
// log is replayed (a fresh id starts an empty board) and kept open for
// appends. Mirrors checkpoint.Manager.SetSession — called at boot, on
// resume, and on /clear.
func (s *Store) SetSession(id string) error {
	s.mu.Lock()
	defer func() {
		snapshot := s.snapshotLocked()
		s.mu.Unlock()
		s.notifyReplaced(snapshot)
	}()

	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	s.tasks = map[string]*Task{}
	s.order = nil
	s.nextID = 0
	s.session = id

	if s.dir == "" || id == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("workflow: create log dir: %w", err)
	}
	path := filepath.Join(s.dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("workflow: open session log: %w", err)
	}
	if err := s.replayLocked(f); err != nil {
		_ = f.Close()
		return err
	}
	s.logFile = f
	return nil
}

// Close releases the session log handle. The board stays readable.
func (s *Store) Close() {
	s.mu.Lock()
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	s.mu.Unlock()
}

// logRecord is one jsonl line. Each mutation appends the task's full
// snapshot ("put") or a tombstone ("del"); replay is last-write-wins per
// id. Snapshot lines make replay immune to patch-semantics drift across
// versions — unknown fields are simply dropped by encoding/json.
type logRecord struct {
	Op   string    `json:"op"` // put | del
	Task *Task     `json:"task,omitempty"`
	ID   string    `json:"id,omitempty"`
	TS   time.Time `json:"ts"`
}

// replayLocked rebuilds state from the open log. Tolerates CRLF endings,
// blank lines, and unparseable lines (skipped — the log is best-effort
// session state, not a financial ledger). Reads from offset 0; the handle
// is O_APPEND so later writes still land at the end.
func (s *Store) replayLocked(f *os.File) error {
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("workflow: seek session log: %w", err)
	}
	sc := bufio.NewScanner(f)
	// Results and descriptions can be long; the default 64KB token cap is
	// too small. 4MB bounds a single record generously.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch rec.Op {
		case "put":
			if rec.Task == nil || rec.Task.ID == "" {
				continue
			}
			t := *rec.Task
			if _, seen := s.tasks[t.ID]; !seen {
				s.order = append(s.order, t.ID)
			}
			s.tasks[t.ID] = &t
			if n, err := strconv.ParseInt(t.ID, 36, 64); err == nil && n > s.nextID {
				s.nextID = n
			}
		case "del":
			if _, seen := s.tasks[rec.ID]; seen {
				delete(s.tasks, rec.ID)
				s.removeFromOrderLocked(rec.ID)
			}
		}
	}
	return sc.Err()
}

func (s *Store) removeFromOrderLocked(id string) {
	for i, v := range s.order {
		if v == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

// appendLocked writes one record to the session log. Best-effort: an
// in-memory-only store (no log) is silent, and a write failure does not
// roll back the in-memory mutation — the board favors availability.
func (s *Store) appendLocked(rec logRecord) {
	if s.logFile == nil {
		return
	}
	rec.TS = time.Now()
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = s.logFile.Write(append(b, '\n'))
}

func (s *Store) putLocked(t *Task) {
	t.UpdatedAt = time.Now()
	s.appendLocked(logRecord{Op: "put", Task: t})
}

// notifyReplaced emits the whole board so observers re-render from one
// event (the todo-store pattern). Callers must NOT hold mu.
func (s *Store) notifyReplaced(snapshot []Task) {
	s.Notify(observable.Change{
		Domain:  Domain,
		Op:      "replaced",
		Payload: snapshot,
	})
}

func (s *Store) snapshotLocked() []Task {
	out := make([]Task, 0, len(s.order))
	for _, id := range s.order {
		if t, ok := s.tasks[id]; ok {
			out = append(out, cloneTask(t))
		}
	}
	return out
}

func cloneTask(t *Task) Task {
	c := *t
	if t.Worker != nil {
		w := *t.Worker
		c.Worker = &w
	}
	c.DependsOn = append([]string(nil), t.DependsOn...)
	c.Comments = append([]Comment(nil), t.Comments...)
	return c
}

// ---- creation ----

// Create validates and inserts a task. The store assigns the id and the
// birth status: blocked iff any dependency is incomplete, else pending
// (a dependency on an already-completed task is satisfied at birth).
// Dependencies must reference existing tasks — with edges immutable after
// creation this makes the graph acyclic by construction.
func (s *Store) Create(t Task) (Task, error) {
	if strings.TrimSpace(t.Subject) == "" {
		return Task{}, errors.New("subject is required")
	}
	switch t.Verify {
	case "":
		t.Verify = VerifyLeader
	case VerifyLeader, VerifyAuto:
	default:
		return Task{}, fmt.Errorf("verify must be %q or %q, got %q", VerifyLeader, VerifyAuto, t.Verify)
	}
	if t.Worker == nil && t.Verify == VerifyAuto {
		return Task{}, errors.New(`verify:"auto" requires a worker — a self-task is completed by you, not the engine`)
	}
	if t.Worker != nil && strings.TrimSpace(t.Worker.AgentType) == "" {
		return Task{}, errors.New("worker.agent_type is required")
	}

	s.mu.Lock()
	blocked := false
	for _, dep := range t.DependsOn {
		d, ok := s.tasks[dep]
		if !ok {
			s.mu.Unlock()
			return Task{}, fmt.Errorf("%w: %q (dependencies may only reference existing tasks)", ErrDepNotFound, dep)
		}
		if d.Status != StatusCompleted {
			blocked = true
		}
	}
	s.nextID++
	now := time.Now()
	nt := &Task{
		ID:          strconv.FormatInt(s.nextID, 36),
		Subject:     strings.TrimSpace(t.Subject),
		Description: t.Description,
		ActiveForm:  t.ActiveForm,
		Status:      StatusPending,
		Verify:      t.Verify,
		DependsOn:   append([]string(nil), t.DependsOn...),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if blocked {
		nt.Status = StatusBlocked
	}
	if t.Worker != nil {
		w := *t.Worker
		nt.Worker = &w
	}
	s.tasks[nt.ID] = nt
	s.order = append(s.order, nt.ID)
	s.putLocked(nt)
	out := cloneTask(nt)
	snapshot := s.snapshotLocked()
	s.mu.Unlock()

	s.notifyReplaced(snapshot)
	return out, nil
}

// ---- the writer matrix ----

// transitionErr is the §4 writer matrix of the SDW PRD as one decision
// function: nil means (from → to) is legal for actor on this task. Root
// holds every judgment edge; System holds only the mechanical edges that
// execute structure the root declared at creation. Kept in one place and
// exhaustively product-tested so a future edge can't quietly widen an
// actor's power.
func transitionErr(t *Task, to Status, actor Actor) error {
	from := t.Status
	deny := func(reason string) error {
		return fmt.Errorf("illegal transition %s → %s by %s: %s", from, to, actor, reason)
	}
	type edge struct {
		from, to Status
	}
	switch (edge{from, to}) {
	case edge{StatusBlocked, StatusPending}:
		return nil // root: force-unblock; system: last dependency completed
	case edge{StatusPending, StatusRunning}:
		if actor == ActorSystem && !t.EngineManaged() {
			return deny("only engine-managed tasks (with a worker) are system-dispatched")
		}
		if actor == ActorRoot && t.EngineManaged() {
			return deny("the engine dispatches worker tasks — do not hand-start them")
		}
		return nil
	case edge{StatusRunning, StatusVerifying}:
		if actor == ActorRoot && t.EngineManaged() {
			return deny("a worker task reaches verifying via its worker's report")
		}
		if actor == ActorSystem && !t.EngineManaged() {
			return deny("the system only records worker reports — self-tasks are the root's")
		}
		return nil
	case edge{StatusRunning, StatusCompleted}:
		if actor != ActorRoot || t.EngineManaged() {
			return deny("only the root completes its own self-tasks directly")
		}
		return nil
	case edge{StatusRunning, StatusPending}:
		if actor != ActorSystem || !t.EngineManaged() {
			return deny("running returns to pending only when the system loses a worker")
		}
		return nil
	case edge{StatusVerifying, StatusCompleted}:
		if actor == ActorSystem {
			if !t.EngineManaged() || t.Verify != VerifyAuto {
				return deny(`only verify:"auto" worker tasks complete without root judgment`)
			}
			if t.WorkerFailed {
				return deny("the worker failed — auto-complete refused, root must verify")
			}
		}
		return nil
	case edge{StatusVerifying, StatusPending}:
		if actor == ActorSystem && !t.EngineManaged() {
			return deny("self-tasks re-open only by root judgment")
		}
		return nil // root: reject → rework re-dispatch; system: worker lost
	}
	return deny("no such edge in the writer matrix")
}

// Transition moves a task along one edge of the writer matrix, appending
// note (when non-empty) to the audit comments. Owner and WorkerFailed are
// cleared on any edge back to pending so a re-dispatch starts clean.
func (s *Store) Transition(id string, to Status, actor Actor, note string) (Task, error) {
	if !to.IsValid() {
		return Task{}, fmt.Errorf("invalid status %q", to)
	}
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if err := transitionErr(t, to, actor); err != nil {
		s.mu.Unlock()
		return Task{}, err
	}
	t.Status = to
	if to == StatusPending {
		t.Owner = ""
		t.WorkerFailed = false
	}
	if note != "" {
		t.Comments = append(t.Comments, Comment{By: actor, Note: note, At: time.Now()})
	}
	s.putLocked(t)
	// Completion cascades inside the same mutation — a dependent can never
	// be left blocked behind a completed dependency, no matter which caller
	// (tool or engine) drove the edge.
	if to == StatusCompleted {
		s.unblockDependentsLocked(id)
	}
	out := cloneTask(t)
	snapshot := s.snapshotLocked()
	s.mu.Unlock()

	s.notifyReplaced(snapshot)
	return out, nil
}

// Dispatch is the engine's pending → running edge plus the owner stamp in
// one mutation: the task records which worker daemon carries it.
func (s *Store) Dispatch(id, owner string) (Task, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if err := transitionErr(t, StatusRunning, ActorSystem); err != nil {
		s.mu.Unlock()
		return Task{}, err
	}
	t.Status = StatusRunning
	t.Owner = owner
	t.WorkerFailed = false
	s.putLocked(t)
	out := cloneTask(t)
	snapshot := s.snapshotLocked()
	s.mu.Unlock()

	s.notifyReplaced(snapshot)
	return out, nil
}

// CompleteWork is the worker's terminal report arriving through the
// engine: result recorded and running → verifying in one mutation. failed
// marks a crash/kill instead of a natural report — the matrix then refuses
// to auto-complete regardless of the verify policy.
func (s *Store) CompleteWork(id, result string, failed bool) (Task, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if err := transitionErr(t, StatusVerifying, ActorSystem); err != nil {
		s.mu.Unlock()
		return Task{}, err
	}
	t.Status = StatusVerifying
	t.Result = result
	t.WorkerFailed = failed
	s.putLocked(t)
	out := cloneTask(t)
	snapshot := s.snapshotLocked()
	s.mu.Unlock()

	s.notifyReplaced(snapshot)
	return out, nil
}

// UnblockDependents flips every blocked dependent of completedID whose
// dependencies are now all complete to pending (the system edge). Returns
// the flipped tasks. Idempotent — a second call finds nothing blocked.
// Transition runs this automatically on every edge into completed; the
// exported form exists for defensive resweeps.
func (s *Store) UnblockDependents(completedID string) []Task {
	s.mu.Lock()
	flipped := s.unblockDependentsLocked(completedID)
	var snapshot []Task
	if len(flipped) > 0 {
		snapshot = s.snapshotLocked()
	}
	s.mu.Unlock()

	if len(flipped) > 0 {
		s.notifyReplaced(snapshot)
	}
	return flipped
}

func (s *Store) unblockDependentsLocked(completedID string) []Task {
	var flipped []Task
	for _, id := range s.order {
		t := s.tasks[id]
		if t.Status != StatusBlocked || !slices.Contains(t.DependsOn, completedID) {
			continue
		}
		if len(s.unmetDepsLocked(t)) > 0 {
			continue
		}
		t.Status = StatusPending
		t.Owner = ""
		t.WorkerFailed = false
		s.putLocked(t)
		flipped = append(flipped, cloneTask(t))
	}
	return flipped
}

// unmetDepsLocked returns the dependency ids not yet completed. A missing
// dependency (corrupt log) counts as unmet forever — force-unblock is the
// escape hatch.
func (s *Store) unmetDepsLocked(t *Task) []string {
	var unmet []string
	for _, dep := range t.DependsOn {
		d, ok := s.tasks[dep]
		if !ok || d.Status != StatusCompleted {
			unmet = append(unmet, dep)
		}
	}
	return unmet
}

// UnmetDeps returns the incomplete dependency ids of a task.
func (s *Store) UnmetDeps(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil
	}
	return s.unmetDepsLocked(t)
}

// Dispatchable returns, in creation order, every pending engine-managed
// task. Pending implies dependencies are satisfied (blocked birth + the
// unblock edge) or the root force-unblocked — either way the engine may
// dispatch. The concurrency cap is the engine's business, not the store's.
func (s *Store) Dispatchable() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Task
	for _, id := range s.order {
		t := s.tasks[id]
		if t.Status == StatusPending && t.EngineManaged() {
			out = append(out, cloneTask(t))
		}
	}
	return out
}

// ---- root edits ----

// Patch carries the root's field edits for Update. nil pointers leave the
// field untouched; Note (non-empty) appends an audit comment.
type Patch struct {
	Subject     *string
	Description *string
	ActiveForm  *string
	Note        string
}

// Update applies the root's field edits. Completed tasks are history —
// only a note may be appended.
func (s *Store) Update(id string, p Patch) (Task, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	editing := p.Subject != nil || p.Description != nil || p.ActiveForm != nil
	if editing && t.Status == StatusCompleted {
		s.mu.Unlock()
		return Task{}, errors.New("completed tasks are history — create a follow-up task instead")
	}
	if p.Subject != nil {
		if strings.TrimSpace(*p.Subject) == "" {
			s.mu.Unlock()
			return Task{}, errors.New("subject cannot be emptied")
		}
		t.Subject = strings.TrimSpace(*p.Subject)
	}
	if p.Description != nil {
		t.Description = *p.Description
	}
	if p.ActiveForm != nil {
		t.ActiveForm = *p.ActiveForm
	}
	if p.Note != "" {
		t.Comments = append(t.Comments, Comment{By: ActorRoot, Note: p.Note, At: time.Now()})
	}
	s.putLocked(t)
	out := cloneTask(t)
	snapshot := s.snapshotLocked()
	s.mu.Unlock()

	s.notifyReplaced(snapshot)
	return out, nil
}

// Delete removes a task created in error. Guarded: a running task has a
// live worker (stop it first), and a depended-on task would orphan its
// dependents' edges — both are refused with the reason.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if t.Status == StatusRunning {
		s.mu.Unlock()
		return errors.New("task is running — stop its worker (daemon_stop) or let it finish first")
	}
	if deps := s.dependentsLocked(id); len(deps) > 0 {
		s.mu.Unlock()
		return fmt.Errorf("tasks %s depend on it — delete or complete them first", strings.Join(deps, ", "))
	}
	delete(s.tasks, id)
	s.removeFromOrderLocked(id)
	s.appendLocked(logRecord{Op: "del", ID: id})
	snapshot := s.snapshotLocked()
	s.mu.Unlock()

	s.notifyReplaced(snapshot)
	return nil
}

// Dependents lists ids of tasks that name id in their DependsOn — the
// cascade a completion may unblock, in creation order.
func (s *Store) Dependents(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dependentsLocked(id)
}

// dependentsLocked lists ids of tasks that name id in their DependsOn.
func (s *Store) dependentsLocked(id string) []string {
	var out []string
	for _, tid := range s.order {
		if slices.Contains(s.tasks[tid].DependsOn, id) {
			out = append(out, tid)
		}
	}
	return out
}

// ---- reads & recovery ----

// Get returns a copy of one task.
func (s *Store) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return cloneTask(t), true
}

// List returns copies of every task in creation order.
func (s *Store) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Counts tallies tasks per status — the workflow daemon's snapshot meta.
func (s *Store) Counts() map[Status]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[Status]int{}
	for _, t := range s.tasks {
		out[t.Status]++
	}
	return out
}

// ResetLostRunning re-queues running tasks whose worker did not survive a
// restart: alive reports whether an owner daemon id is still live. Each
// reset task returns to pending with an audit comment; the caller follows
// with a dispatch sweep. The restart-recovery half of the engine.
func (s *Store) ResetLostRunning(alive func(owner string) bool) []Task {
	s.mu.Lock()
	var reset []Task
	for _, id := range s.order {
		t := s.tasks[id]
		// Only engine-managed tasks have workers to lose; a running
		// self-task is the root's own in-flight work and survives resume.
		if t.Status != StatusRunning || !t.EngineManaged() {
			continue
		}
		if t.Owner != "" && alive != nil && alive(t.Owner) {
			continue
		}
		t.Status = StatusPending
		t.Owner = ""
		t.WorkerFailed = false
		t.Comments = append(t.Comments, Comment{
			By: ActorSystem, Note: "worker lost on session resume — re-queued", At: time.Now(),
		})
		s.putLocked(t)
		reset = append(reset, cloneTask(t))
	}
	var snapshot []Task
	if len(reset) > 0 {
		snapshot = s.snapshotLocked()
	}
	s.mu.Unlock()

	if len(reset) > 0 {
		s.notifyReplaced(snapshot)
	}
	return reset
}
