package store

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

// retention fixture: a ledger holding every eligibility class at once.
//
//	messages: m-old-read   read 40d ago            → vacuumable
//	          m-new-read   read just now           → survives (too fresh)
//	          m-unread     90d old, never read     → survives (unread is sacred)
//	          m-claimed    old, claimed in-flight  → survives (read_at NULL)
//	          m-ref        unread, ref_task=4      → survives AND pins task 4
//	tasks:    1 done-old                           → vacuumable
//	          2 done-new                           → survives (too fresh)
//	          3 running-old                        → survives (not completed)
//	          4 done-old, ref'd by surviving m-ref → survives (pinned)
//	          5 done-old, parent of running 6     → survives (fixpoint pin)
//	          6 running, child of 5               → survives
//	          7 done-old, parent of done-old 8    → vacuumable (whole branch goes)
//	          8 done-old, child of 7              → vacuumable
func buildRetentionFixture(t *testing.T, st *Store) (cutoff time.Time) {
	t.Helper()
	now := time.Now()
	old := now.AddDate(0, 0, -40).UnixMilli()
	veryOld := now.AddDate(0, 0, -90).UnixMilli()
	fresh := now.UnixMilli()

	rawTask := func(id int64, status Status, stamp int64, parent *int64) {
		t.Helper()
		if _, err := st.db.Exec(
			`INSERT INTO tasks (id, title, spec, status, assignee, created_by, parent_id, created_at, updated_at)
			 VALUES (?, ?, '', ?, 'w', 'leader', ?, ?, ?)`,
			id, fmt.Sprintf("t%d", id), string(status), nullableInt(parent), stamp, stamp); err != nil {
			t.Fatalf("raw task %d: %v", id, err)
		}
	}
	p5, p7 := int64(5), int64(7)
	rawTask(1, StatusCompleted, old, nil)
	rawTask(2, StatusCompleted, fresh, nil)
	rawTask(3, StatusRunning, old, nil)
	rawTask(4, StatusCompleted, old, nil)
	rawTask(5, StatusCompleted, old, nil)
	rawTask(6, StatusRunning, old, &p5)
	rawTask(7, StatusCompleted, old, nil)
	rawTask(8, StatusCompleted, old, &p7)

	put := func(id string, createdAt int64, readAt *int64, refTask *int64) {
		t.Helper()
		if err := st.PutMessage(Message{
			ID: id, Sender: "a", Recipient: "b", Body: "x",
			CreatedAt: createdAt, ReadAt: readAt, RefTask: refTask,
		}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	t4 := int64(4)
	put("m-old-read", veryOld, &old, nil)
	put("m-new-read", old, &fresh, nil)
	put("m-unread", veryOld, nil, nil)
	put("m-claimed", veryOld, nil, nil)
	put("m-ref", fresh, nil, &t4)
	if _, err := st.db.Exec(`UPDATE messages SET claimed_at = ? WHERE id = 'm-claimed'`, old); err != nil {
		t.Fatalf("claim m-claimed: %v", err)
	}

	return now.AddDate(0, 0, -30)
}

func snapshot(t *testing.T, st *Store) ([]Message, []Task) {
	t.Helper()
	msgs, err := st.ListMessages(0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	tasks, err := st.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	return msgs, tasks
}

// RP-16 DoD#1: dry-run counts match the real run; live data (unread, claimed,
// active/pinned tasks) is byte-identical before and after; the doomed rows are
// gone afterwards.
func TestVacuumDryRunThenReal(t *testing.T) {
	st := openTemp(t)
	cutoff := buildRetentionFixture(t, st)
	beforeMsgs, beforeTasks := snapshot(t, st)

	dry, err := st.Vacuum(cutoff, true)
	if err != nil {
		t.Fatalf("dry vacuum: %v", err)
	}
	if dry.Messages != 1 || dry.Tasks != 3 {
		t.Fatalf("dry stats = %d msgs / %d tasks, want 1 / 3", dry.Messages, dry.Tasks)
	}
	if len(dry.Files) != 0 {
		t.Fatalf("dry run touched archive files: %v", dry.Files)
	}
	if m, tk := snapshot(t, st); !reflect.DeepEqual(m, beforeMsgs) || !reflect.DeepEqual(tk, beforeTasks) {
		t.Fatal("dry run mutated the ledger")
	}

	real, err := st.Vacuum(cutoff, false)
	if err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	if real.Messages != dry.Messages || real.Tasks != dry.Tasks {
		t.Fatalf("real stats %d/%d != dry stats %d/%d", real.Messages, real.Tasks, dry.Messages, dry.Tasks)
	}
	if len(real.Files) == 0 {
		t.Fatal("real vacuum reported no archive files")
	}

	// Survivors: exactly the fixture's protected rows, byte-identical.
	afterMsgs, afterTasks := snapshot(t, st)
	wantMsgs := filterMessages(beforeMsgs, "m-new-read", "m-unread", "m-claimed", "m-ref")
	if !reflect.DeepEqual(afterMsgs, wantMsgs) {
		t.Fatalf("surviving messages mutated:\n got %+v\nwant %+v", afterMsgs, wantMsgs)
	}
	wantTasks := filterTasks(beforeTasks, 2, 3, 4, 5, 6)
	if !reflect.DeepEqual(afterTasks, wantTasks) {
		t.Fatalf("surviving tasks mutated:\n got %+v\nwant %+v", afterTasks, wantTasks)
	}

	// Idempotent: nothing left to clear.
	again, err := st.Vacuum(cutoff, false)
	if err != nil {
		t.Fatalf("second vacuum: %v", err)
	}
	if again.Messages != 0 || again.Tasks != 0 {
		t.Fatalf("second vacuum cleared %d/%d, want 0/0", again.Messages, again.Tasks)
	}

	// DoD#2: the archive is re-readable and carries exactly the deleted rows.
	var recs []ArchiveRecord
	for _, f := range real.Files {
		r, err := ReadArchive(f)
		if err != nil {
			t.Fatalf("ReadArchive(%s): %v", f, err)
		}
		recs = append(recs, r...)
	}
	var gotMsgIDs []string
	var gotTaskIDs []int64
	for _, r := range recs {
		switch r.Kind {
		case "message":
			gotMsgIDs = append(gotMsgIDs, r.Message.ID)
		case "task":
			gotTaskIDs = append(gotTaskIDs, r.Task.ID)
		default:
			t.Fatalf("unknown archive kind %q", r.Kind)
		}
	}
	sort.Slice(gotTaskIDs, func(i, j int) bool { return gotTaskIDs[i] < gotTaskIDs[j] })
	if !reflect.DeepEqual(gotMsgIDs, []string{"m-old-read"}) || !reflect.DeepEqual(gotTaskIDs, []int64{1, 7, 8}) {
		t.Fatalf("archive contents = msgs %v tasks %v, want [m-old-read] [1 7 8]", gotMsgIDs, gotTaskIDs)
	}
}

// A later pass appends to the same month file as a second gzip member, and the
// reader sees both batches.
func TestVacuumArchiveAppends(t *testing.T) {
	st := openTemp(t)
	now := time.Now()
	old := now.AddDate(0, 0, -40).UnixMilli()
	created := now.AddDate(0, 0, -45).UnixMilli() // both rows in the same month bucket

	mk := func(id string) {
		if err := st.PutMessage(Message{ID: id, Sender: "a", Recipient: "b", Body: "x", CreatedAt: created, ReadAt: &old}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	cutoff := now.AddDate(0, 0, -30)

	mk("first")
	one, err := st.Vacuum(cutoff, false)
	if err != nil || one.Messages != 1 {
		t.Fatalf("first vacuum = %+v, %v", one, err)
	}
	mk("second")
	two, err := st.Vacuum(cutoff, false)
	if err != nil || two.Messages != 1 {
		t.Fatalf("second vacuum = %+v, %v", two, err)
	}
	if len(one.Files) != 1 || len(two.Files) != 1 || one.Files[0] != two.Files[0] {
		t.Fatalf("same-month passes hit different files: %v vs %v", one.Files, two.Files)
	}

	recs, err := ReadArchive(one.Files[0])
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	if len(recs) != 2 || recs[0].Message.ID != "first" || recs[1].Message.ID != "second" {
		t.Fatalf("appended archive = %+v, want first then second", recs)
	}
}

func filterMessages(in []Message, keep ...string) []Message {
	set := map[string]bool{}
	for _, k := range keep {
		set[k] = true
	}
	out := make([]Message, 0)
	for _, m := range in {
		if set[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

func filterTasks(in []Task, keep ...int64) []Task {
	set := map[int64]bool{}
	for _, k := range keep {
		set[k] = true
	}
	out := make([]Task, 0)
	for _, t := range in {
		if set[t.ID] {
			out = append(out, t)
		}
	}
	return out
}
