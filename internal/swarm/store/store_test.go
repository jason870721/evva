package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func leader() Actor { return Actor{Name: "leader", Role: RoleLeader} }

// TestPutMessageIfNewDedup (RP-9): the same idempotency key collapses to one
// durable row; a fresh key inserts; an empty key is rejected.
func TestPutMessageIfNewDedup(t *testing.T) {
	st := openTemp(t)

	ins, existing, err := st.PutMessageIfNew(Message{ID: "x1", Sender: "webhook", Recipient: "leader", Body: "evt"}, "k1")
	if err != nil || !ins || existing != "" {
		t.Fatalf("first = inserted:%v existing:%q err:%v, want inserted", ins, existing, err)
	}
	ins2, existing2, err := st.PutMessageIfNew(Message{ID: "x2", Sender: "webhook", Recipient: "leader", Body: "retry"}, "k1")
	if err != nil || ins2 || existing2 != "x1" {
		t.Fatalf("same key = inserted:%v existing:%q err:%v, want dup of x1", ins2, existing2, err)
	}
	ins3, _, err := st.PutMessageIfNew(Message{ID: "x3", Sender: "webhook", Recipient: "leader", Body: "e"}, "k2")
	if err != nil || !ins3 {
		t.Fatalf("new key = inserted:%v err:%v, want inserted", ins3, err)
	}
	if _, _, err := st.PutMessageIfNew(Message{ID: "x4", Sender: "s", Recipient: "r", Body: "b"}, ""); err == nil {
		t.Error("empty idempotency key should error")
	}

	// Exactly two rows persisted (k1 once + k2), proving the retry didn't double-write.
	msgs, err := st.ListMessages(0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Sender == "webhook" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("webhook rows = %d, want 2 (k1 once, k2 once)", n)
	}
}

// TestOpenCreatesDBWithPragmas covers AC#1 (WAL) and proves the DSN
// per-connection pragmas took (foreign_keys on).
func TestOpenCreatesDBWithPragmas(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := os.Stat(filepath.Join(dir, ".vero", "vero.db")); err != nil {
		t.Fatalf(".vero/vero.db not created: %v", err)
	}

	var mode string
	if err := st.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	var fk int
	if err := st.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1 (DSN per-connection pragma did not apply)", fk)
	}

	var busy int
	if err := st.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busy)
	}
}

// TestForeignKeyEnforced proves foreign_keys is actually ON, not just reported:
// a message referencing a non-existent task must be rejected.
func TestForeignKeyEnforced(t *testing.T) {
	st := openTemp(t)
	ghost := int64(424242)
	err := st.PutMessage(Message{ID: "fk", Sender: "a", Recipient: "b", Body: "x", RefTask: &ghost})
	if err == nil {
		t.Fatal("PutMessage with a dangling ref_task should violate the FK, got nil")
	}
}

// TestOpenIsIdempotent: re-opening an existing .vero/vero.db does not re-run
// migrations or error (forward-only runner skips applied versions).
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	st1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	id, err := st1.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "leader"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	_ = st1.Close()

	st2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer st2.Close()
	if _, err := st2.GetTask(id); err != nil {
		t.Fatalf("task did not survive reopen: %v", err)
	}
}

// taskInState creates a fresh task and drives it through the canonical legal
// path to the requested state. Used to seed each cell of the matrix.
func taskInState(t *testing.T, st *Store, target Status) int64 {
	t.Helper()
	id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "leader"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	path := map[Status][]Status{
		StatusPending:   {},
		StatusRunning:   {StatusRunning},
		StatusSuspended: {StatusRunning, StatusSuspended},
		StatusVerifying: {StatusRunning, StatusVerifying},
		StatusCompleted: {StatusRunning, StatusVerifying, StatusCompleted},
	}
	for _, step := range path[target] {
		if err := st.TransitionTask(id, step, leader(), ""); err != nil {
			t.Fatalf("seed %s: step ->%s failed: %v", target, step, err)
		}
	}
	return id
}

