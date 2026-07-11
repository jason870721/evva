package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSetTaskChecksRoundTrip: evidence persists, overwrites (latest run
// only), never bumps updated_at, and survives a store reopen (restart).
func TestSetTaskChecksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id := taskInState(t, st, StatusVerifying)
	before, _ := st.GetTask(id)
	if before.Checks != nil {
		t.Fatalf("fresh task carries evidence: %+v", before.Checks)
	}

	fail := CheckEvidence{Command: "go test ./...", Exit: 1, DurationMs: 1200, StartedAt: 42, Workdir: dir, Tail: "FAIL pkg/x", Pass: false}
	if err := st.SetTaskChecks(id, fail); err != nil {
		t.Fatalf("SetTaskChecks: %v", err)
	}
	got, _ := st.GetTask(id)
	if got.Checks == nil || got.Checks.Exit != 1 || got.Checks.Pass || got.Checks.Tail != "FAIL pkg/x" {
		t.Fatalf("evidence = %+v, want the fail run", got.Checks)
	}
	if got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("SetTaskChecks bumped updated_at (%d -> %d) — it must not reset the stale clock", before.UpdatedAt, got.UpdatedAt)
	}

	// Overwrite: a re-run replaces, never appends.
	pass := CheckEvidence{Command: "go test ./...", Exit: 0, DurationMs: 900, StartedAt: 43, Pass: true}
	if err := st.SetTaskChecks(id, pass); err != nil {
		t.Fatalf("SetTaskChecks(overwrite): %v", err)
	}
	if got, _ := st.GetTask(id); !got.Checks.Pass || got.Checks.StartedAt != 43 {
		t.Fatalf("evidence after overwrite = %+v, want the pass run", got.Checks)
	}

	// Survives restart.
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if got, _ := st2.GetTask(id); got.Checks == nil || !got.Checks.Pass {
		t.Fatalf("evidence after reopen = %+v, want the pass run", got.Checks)
	}

	if err := st2.SetTaskChecks(99999, pass); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("unknown task: err = %v, want ErrTaskNotFound", err)
	}
}

// TestWriterMatrixChecksGate pins the CHK half of the writer matrix: the
// system actor settles a verify:"checks" task ONLY over passing evidence;
// the leader may overrule a red (or missing) check.
func TestWriterMatrixChecksGate(t *testing.T) {
	seed := func(t *testing.T, st *Store) int64 {
		t.Helper()
		return seedGraphTask(t, st, StatusVerifying, "", VerifyChecks)
	}

	t.Run("system_no_evidence_refused", func(t *testing.T) {
		st := openTemp(t)
		id := seed(t, st)
		if err := st.TransitionTask(id, StatusCompleted, asSystem(), ""); !errors.Is(err, ErrNotLeader) {
			t.Fatalf("err = %v, want ErrNotLeader", err)
		}
		if got, _ := st.GetTask(id); got.Status != StatusVerifying {
			t.Fatalf("refused write mutated status to %s", got.Status)
		}
	})

	t.Run("system_fail_evidence_refused", func(t *testing.T) {
		st := openTemp(t)
		id := seed(t, st)
		if err := st.SetTaskChecks(id, CheckEvidence{Command: "c", Exit: 1, Pass: false}); err != nil {
			t.Fatal(err)
		}
		if err := st.TransitionTask(id, StatusCompleted, asSystem(), ""); !errors.Is(err, ErrNotLeader) {
			t.Fatalf("err = %v, want ErrNotLeader", err)
		}
	})

	t.Run("system_pass_evidence_completes", func(t *testing.T) {
		st := openTemp(t)
		id := seed(t, st)
		if err := st.SetTaskChecks(id, CheckEvidence{Command: "c", Exit: 0, Pass: true}); err != nil {
			t.Fatal(err)
		}
		if err := st.TransitionTask(id, StatusCompleted, asSystem(), "checks passed"); err != nil {
			t.Fatalf("system completion over green evidence: %v", err)
		}
		if got, _ := st.GetTask(id); got.Status != StatusCompleted {
			t.Fatalf("status = %s, want completed", got.Status)
		}
	})

	t.Run("system_pass_evidence_auto_unaffected", func(t *testing.T) {
		// The auto policy stays evidence-blind: it completes regardless.
		st := openTemp(t)
		id := seedGraphTask(t, st, StatusVerifying, "", VerifyAuto)
		if err := st.TransitionTask(id, StatusCompleted, asSystem(), ""); err != nil {
			t.Fatalf("auto settlement: %v", err)
		}
	})

	t.Run("leader_overrules_red_check", func(t *testing.T) {
		st := openTemp(t)
		id := seed(t, st)
		if err := st.SetTaskChecks(id, CheckEvidence{Command: "c", Exit: 1, Pass: false}); err != nil {
			t.Fatal(err)
		}
		if err := st.TransitionTask(id, StatusCompleted, leader(), "flaky test, known-red main"); err != nil {
			t.Fatalf("leader overrule: %v", err)
		}
	})
}

// TestCheckOffPersists: the birth-time opt-out survives create/read/list.
func TestCheckOffPersists(t *testing.T) {
	st := openTemp(t)
	id, err := st.CreateTask(Task{Title: "docs", Spec: "s", Assignee: "w", CreatedBy: "l", CheckOff: true})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	on, err := st.CreateTask(Task{Title: "code", Spec: "s", Assignee: "w", CreatedBy: "l"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if got, _ := st.GetTask(id); !got.CheckOff {
		t.Fatal("CheckOff lost on GetTask")
	}
	all, err := st.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, tk := range all {
		if tk.ID == id && !tk.CheckOff {
			t.Fatal("CheckOff lost on ListTasks")
		}
		if tk.ID == on && tk.CheckOff {
			t.Fatal("CheckOff leaked onto a default task")
		}
	}
}

// TestMigration0007AppliesToPopulatedLedger: a real pre-CHK ledger (migrations
// 1..6, rows in the old column set) opens through the normal path; old rows
// read back with nil evidence and check_off false, and evidence writes work
// post-migration.
func TestMigration0007AppliesToPopulatedLedger(t *testing.T) {
	dir := t.TempDir()
	vero := filepath.Join(dir, ".vero")
	if err := os.MkdirAll(vero, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(vero, "vero.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("version of %s: %v", name, err)
		}
		if v > 6 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1)`, v); err != nil {
			t.Fatalf("record legacy %s: %v", name, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (title, spec, status, assignee, created_by, verify_policy, created_at, updated_at)
		 VALUES ('old-row', 's', 'verifying', 'w', 'leader', 'leader', 1, 1)`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open over legacy ledger: %v", err)
	}
	defer st.Close()

	got, err := st.GetTask(1)
	if err != nil {
		t.Fatalf("GetTask(legacy row): %v", err)
	}
	if got.Checks != nil || got.CheckOff {
		t.Fatalf("legacy row after 0007 = checks:%+v off:%v, want nil/false", got.Checks, got.CheckOff)
	}
	if err := st.SetTaskChecks(1, CheckEvidence{Command: "c", Exit: 0, Pass: true}); err != nil {
		t.Fatalf("evidence write post-migration: %v", err)
	}
	if got, _ := st.GetTask(1); got.Checks == nil || !got.Checks.Pass {
		t.Fatalf("evidence readback post-migration = %+v", got.Checks)
	}
}