// TestTaskStateMachine is the centerpiece (AC#2): every (from,to) pair across
// all 5 states. The expected legal set is hardcoded here, independent of the
// implementation's map, so a bug in legalTransitions can't pass by testing
// itself. Illegal transitions must error AND leave the row unmutated.
func TestTaskStateMachine(t *testing.T) {
	all := []Status{StatusPending, StatusRunning, StatusSuspended, StatusVerifying, StatusCompleted}
	expected := map[Status]map[Status]bool{
		StatusPending:   {StatusRunning: true},
		StatusRunning:   {StatusSuspended: true, StatusVerifying: true},
		StatusSuspended: {StatusRunning: true},
		StatusVerifying: {StatusCompleted: true, StatusRunning: true},
		StatusCompleted: {},
	}

	for _, from := range all {
		for _, to := range all {
			from, to := from, to
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				st := openTemp(t)
				id := taskInState(t, st, from)

				err := st.TransitionTask(id, to, leader(), "")
				got, gerr := st.GetTask(id)
				if gerr != nil {
					t.Fatalf("GetTask: %v", gerr)
				}

				if expected[from][to] {
					if err != nil {
						t.Fatalf("legal %s->%s returned error: %v", from, to, err)
					}
					if got.Status != to {
						t.Fatalf("after legal %s->%s status = %s, want %s", from, to, got.Status, to)
					}
				} else {
					if !errors.Is(err, ErrIllegalTransition) {
						t.Fatalf("illegal %s->%s: err = %v, want ErrIllegalTransition", from, to, err)
					}
					if got.Status != from {
						t.Fatalf("illegal %s->%s mutated status to %s (want unchanged)", from, to, got.Status)
					}
				}
			})
		}
	}
}

// TestLeaderOnlyWrites covers AC#3.
func TestLeaderOnlyWrites(t *testing.T) {
	st := openTemp(t)
	id := taskInState(t, st, StatusPending)

	worker := Actor{Name: "w", Role: RoleWorker}
	if err := st.TransitionTask(id, StatusRunning, worker, ""); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("worker transition: err = %v, want ErrNotLeader", err)
	}
	got, _ := st.GetTask(id)
	if got.Status != StatusPending {
		t.Fatalf("non-leader write mutated status to %s", got.Status)
	}
}

// TestCreateRequiresAssignee covers AC#4.
func TestCreateRequiresAssignee(t *testing.T) {
	st := openTemp(t)
	if _, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "  ", CreatedBy: "leader"}); !errors.Is(err, ErrEmptyAssignee) {
		t.Fatalf("err = %v, want ErrEmptyAssignee", err)
	}
}

func TestTransitionUnknownTask(t *testing.T) {
	st := openTemp(t)
	if err := st.TransitionTask(99999, StatusRunning, leader(), ""); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

// TestListTasksPaginationAndCount covers RP-6: ListTasks honors Limit/Offset and
// Newest ordering, CountTasks reports the full match total (not the page size),
// Statuses filters by an IN-list, and a zero-value filter still returns every
// row oldest-first (the unchanged default).
func TestListTasksPaginationAndCount(t *testing.T) {
	st := openTemp(t)

	// 7 completed (ids ascending, oldest→newest) + 2 pending + 1 running = 10.
	var completedIDs []int64
	for i := 0; i < 7; i++ {
		completedIDs = append(completedIDs, taskInState(t, st, StatusCompleted))
	}
	taskInState(t, st, StatusPending)
	taskInState(t, st, StatusPending)
	taskInState(t, st, StatusRunning)

	// CountTasks ignores Limit/Offset — it's the full total for the WHERE.
	if n, err := st.CountTasks(TaskFilter{Status: StatusCompleted}); err != nil || n != 7 {
		t.Fatalf("CountTasks(completed) = %d, %v; want 7", n, err)
	}

	// Newest-first page 1: the 5 highest completed ids, descending.
	page1, err := st.ListTasks(TaskFilter{Status: StatusCompleted, Limit: 5, Newest: true})
	if err != nil {
		t.Fatalf("ListTasks page1: %v", err)
	}
	if len(page1) != 5 || page1[0].ID != completedIDs[6] || page1[4].ID != completedIDs[2] {
		t.Fatalf("page1 ids = %v, want newest-first %d..%d", idsOf(page1), completedIDs[6], completedIDs[2])
	}

	// Page 2 via Offset: the remaining 2, still newest-first.
	page2, err := st.ListTasks(TaskFilter{Status: StatusCompleted, Limit: 5, Offset: 5, Newest: true})
	if err != nil {
		t.Fatalf("ListTasks page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != completedIDs[1] || page2[1].ID != completedIDs[0] {
		t.Fatalf("page2 ids = %v, want [%d %d]", idsOf(page2), completedIDs[1], completedIDs[0])
	}

	// Statuses IN-list: the active set (pending+running) = 3 rows, oldest-first.
	active, err := st.ListTasks(TaskFilter{Statuses: []Status{StatusPending, StatusRunning}})
	if err != nil {
		t.Fatalf("ListTasks active: %v", err)
	}
	if len(active) != 3 {
		t.Fatalf("active len = %d (%v), want 3", len(active), idsOf(active))
	}

	// Zero-value filter: all 10 rows, oldest-first (regression — default unchanged).
	all, err := st.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	if len(all) != 10 || all[0].ID != completedIDs[0] {
		t.Fatalf("all len = %d (want 10), first id = %d (want %d)", len(all), all[0].ID, completedIDs[0])
	}
}

func idsOf(tasks []Task) []int64 {
	out := make([]int64, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestVerifyNoteWritten(t *testing.T) {
	st := openTemp(t)
	id := taskInState(t, st, StatusVerifying)
	if err := st.TransitionTask(id, StatusRunning, leader(), "needs rework: add tests"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTask(id)
	if got.VerifyNote != "needs rework: add tests" {
		t.Fatalf("verify_note = %q, want the rework note", got.VerifyNote)
	}
}

// TestMessageRoundTripAndMarkRead covers part of AC#5.
func TestMessageRoundTripAndMarkRead(t *testing.T) {
	st := openTemp(t)
	m := Message{ID: "m-1", Sender: "alice", Recipient: "bob", Subject: "hi", Body: "schema changed"}
	if err := st.PutMessage(m); err != nil {
		t.Fatalf("PutMessage: %v", err)
	}

	got, err := st.GetMessage("m-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Sender != "alice" || got.Recipient != "bob" || got.Subject != "hi" || got.Body != "schema changed" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.ReadAt != nil {
		t.Fatalf("new message should be unread, got read_at=%v", *got.ReadAt)
	}

	if err := st.MarkRead("m-1"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	got2, _ := st.GetMessage("m-1")
	if got2.ReadAt == nil {
		t.Fatal("MarkRead did not set read_at")
	}

	// idempotent on already-read; not-found is an error.
	if err := st.MarkRead("m-1"); err != nil {
		t.Fatalf("second MarkRead should be a no-op, got %v", err)
	}
	if err := st.MarkRead("nope"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("MarkRead(missing): err = %v, want ErrMessageNotFound", err)
	}
}

func TestPutMessageValidation(t *testing.T) {
	st := openTemp(t)
	if err := st.PutMessage(Message{ID: "", Sender: "a", Recipient: "b", Body: "x"}); !errors.Is(err, ErrEmptyMessageID) {
		t.Fatalf("empty id: err = %v, want ErrEmptyMessageID", err)
	}
	if err := st.PutMessage(Message{ID: "x", Sender: "a", Recipient: "", Body: "x"}); !errors.Is(err, ErrIncompleteMessage) {
		t.Fatalf("empty recipient: err = %v, want ErrIncompleteMessage", err)
	}
}

// TestUnreadForOrderingAndExclusion covers the rest of AC#5: unread-only,
// oldest-first, recipient-isolated.
func TestUnreadForOrderingAndExclusion(t *testing.T) {
	st := openTemp(t)
	// Insert out of order; explicit created_at controls ordering.
	msgs := []Message{
		{ID: "b2", Sender: "a", Recipient: "bob", Body: "2", CreatedAt: 2000},
		{ID: "b1", Sender: "a", Recipient: "bob", Body: "1", CreatedAt: 1000},
		{ID: "b3", Sender: "a", Recipient: "bob", Body: "3", CreatedAt: 3000},
		{ID: "c1", Sender: "a", Recipient: "carol", Body: "x", CreatedAt: 1500},
	}
	for _, m := range msgs {
		if err := st.PutMessage(m); err != nil {
			t.Fatalf("PutMessage %s: %v", m.ID, err)
		}
	}

	ids, err := st.UnreadFor("bob")
	if err != nil {
		t.Fatalf("UnreadFor: %v", err)
	}
	if want := []string{"b1", "b2", "b3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("UnreadFor(bob) = %v, want %v (oldest first)", ids, want)
	}

	if err := st.MarkRead("b1"); err != nil {
		t.Fatal(err)
	}
	ids, _ = st.UnreadFor("bob")
	if want := []string{"b2", "b3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("after MarkRead(b1): %v, want %v", ids, want)
	}

	// recipient isolation
	if ids, _ := st.UnreadFor("carol"); !reflect.DeepEqual(ids, []string{"c1"}) {
		t.Fatalf("UnreadFor(carol) = %v, want [c1]", ids)
	}
	if ids, _ := st.UnreadFor("nobody"); !reflect.DeepEqual(ids, []string{}) {
		t.Fatalf("UnreadFor(nobody) = %v, want empty", ids)
	}
}

// TestClaimLifecycle covers RP-1's unread→claimed→read message lifecycle: a
// claim is exclusive (drain B can't re-fold a claimed row), and a clean run
// settles every claim — the start batch (ClaimUnread) plus a mid-run fold
// (ClaimOne) — to read in one SettleClaimed.
func TestClaimLifecycle(t *testing.T) {
	st := openTemp(t)
	put := func(id string, at int64) {
		if err := st.PutMessage(Message{ID: id, Sender: "a", Recipient: "bob", Body: id, CreatedAt: at}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	put("m1", 1000)
	put("m2", 2000)

	// ClaimUnread takes the whole unread batch oldest-first and claims each.
	batch, err := st.ClaimUnread("bob")
	if err != nil {
		t.Fatalf("ClaimUnread: %v", err)
	}
	if len(batch) != 2 || batch[0].ID != "m1" || batch[1].ID != "m2" {
		t.Fatalf("ClaimUnread = %+v, want [m1 m2]", batch)
	}
	for _, m := range batch {
		if m.ClaimedAt == nil || m.ReadAt != nil {
			t.Fatalf("claimed row wants claimed_at set, read_at nil: %+v", m)
		}
	}

	// A second ClaimUnread sees nothing — the batch is claimed, not yet read.
	if again, _ := st.ClaimUnread("bob"); len(again) != 0 {
		t.Fatalf("second ClaimUnread = %+v, want empty (already claimed)", again)
	}
	// ClaimOne on an already-claimed row is refused (the drain-B dedup).
	if _, ok, _ := st.ClaimOne("m1"); ok {
		t.Fatal("ClaimOne on a claimed row should return ok=false")
	}

	// A message arriving now IS claimable by drain B.
	put("m3", 3000)
	got, ok, err := st.ClaimOne("m3")
	if err != nil || !ok || got.ID != "m3" {
		t.Fatalf("ClaimOne(m3) = %+v ok=%v err=%v, want m3/true", got, ok, err)
	}

	// One settle marks every claimed row (batch + drain-B fold) read.
	if err := st.SettleClaimed("bob"); err != nil {
		t.Fatalf("SettleClaimed: %v", err)
	}
	if ids, _ := st.UnreadFor("bob"); len(ids) != 0 {
		t.Fatalf("after settle UnreadFor = %v, want empty", ids)
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if m, _ := st.GetMessage(id); m.ReadAt == nil {
			t.Fatalf("%s should be read after settle", id)
		}
	}
}

// TestUnclaimForResetsAbortedRun: a run that claims mail then aborts must leave
// the mail UNREAD and re-claimable (the opposite-of-lost guarantee), and must
// never resurrect an already-read message.
func TestUnclaimForResetsAbortedRun(t *testing.T) {
	st := openTemp(t)
	if err := st.PutMessage(Message{ID: "m1", Sender: "a", Recipient: "bob", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimUnread("bob"); err != nil {
		t.Fatal(err)
	}
	if err := st.UnclaimFor("bob"); err != nil { // run aborted → reset
		t.Fatalf("UnclaimFor: %v", err)
	}
	if ids, _ := st.UnreadFor("bob"); !reflect.DeepEqual(ids, []string{"m1"}) {
		t.Fatalf("after unclaim UnreadFor = %v, want [m1]", ids)
	}
	if batch, _ := st.ClaimUnread("bob"); len(batch) != 1 || batch[0].ID != "m1" {
		t.Fatalf("re-claim after unclaim = %+v, want [m1]", batch)
	}
	// Settle, then a stray unclaim must NOT bring it back.
	_ = st.SettleClaimed("bob")
	_ = st.UnclaimFor("bob")
	if ids, _ := st.UnreadFor("bob"); len(ids) != 0 {
		t.Fatalf("UnclaimFor must not resurrect a read message: %v", ids)
	}
}

// TestConcurrentReadersAndWriter covers AC#6: with -race, concurrent readers
// and writers through the RWMutex DAO must not race. Writers do bounded work,
// then signal readers to stop.
func TestConcurrentReadersAndWriter(t *testing.T) {
	st := openTemp(t)
	seed := taskInState(t, st, StatusPending)
	if err := st.PutMessage(Message{ID: "seed", Sender: "a", Recipient: "bob", Body: "x"}); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var readers, writers sync.WaitGroup

	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = st.ListTasks(TaskFilter{})
				_, _ = st.GetTask(seed)
				_, _ = st.UnreadFor("bob")
			}
		}()
	}

	for i := 0; i < 3; i++ {
		writers.Add(1)
		go func(n int) {
			defer writers.Done()
			for j := 0; j < 25; j++ {
				id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "leader"})
				if err != nil {
					t.Errorf("CreateTask: %v", err)
					return
				}
				_ = st.TransitionTask(id, StatusRunning, leader(), "")
				if err := st.PutMessage(Message{ID: msgID(n, j), Sender: "a", Recipient: "bob", Body: "y"}); err != nil {
					t.Errorf("PutMessage: %v", err)
					return
				}
			}
		}(i)
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	tasks, err := st.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("final ListTasks: %v", err)
	}
	// seed + 3 writers × 25 creates = 76
	if len(tasks) != 1+3*25 {
		t.Fatalf("task count = %d, want %d", len(tasks), 1+3*25)
	}
}

func msgID(worker, n int) string {
	return "w" + string(rune('0'+worker)) + "-" + string(rune('a'+n%26)) + string(rune('0'+n/26))
}
